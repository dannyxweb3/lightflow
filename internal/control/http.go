package control

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http"
	"nimbus/internal/security"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	DB                *pgxpool.Pool
	AdminKey          string
	AdminPasswordHash string
	AdminCookieSecure bool
	Master            []byte
	Signing           ed25519.PrivateKey
	LeaseTTL          time.Duration
	AckTimeout        time.Duration
	DummyPassword     string
}
type fault struct {
	Status int
	Code   string
}

func (e fault) Error() string            { return e.Code }
func fail(status int, code string) error { return fault{status, code} }

type handler func(http.ResponseWriter, *http.Request) error

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fail(400, "INVALID_JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return fail(400, "INVALID_JSON")
	}
	return nil
}
func wrap(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := security.ID()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		defer func() {
			if recover() != nil {
				slog.Error("request panic", "request_id", id)
				write(w, 500, map[string]any{"code": "INTERNAL_ERROR", "request_id": id})
			}
		}()
		if err := h(w, r); err != nil {
			f := fault{500, "INTERNAL_ERROR"}
			var known fault
			if errors.As(err, &known) {
				f = known
			} else {
				slog.Error("request failed", "request_id", id)
			}
			write(w, f.Status, map[string]any{"code": f.Code, "request_id": id, "retryable": f.Status >= 500 || f.Status == 429})
		}
	}
}
func (s *Server) user(r *http.Request) (string, error) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == r.Header.Get("Authorization") {
		return "", fail(401, "AUTH_REQUIRED")
	}
	var id string
	err := s.DB.QueryRow(r.Context(), `SELECT t.user_id FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.hash=$1 AND t.kind='access' AND t.expires_at>now() AND NOT t.consumed AND u.enabled`, security.Hash(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fail(401, "AUTH_EXPIRED")
	}
	return id, err
}
func (s *Server) admin(h handler) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if !security.Equal(r.Header.Get("Authorization"), "Bearer "+s.AdminKey) {
			return fail(401, "ADMIN_AUTH_REQUIRED")
		}
		return h(w, r)
	}
}
func (s *Server) Public() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", wrap(func(w http.ResponseWriter, r *http.Request) error {
		write(w, 200, map[string]string{"status": "ok"})
		return nil
	}))
	m.HandleFunc("GET /readyz", wrap(func(w http.ResponseWriter, r *http.Request) error {
		ctx, c := context.WithTimeout(r.Context(), time.Second)
		defer c()
		if s.DB.Ping(ctx) != nil {
			return fail(503, "DATABASE_UNAVAILABLE")
		}
		write(w, 200, map[string]string{"status": "ready"})
		return nil
	}))
	for p, h := range map[string]handler{
		"POST /v1/auth/login": s.login, "POST /v1/auth/refresh": s.refresh, "POST /v1/auth/logout": s.logout,
		"GET /v1/me": s.me, "POST /v1/devices": s.addDevice, "GET /v1/devices": s.devices, "DELETE /v1/devices/{id}": s.deleteDevice,
		"GET /v1/countries": s.countries, "POST /v1/connection-sessions": s.createSession,
		"POST /v1/connection-sessions/{id}/activate": s.activate, "POST /v1/connection-sessions/{id}/renew": s.renew, "DELETE /v1/connection-sessions/{id}": s.release,
		"GET /v1/policies/current": s.document("policy"), "GET /v1/artifacts/manifest": s.document("manifest"),
		"GET /metrics":      s.admin(s.metrics),
		"POST /admin/users": s.admin(s.addUser), "PUT /admin/users/{id}/subscription": s.admin(s.subscription),
		"POST /admin/countries": s.admin(s.addCountry), "POST /admin/endpoints": s.admin(s.addEndpoint), "PATCH /admin/endpoints/{id}": s.admin(s.setEndpoint),
		"GET /admin/endpoints": s.admin(s.listEndpoints), "POST /admin/documents/{kind}": s.admin(s.publish),
		"GET /admin/overview": s.admin(s.overview), "GET /admin/users": s.admin(s.listUsers),
		"GET /admin/users/{id}": s.admin(s.userDetail), "GET /admin/countries": s.admin(s.listCountries),
	} {
		m.HandleFunc(p, wrap(h))
	}
	s.mountConsole(m)
	return m
}
func (s *Server) Internal() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /internal/gateways/{id}/snapshot", wrap(s.snapshot))
	m.HandleFunc("POST /internal/gateways/{id}/ack", wrap(s.ack))
	return m
}
func (s *Server) gateway(r *http.Request) error {
	header := r.Header.Get("Authorization")
	token := strings.TrimPrefix(header, "Bearer ")
	if token == "" || token == header || len(token) > 256 {
		return fail(401, "GATEWAY_AUTH_REQUIRED")
	}
	var expected *string
	err := s.DB.QueryRow(r.Context(), `SELECT auth_token_hash FROM endpoints WHERE id=$1`, r.PathValue("id")).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) || expected == nil {
		return fail(401, "GATEWAY_AUTH_REQUIRED")
	}
	if err != nil {
		return err
	}
	if !security.Equal(*expected, security.Hash(token)) {
		return fail(401, "GATEWAY_AUTH_REQUIRED")
	}
	return nil
}

func (s *Server) proof(w http.ResponseWriter, r *http.Request, user string) (string, []byte, error) {
	id := r.Header.Get("X-Device-ID")
	stamp := r.Header.Get("X-Device-Timestamp")
	nonce := r.Header.Get("X-Device-Nonce")
	sec, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || time.Since(time.Unix(sec, 0)) > time.Minute || time.Until(time.Unix(sec, 0)) > time.Minute || len(nonce) < 16 || len(nonce) > 128 {
		return "", nil, fail(401, "INVALID_DEVICE_PROOF")
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if e != nil {
		return "", nil, fail(400, "INVALID_BODY")
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	var pub string
	e = s.DB.QueryRow(r.Context(), `SELECT public_key FROM devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, user).Scan(&pub)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", nil, fail(403, "DEVICE_REVOKED")
	}
	if e != nil {
		return "", nil, e
	}
	if !security.VerifyProof(pub, r.Header.Get("X-Device-Signature"), security.ProofMessage(r.Method, r.URL.EscapedPath(), stamp, nonce, body)) {
		return "", nil, fail(401, "INVALID_DEVICE_PROOF")
	}
	tag, e := s.DB.Exec(r.Context(), `INSERT INTO proof_nonces VALUES($1,$2,now()+interval '2 minutes') ON CONFLICT DO NOTHING`, id, nonce)
	if e != nil {
		return "", nil, e
	}
	if tag.RowsAffected() == 0 {
		return "", nil, fail(409, "PROOF_REPLAY")
	}
	return id, body, nil
}
func publicKeyValid(p string) bool {
	b, e := base64.StdEncoding.DecodeString(p)
	return e == nil && len(b) == ed25519.PublicKeySize
}
