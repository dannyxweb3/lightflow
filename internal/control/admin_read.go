package control

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) overview(w http.ResponseWriter, r *http.Request) error {
	var users, active, pending, ready, total int64
	e := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM users`).Scan(&users)
	if e != nil {
		return e
	}
	e = s.DB.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE state IN ('issued','active') AND expires_at>now()), count(*) FILTER (WHERE state='pending' AND expires_at>now()) FROM sessions`).Scan(&active, &pending)
	if e != nil {
		return e
	}
	e = s.DB.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE enabled AND ready AND last_seen_at>now()-interval '10 seconds'),count(*) FROM endpoints`).Scan(&ready, &total)
	if e != nil {
		return e
	}
	write(w, 200, map[string]any{"users": users, "active_sessions": active, "pending_sessions": pending, "ready_endpoints": ready, "total_endpoints": total})
	return nil
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 254 {
		return fail(400, "INVALID_SEARCH")
	}
	offset, e := strconv.Atoi(r.URL.Query().Get("offset"))
	if r.URL.Query().Get("offset") == "" {
		offset = 0
		e = nil
	}
	if e != nil || offset < 0 || offset > 1000000 {
		return fail(400, "INVALID_OFFSET")
	}
	rows, e := s.DB.Query(r.Context(), `SELECT u.id,u.email,u.enabled,u.created_at,sub.plan,sub.expires_at,sub.device_limit,sub.concurrent_limit,sub.enabled,(SELECT count(*) FROM devices d WHERE d.user_id=u.id AND d.revoked_at IS NULL),(SELECT count(*) FROM sessions x WHERE x.user_id=u.id AND x.state IN ('pending','issued','active') AND x.expires_at>now()) FROM users u JOIN subscriptions sub ON sub.user_id=u.id WHERE $1='' OR u.email ILIKE '%'||$1||'%' ORDER BY u.created_at DESC,u.id LIMIT 50 OFFSET $2`, search, offset)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email, plan string
		var enabled, subEnabled bool
		var created, expires time.Time
		var deviceLimit, concurrentLimit int
		var devices, sessions int64
		if e = rows.Scan(&id, &email, &enabled, &created, &plan, &expires, &deviceLimit, &concurrentLimit, &subEnabled, &devices, &sessions); e != nil {
			return e
		}
		out = append(out, map[string]any{"id": id, "email": email, "enabled": enabled, "created_at": created, "subscription": map[string]any{"plan": plan, "expires_at": expires, "device_limit": deviceLimit, "concurrent_limit": concurrentLimit, "enabled": subEnabled}, "device_count": devices, "active_sessions": sessions})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	write(w, 200, map[string]any{"users": out, "offset": offset, "limit": 50})
	return nil
}

func (s *Server) userDetail(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var email, plan string
	var enabled, subEnabled bool
	var created, expires time.Time
	var deviceLimit, concurrentLimit int
	e := s.DB.QueryRow(r.Context(), `SELECT u.email,u.enabled,u.created_at,sub.plan,sub.expires_at,sub.device_limit,sub.concurrent_limit,sub.enabled FROM users u JOIN subscriptions sub ON sub.user_id=u.id WHERE u.id=$1`, id).Scan(&email, &enabled, &created, &plan, &expires, &deviceLimit, &concurrentLimit, &subEnabled)
	if errors.Is(e, pgx.ErrNoRows) {
		return fail(404, "USER_NOT_FOUND")
	}
	if e != nil {
		return e
	}
	devices := []map[string]any{}
	rows, e := s.DB.Query(r.Context(), `SELECT id,name,os,created_at,revoked_at FROM devices WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if e != nil {
		return e
	}
	for rows.Next() {
		var did, name, os string
		var at time.Time
		var revoked *time.Time
		if e = rows.Scan(&did, &name, &os, &at, &revoked); e != nil {
			rows.Close()
			return e
		}
		devices = append(devices, map[string]any{"id": did, "name": name, "os": os, "created_at": at, "revoked_at": revoked})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	sessions := []map[string]any{}
	rows, e = s.DB.Query(r.Context(), `SELECT id,device_id,endpoint_id,state,created_at,expires_at FROM sessions WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if e != nil {
		return e
	}
	for rows.Next() {
		var sid, did, endpoint, state string
		var at, until time.Time
		if e = rows.Scan(&sid, &did, &endpoint, &state, &at, &until); e != nil {
			rows.Close()
			return e
		}
		sessions = append(sessions, map[string]any{"id": sid, "device_id": did, "endpoint_id": endpoint, "state": state, "created_at": at, "expires_at": until})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	write(w, 200, map[string]any{"id": id, "email": email, "enabled": enabled, "created_at": created, "subscription": map[string]any{"plan": plan, "expires_at": expires, "device_limit": deviceLimit, "concurrent_limit": concurrentLimit, "enabled": subEnabled}, "devices": devices, "sessions": sessions})
	return nil
}

func (s *Server) listCountries(w http.ResponseWriter, r *http.Request) error {
	rows, e := s.DB.Query(r.Context(), `SELECT code,name,enabled FROM countries ORDER BY code`)
	if e != nil {
		return e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var code, name string
		var enabled bool
		if e = rows.Scan(&code, &name, &enabled); e != nil {
			return e
		}
		out = append(out, map[string]any{"code": code, "name": name, "enabled": enabled})
	}
	if e = rows.Err(); e != nil {
		return e
	}
	write(w, 200, map[string]any{"countries": out})
	return nil
}
