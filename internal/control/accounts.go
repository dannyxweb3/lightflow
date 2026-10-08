package control

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"net/mail"
	"nimbus/internal/security"
	"strings"
	"time"
)

type subscriptionInput struct {
	Plan            string    `json:"plan"`
	ExpiresAt       time.Time `json:"expires_at"`
	DeviceLimit     int       `json:"device_limit"`
	ConcurrentLimit int       `json:"concurrent_limit"`
	Enabled         bool      `json:"enabled"`
}

func (v subscriptionInput) valid() bool {
	return len(v.Plan) > 0 && len(v.Plan) <= 80 && !v.ExpiresAt.IsZero() && v.DeviceLimit >= 1 && v.DeviceLimit <= 100 && v.ConcurrentLimit >= 1 && v.ConcurrentLimit <= 100
}
func audit(ctx context.Context, tx pgx.Tx, action, id string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(action,resource_id) VALUES($1,$2)`, action, id)
	return e
}
func (s *Server) addUser(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email        string            `json:"email"`
		Password     string            `json:"password"`
		Subscription subscriptionInput `json:"subscription"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	a, e := mail.ParseAddress(in.Email)
	if e != nil || a.Address != in.Email || len(in.Email) > 254 || !in.Subscription.valid() {
		return fail(400, "INVALID_USER")
	}
	hash, e := security.Password(in.Password)
	if e != nil {
		return fail(400, "INVALID_PASSWORD")
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	id := security.ID()
	tag, e := tx.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, in.Email, hash)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return fail(409, "EMAIL_EXISTS")
	}
	v := in.Subscription
	_, e = tx.Exec(ctx, `INSERT INTO subscriptions(user_id,plan,expires_at,device_limit,concurrent_limit,enabled) VALUES($1,$2,$3,$4,$5,$6)`, id, v.Plan, v.ExpiresAt, v.DeviceLimit, v.ConcurrentLimit, v.Enabled)
	if e != nil {
		return e
	}
	if e = audit(ctx, tx, "user.create", id); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 201, map[string]string{"id": id})
	return nil
}
func (s *Server) subscription(w http.ResponseWriter, r *http.Request) error {
	var v subscriptionInput
	if e := decode(w, r, &v); e != nil {
		return e
	}
	if !v.valid() {
		return fail(400, "INVALID_SUBSCRIPTION")
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	id := r.PathValue("id")
	var locked string
	e = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(e, pgx.ErrNoRows) {
		return fail(404, "USER_NOT_FOUND")
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE subscriptions SET plan=$2,expires_at=$3,device_limit=$4,concurrent_limit=$5,enabled=$6 WHERE user_id=$1`, id, v.Plan, v.ExpiresAt, v.DeviceLimit, v.ConcurrentLimit, v.Enabled)
	if e != nil {
		return e
	}
	// Every entitlement change revokes old leases, including downgrades.
	_, e = tx.Exec(ctx, `UPDATE sessions SET state='revoked' WHERE user_id=$1 AND state IN ('pending','issued','active')`, id)
	if e != nil {
		return e
	}
	if e = audit(ctx, tx, "subscription.update", id); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 200, map[string]string{"status": "updated", "connections": "revoked"})
	return nil
}
func (s *Server) rate(ctx context.Context, key string, limit int) error {
	var attempts int
	e := s.DB.QueryRow(ctx, `INSERT INTO login_limits(key,attempts,resets_at) VALUES($1,1,now()+interval '5 minutes') ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN login_limits.resets_at<=now() THEN 1 ELSE login_limits.attempts+1 END,resets_at=CASE WHEN login_limits.resets_at<=now() THEN now()+interval '5 minutes' ELSE login_limits.resets_at END RETURNING attempts`, security.Hash(key)).Scan(&attempts)
	if e != nil {
		return e
	}
	if attempts > limit {
		return fail(429, "RATE_LIMITED")
	}
	return nil
}
func issueTokens(ctx context.Context, tx pgx.Tx, user, family string) (map[string]any, error) {
	access, refresh := security.Random(32), security.Random(32)
	until := time.Now().UTC().Add(15 * time.Minute)
	var refreshUntil time.Time
	e := tx.QueryRow(ctx, `SELECT expires_at FROM tokens WHERE family=$1 AND kind='refresh' ORDER BY created_at LIMIT 1`, family).Scan(&refreshUntil)
	if errors.Is(e, pgx.ErrNoRows) {
		refreshUntil = time.Now().UTC().Add(7 * 24 * time.Hour)
	} else if e != nil {
		return nil, e
	}
	if !refreshUntil.After(time.Now()) {
		return nil, fail(401, "AUTH_EXPIRED")
	}
	_, e = tx.Exec(ctx, `INSERT INTO tokens(hash,user_id,family,kind,expires_at) VALUES($1,$2,$3,'access',$4),($5,$2,$3,'refresh',$6)`, security.Hash(access), user, family, until, security.Hash(refresh), refreshUntil)
	if e != nil {
		return nil, e
	}
	return map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_at": until}, nil
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Email) > 254 || len(in.Password) > 128 {
		return fail(401, "INVALID_CREDENTIALS")
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	w.Header().Set("Retry-After", "300")
	if e := s.rate(r.Context(), "login-ip:"+ip, 100); e != nil {
		return e
	}
	if e := s.rate(r.Context(), "login-email:"+in.Email, 10); e != nil {
		return e
	}
	w.Header().Del("Retry-After")
	var id, hash string
	e := s.DB.QueryRow(r.Context(), `SELECT id,password_hash FROM users WHERE email=$1 AND enabled`, in.Email).Scan(&id, &hash)
	if errors.Is(e, pgx.ErrNoRows) {
		security.CheckPassword(s.DummyPassword, in.Password)
		return fail(401, "INVALID_CREDENTIALS")
	}
	if e != nil {
		return e
	}
	if !security.CheckPassword(hash, in.Password) {
		return fail(401, "INVALID_CREDENTIALS")
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	out, e := issueTokens(r.Context(), tx, id, security.ID())
	if e != nil {
		return e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	write(w, 200, out)
	return nil
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"refresh_token"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Token) > 128 {
		return fail(401, "AUTH_EXPIRED")
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var user, family string
	var consumed bool
	e = tx.QueryRow(ctx, `SELECT user_id FROM tokens WHERE hash=$1 AND kind='refresh'`, security.Hash(in.Token)).Scan(&user)
	if errors.Is(e, pgx.ErrNoRows) {
		return fail(401, "AUTH_EXPIRED")
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); e != nil {
		return e
	}
	e = tx.QueryRow(ctx, `SELECT t.user_id,t.family,t.consumed FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.hash=$1 AND t.kind='refresh' AND t.expires_at>now() AND u.enabled FOR UPDATE OF t`, security.Hash(in.Token)).Scan(&user, &family, &consumed)
	if errors.Is(e, pgx.ErrNoRows) {
		return fail(401, "AUTH_EXPIRED")
	}
	if e != nil {
		return e
	}
	if consumed {
		if _, e = tx.Exec(ctx, `UPDATE tokens SET consumed=true WHERE family=$1`, family); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		return fail(401, "REFRESH_REPLAY")
	}
	if _, e = tx.Exec(ctx, `UPDATE tokens SET consumed=true WHERE hash=$1`, security.Hash(in.Token)); e != nil {
		return e
	}
	out, e := issueTokens(ctx, tx, user, family)
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 200, out)
	return nil
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE tokens SET consumed=true WHERE user_id=$1`, user); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE sessions SET state='revoked' WHERE user_id=$1 AND state IN ('pending','issued','active')`, user); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 204, nil)
	return nil
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	var email string
	var sub subscriptionInput
	e = s.DB.QueryRow(r.Context(), `SELECT u.email,s.plan,s.expires_at,s.device_limit,s.concurrent_limit,s.enabled FROM users u JOIN subscriptions s ON s.user_id=u.id WHERE u.id=$1`, user).Scan(&email, &sub.Plan, &sub.ExpiresAt, &sub.DeviceLimit, &sub.ConcurrentLimit, &sub.Enabled)
	if e != nil {
		return e
	}
	write(w, 200, map[string]any{"id": user, "email": email, "subscription": sub})
	return nil
}

// Account lock serializes device/session quota changes. Revalidate access after locking.
func (s *Server) entitlement(ctx context.Context, tx pgx.Tx, user, token string) (subscriptionInput, error) {
	var v subscriptionInput
	var enabled bool
	if _, e := tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); e != nil {
		return v, e
	}
	e := tx.QueryRow(ctx, `SELECT u.enabled,s.plan,s.expires_at,s.device_limit,s.concurrent_limit,s.enabled FROM users u JOIN subscriptions s ON s.user_id=u.id WHERE u.id=$1`, user).Scan(&enabled, &v.Plan, &v.ExpiresAt, &v.DeviceLimit, &v.ConcurrentLimit, &v.Enabled)
	if e != nil {
		return v, e
	}
	if !enabled || !v.Enabled || !v.ExpiresAt.After(time.Now()) {
		return v, fail(403, "SUBSCRIPTION_INACTIVE")
	}
	var ok bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tokens WHERE hash=$1 AND user_id=$2 AND kind='access' AND expires_at>now() AND NOT consumed)`, security.Hash(strings.TrimPrefix(token, "Bearer ")), user).Scan(&ok)
	if e != nil {
		return v, e
	}
	if !ok {
		return v, fail(401, "AUTH_EXPIRED")
	}
	return v, nil
}
func (s *Server) addDevice(w http.ResponseWriter, r *http.Request) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	var in struct {
		Name      string `json:"name"`
		OS        string `json:"os"`
		PublicKey string `json:"public_key"`
	}
	if e = decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Name) < 1 || len(in.Name) > 100 || !publicKeyValid(in.PublicKey) || (in.OS != "windows" && in.OS != "macos" && in.OS != "linux") {
		return fail(400, "INVALID_DEVICE")
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	v, e := s.entitlement(ctx, tx, user, r.Header.Get("Authorization"))
	if e != nil {
		return e
	}
	var id string
	var revoked *time.Time
	e = tx.QueryRow(ctx, `SELECT id,revoked_at FROM devices WHERE user_id=$1 AND public_key=$2`, user, in.PublicKey).Scan(&id, &revoked)
	if e == nil {
		if revoked != nil {
			return fail(409, "DEVICE_REVOKED")
		}
		write(w, 200, map[string]string{"id": id})
		return nil
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id=$1 AND revoked_at IS NULL`, user).Scan(&count); e != nil {
		return e
	}
	if count >= v.DeviceLimit {
		return fail(409, "DEVICE_LIMIT")
	}
	id = security.ID()
	_, e = tx.Exec(ctx, `INSERT INTO devices(id,user_id,name,os,public_key) VALUES($1,$2,$3,$4,$5)`, id, user, in.Name, in.OS, in.PublicKey)
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 201, map[string]string{"id": id})
	return nil
}
func (s *Server) devices(w http.ResponseWriter, r *http.Request) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	rows, e := s.DB.Query(r.Context(), `SELECT id,name,os,created_at FROM devices WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at`, user)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, os string
		var created time.Time
		if e = rows.Scan(&id, &name, &os, &created); e != nil {
			return e
		}
		out = append(out, map[string]any{"id": id, "name": name, "os": os, "created_at": created})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	write(w, 200, map[string]any{"devices": out})
	return nil
}
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) error {
	user, e := s.user(r)
	if e != nil {
		return e
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE devices SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2`, r.PathValue("id"), user)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return fail(404, "DEVICE_NOT_FOUND")
	}
	if _, e = tx.Exec(ctx, `UPDATE sessions SET state='revoked' WHERE device_id=$1 AND state IN ('pending','issued','active')`, r.PathValue("id")); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 204, nil)
	return nil
}
