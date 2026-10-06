package control

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"net/http"
	"nimbus/internal/security"
	"regexp"
	"strings"
	"time"
)

var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func (s *Server) addCountry(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !countryPattern.MatchString(in.Code) || len(in.Name) < 1 || len(in.Name) > 80 {
		return fail(400, "INVALID_COUNTRY")
	}
	_, e := s.DB.Exec(r.Context(), `INSERT INTO countries(code,name) VALUES($1,$2) ON CONFLICT(code) DO UPDATE SET name=$2`, in.Code, in.Name)
	if e != nil {
		return e
	}
	write(w, 201, in)
	return nil
}
func hostValid(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	if len(h) < 1 || len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !idPattern.MatchString(l) {
			return false
		}
	}
	return true
}
func (s *Server) addEndpoint(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID         string `json:"id"`
		Country    string `json:"country_code"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		ServerName string `json:"server_name"`
		Capacity   int    `json:"capacity"`
		AuthToken  string `json:"auth_token"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if !idPattern.MatchString(in.ID) || !countryPattern.MatchString(in.Country) || !hostValid(in.Host) || !hostValid(in.ServerName) || in.Port < 1 || in.Port > 65535 || in.Capacity < 1 || in.Capacity > 5000 || len(in.AuthToken) < 32 || len(in.AuthToken) > 256 {
		return fail(400, "INVALID_ENDPOINT")
	}
	var exists bool
	e := s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM countries WHERE code=$1)`, in.Country).Scan(&exists)
	if e != nil {
		return e
	}
	if !exists {
		return fail(400, "COUNTRY_NOT_FOUND")
	}
	tag, e := s.DB.Exec(r.Context(), `INSERT INTO endpoints(id,country_code,host,port,server_name,capacity,auth_token_hash) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, in.ID, in.Country, in.Host, in.Port, in.ServerName, in.Capacity, security.Hash(in.AuthToken))
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return fail(409, "ENDPOINT_EXISTS")
	}
	write(w, 201, map[string]any{"id": in.ID, "country_code": in.Country, "host": in.Host, "port": in.Port, "server_name": in.ServerName, "capacity": in.Capacity})
	return nil
}
func (s *Server) setEndpoint(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled   *bool  `json:"enabled"`
		AuthToken string `json:"auth_token"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Enabled == nil && in.AuthToken == "" {
		return fail(400, "INVALID_ENDPOINT")
	}
	if in.AuthToken != "" && (len(in.AuthToken) < 32 || len(in.AuthToken) > 256) {
		return fail(400, "INVALID_ENDPOINT")
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `UPDATE endpoints SET enabled=COALESCE($2,enabled),auth_token_hash=COALESCE($3,auth_token_hash) WHERE id=$1`, r.PathValue("id"), in.Enabled, func() any {
		if in.AuthToken == "" {
			return nil
		}
		return security.Hash(in.AuthToken)
	}())
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return fail(404, "ENDPOINT_NOT_FOUND")
	}
	if in.AuthToken != "" {
		if _, e = tx.Exec(ctx, `UPDATE endpoints SET ready=false WHERE id=$1`, r.PathValue("id")); e != nil {
			return e
		}
	}
	if (in.Enabled != nil && !*in.Enabled) || in.AuthToken != "" {
		if _, e = tx.Exec(ctx, `UPDATE sessions SET state='revoked' WHERE endpoint_id=$1 AND state IN ('pending','issued','active')`, r.PathValue("id")); e != nil {
			return e
		}
	}
	if e = audit(ctx, tx, "endpoint.update", r.PathValue("id")); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 204, nil)
	return nil
}
func (s *Server) listEndpoints(w http.ResponseWriter, r *http.Request) error {
	rows, e := s.DB.Query(r.Context(), `SELECT id,country_code,host,port,enabled,ready,last_seen_at FROM endpoints ORDER BY id`)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, c, h string
		var p int
		var enabled, ready bool
		var seen *time.Time
		if e = rows.Scan(&id, &c, &h, &p, &enabled, &ready, &seen); e != nil {
			return e
		}
		out = append(out, map[string]any{"id": id, "country_code": c, "host": h, "port": p, "enabled": enabled, "ready": ready, "last_seen_at": seen})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	write(w, 200, map[string]any{"endpoints": out})
	return nil
}
func (s *Server) countries(w http.ResponseWriter, r *http.Request) error {
	if _, e := s.user(r); e != nil {
		return e
	}
	rows, e := s.DB.Query(r.Context(), `SELECT c.code,c.name,EXISTS(SELECT 1 FROM endpoints e WHERE e.country_code=c.code AND e.enabled AND e.ready AND e.last_seen_at>now()-interval '10 seconds') FROM countries c WHERE c.enabled ORDER BY c.code`)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var code, name string
		var available bool
		if e = rows.Scan(&code, &name, &available); e != nil {
			return e
		}
		out = append(out, map[string]any{"code": code, "name": name, "available": available})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	write(w, 200, map[string]any{"countries": out})
	return nil
}
func decodeLarge(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return fail(400, "INVALID_JSON")
	}
	return nil
}
func (s *Server) publish(w http.ResponseWriter, r *http.Request) error {
	kind := r.PathValue("kind")
	if kind != "policy" && kind != "manifest" {
		return fail(400, "INVALID_DOCUMENT_KIND")
	}
	var in struct {
		Version int64           `json:"version"`
		Payload json.RawMessage `json:"payload"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if in.Version < 1 || len(in.Payload) == 0 || in.Payload[0] != '{' {
		return fail(400, "INVALID_DOCUMENT")
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7483282)`); e != nil {
		return e
	}
	var last int64
	if e = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM documents WHERE kind=$1`, kind).Scan(&last); e != nil {
		return e
	}
	if in.Version <= last {
		return fail(409, "VERSION_MUST_INCREASE")
	}
	if _, e = tx.Exec(ctx, `INSERT INTO documents(kind,version,payload) VALUES($1,$2,$3)`, kind, in.Version, []byte(in.Payload)); e != nil {
		return e
	}
	if e = audit(ctx, tx, "document.publish", kind); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 201, map[string]any{"version": in.Version})
	return nil
}
func (s *Server) document(kind string) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if _, e := s.user(r); e != nil {
			return e
		}
		var version int64
		var payload json.RawMessage
		var created time.Time
		e := s.DB.QueryRow(r.Context(), `SELECT version,payload,created_at FROM documents WHERE kind=$1 ORDER BY version DESC LIMIT 1`, kind).Scan(&version, &payload, &created)
		if errors.Is(e, pgx.ErrNoRows) {
			return fail(404, "DOCUMENT_NOT_PUBLISHED")
		}
		if e != nil {
			return e
		}
		// Clients verify the exact decoded payload bytes, never a reserialized object.
		signed, e := json.Marshal(map[string]any{"kind": kind, "version": version, "published_at": created, "valid_until": time.Now().UTC().Add(24 * time.Hour), "content": payload})
		if e != nil {
			return e
		}
		write(w, 200, map[string]any{"algorithm": "Ed25519", "payload": base64.StdEncoding.EncodeToString(signed), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(s.Signing, signed))})
		return nil
	}
}
