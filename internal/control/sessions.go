package control

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"nimbus/internal/security"
	"strings"
	"time"
)

type session struct {
	ID         string     `json:"lease_id"`
	Device     string     `json:"device_id"`
	Endpoint   string     `json:"endpoint_id"`
	State      string     `json:"state"`
	Expires    time.Time  `json:"expires_at"`
	Confirmed  *time.Time `json:"-"`
	Country    string     `json:"country_code"`
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	ServerName string     `json:"server_name"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	device, _, e := s.proof(w, r, user)
	if e != nil {
		return e
	}
	var in struct {
		Country   string   `json:"country_code"`
		Protocols []string `json:"protocols"`
		Mode      string   `json:"mode"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if in.Mode != "smart" && in.Mode != "global" {
		return fail(400, "INVALID_MODE")
	}
	in.Country = strings.ToUpper(in.Country)
	if in.Country != "" && len(in.Country) != 2 {
		return fail(400, "INVALID_COUNTRY")
	}
	supported := false
	for _, p := range in.Protocols {
		if p == "hysteria2" {
			supported = true
		}
	}
	if !supported {
		return fail(409, "NO_COMPATIBLE_ENDPOINT")
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 128 {
		return fail(400, "INVALID_IDEMPOTENCY_KEY")
	}
	requestHash := security.Hash(device + "|" + in.Country + "|" + in.Mode + "|" + strings.Join(in.Protocols, ","))
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	sub, e := s.entitlement(ctx, tx, user, r.Header.Get("Authorization"))
	if e != nil {
		return e
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, device, user).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fail(403, "DEVICE_REVOKED")
	}
	var id, hash string
	e = tx.QueryRow(ctx, `SELECT id,request_hash FROM sessions WHERE user_id=$1 AND idempotency_key=$2`, user, key).Scan(&id, &hash)
	if e == nil {
		if hash != requestHash {
			return fail(409, "IDEMPOTENCY_CONFLICT")
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		return s.plan(w, r, user, id)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var count int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1 AND state IN ('pending','issued','active') AND expires_at>now()`, user).Scan(&count)
	if e != nil {
		return e
	}
	if count >= sub.ConcurrentLimit {
		return fail(409, "CONCURRENT_LIMIT")
	}
	// One live logical session per device. Reuse the same idempotency key for retries.
	e = tx.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE device_id=$1 AND state IN ('pending','issued','active') AND expires_at>now()`, device).Scan(&count)
	if e != nil {
		return e
	}
	if count > 0 {
		return fail(409, "DEVICE_ALREADY_CONNECTED")
	}
	var endpoint string
	// Row lock serializes capacity reservations across different accounts. A second
	// statement below counts committed reservations after any wait for this lock.
	e = tx.QueryRow(ctx, `SELECT e.id FROM endpoints e JOIN countries c ON c.code=e.country_code WHERE e.enabled AND c.enabled AND e.ready AND e.last_seen_at>now()-interval '10 seconds' AND ($1='' OR e.country_code=$1) AND (SELECT count(*) FROM sessions s WHERE s.endpoint_id=e.id AND s.state IN ('pending','issued','active') AND s.expires_at>now())<e.capacity ORDER BY (SELECT count(*)::float FROM sessions s WHERE s.endpoint_id=e.id AND s.state IN ('pending','issued','active') AND s.expires_at>now())/e.capacity,e.id FOR UPDATE OF e LIMIT 1`, in.Country).Scan(&endpoint)
	if errors.Is(e, pgx.ErrNoRows) {
		return fail(503, "NO_CAPACITY")
	}
	if e != nil {
		return e
	}
	var capacity int
	e = tx.QueryRow(ctx, `SELECT capacity,(SELECT count(*) FROM sessions WHERE endpoint_id=$1 AND state IN ('pending','issued','active') AND expires_at>now()) FROM endpoints WHERE id=$1`, endpoint).Scan(&capacity, &count)
	if e != nil {
		return e
	}
	if count >= capacity {
		return fail(503, "NO_CAPACITY")
	}
	until := time.Now().UTC().Add(s.LeaseTTL)
	if sub.ExpiresAt.Before(until) {
		until = sub.ExpiresAt
	}
	id = security.ID()
	_, e = tx.Exec(ctx, `INSERT INTO sessions(id,user_id,device_id,endpoint_id,state,expires_at,credential_hash,idempotency_key,request_hash) VALUES($1,$2,$3,$4,'pending',$5,$6,$7,$8)`, id, user, device, endpoint, until, security.Hash(security.Credential(s.Master, id)), key, requestHash)
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	return s.plan(w, r, user, id)
}
func (s *Server) plan(w http.ResponseWriter, r *http.Request, user, id string) error {
	deadline := time.NewTimer(s.AckTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var v session
		e := s.DB.QueryRow(r.Context(), `SELECT s.id,s.device_id,s.endpoint_id,s.state,s.expires_at,s.confirmed_until,e.country_code,e.host,e.port,e.server_name FROM sessions s JOIN endpoints e ON e.id=s.endpoint_id WHERE s.id=$1 AND s.user_id=$2`, id, user).Scan(&v.ID, &v.Device, &v.Endpoint, &v.State, &v.Expires, &v.Confirmed, &v.Country, &v.Host, &v.Port, &v.ServerName)
		if errors.Is(e, pgx.ErrNoRows) {
			return fail(404, "SESSION_NOT_FOUND")
		}
		if e != nil {
			return e
		}
		if (v.State != "pending" && v.State != "issued" && v.State != "active") || !v.Expires.After(time.Now()) {
			return fail(409, "SESSION_CLOSED")
		}
		if v.Confirmed != nil && !v.Confirmed.Before(v.Expires) {
			write(w, 200, map[string]any{"schema_version": 1, "lease_id": v.ID, "device_id": v.Device, "state": v.State, "expires_at": v.Expires, "country_code": v.Country, "renew_after_seconds": int(s.LeaseTTL.Seconds() / 3), "candidates": []map[string]any{{"endpoint_id": v.Endpoint, "protocol": "hysteria2", "transport": "quic", "host": v.Host, "port": v.Port, "credential": security.Credential(s.Master, v.ID), "public_params": map[string]string{"server_name": v.ServerName}}}})
			return nil
		}
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		case <-deadline.C:
			w.Header().Set("Retry-After", "1")
			write(w, 202, map[string]string{"lease_id": id, "state": "pending"})
			return nil
		case <-tick.C:
		}
	}
}
func (s *Server) mutateSession(w http.ResponseWriter, r *http.Request, action string) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	device, _, e := s.proof(w, r, user)
	if e != nil {
		return e
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var sub subscriptionInput
	if action == "renew" {
		sub, e = s.entitlement(ctx, tx, user, r.Header.Get("Authorization"))
	} else {
		_, e = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user)
	}
	if e != nil {
		return e
	}
	var state string
	var expires time.Time
	var confirmed *time.Time
	id := r.PathValue("id")
	e = tx.QueryRow(ctx, `SELECT state,expires_at,confirmed_until FROM sessions WHERE id=$1 AND user_id=$2 AND device_id=$3 FOR UPDATE`, id, user, device).Scan(&state, &expires, &confirmed)
	if errors.Is(e, pgx.ErrNoRows) {
		return fail(404, "SESSION_NOT_FOUND")
	}
	if e != nil {
		return e
	}
	if action == "release" {
		_, e = tx.Exec(ctx, `UPDATE sessions SET state='released' WHERE id=$1 AND state IN ('pending','issued','active')`, id)
	} else {
		if (state != "issued" && state != "active") || !expires.After(time.Now()) || confirmed == nil || !confirmed.After(time.Now()) {
			return fail(409, "SESSION_CLOSED")
		}
		if action == "activate" {
			_, e = tx.Exec(ctx, `UPDATE sessions SET state='active' WHERE id=$1`, id)
		} else {
			until := time.Now().UTC().Add(s.LeaseTTL)
			if sub.ExpiresAt.Before(until) {
				until = sub.ExpiresAt
			}
			// Preserve an in-flight renewal on retry; do not continually push its ACK target.
			if !confirmed.Before(expires) {
				_, e = tx.Exec(ctx, `UPDATE sessions SET expires_at=$2 WHERE id=$1`, id, until)
			}
		}
	}
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if action == "release" {
		write(w, 204, nil)
		return nil
	}
	return s.plan(w, r, user, id)
}
func (s *Server) activate(w http.ResponseWriter, r *http.Request) error {
	return s.mutateSession(w, r, "activate")
}
func (s *Server) renew(w http.ResponseWriter, r *http.Request) error {
	return s.mutateSession(w, r, "renew")
}
func (s *Server) release(w http.ResponseWriter, r *http.Request) error {
	return s.mutateSession(w, r, "release")
}
