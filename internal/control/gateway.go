package control

import (
	"context"
	"net/http"
	"time"
)

type Grant struct {
	ID      string    `json:"id"`
	Hash    string    `json:"credential_hash"`
	Expires time.Time `json:"expires_at"`
}
type Snapshot struct {
	Grants     []Grant   `json:"grants"`
	ServerTime time.Time `json:"server_time"`
}
type Ack struct {
	Grants []Grant `json:"grants"`
	Ready  bool    `json:"ready"`
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) error {
	if e := s.gateway(r); e != nil {
		return e
	}
	out := Snapshot{Grants: []Grant{}, ServerTime: time.Now().UTC()}
	rows, e := s.DB.Query(r.Context(), `SELECT s.id,s.credential_hash,LEAST(s.expires_at,sub.expires_at) FROM sessions s JOIN endpoints e ON e.id=s.endpoint_id JOIN countries c ON c.code=e.country_code JOIN devices d ON d.id=s.device_id JOIN users u ON u.id=s.user_id JOIN subscriptions sub ON sub.user_id=u.id WHERE s.endpoint_id=$1 AND e.enabled AND c.enabled AND u.enabled AND sub.enabled AND d.revoked_at IS NULL AND s.state IN ('pending','issued','active') AND s.expires_at>now() AND sub.expires_at>now()`, r.PathValue("id"))
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var g Grant
		if e = rows.Scan(&g.ID, &g.Hash, &g.Expires); e != nil {
			return e
		}
		out.Grants = append(out.Grants, g)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	write(w, 200, out)
	return nil
}
func (s *Server) ack(w http.ResponseWriter, r *http.Request) error {
	if e := s.gateway(r); e != nil {
		return e
	}
	var in Ack
	// Large gateways may ACK many leases, unlike public 64 KiB requests.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if e := decodeLarge(r, &in); e != nil {
		return e
	}
	ctx := r.Context()
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `UPDATE endpoints SET last_seen_at=now(),ready=$2 WHERE id=$1`, r.PathValue("id"), in.Ready); e != nil {
		return e
	}
	if in.Ready {
		for _, g := range in.Grants {
			_, e = tx.Exec(ctx, `UPDATE sessions SET confirmed_until=GREATEST(confirmed_until,$3),state=CASE WHEN state='pending' THEN 'issued' ELSE state END WHERE id=$1 AND endpoint_id=$2 AND state IN ('pending','issued','active') AND expires_at>now() AND $3<=expires_at`, g.ID, r.PathValue("id"), g.Expires)
			if e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	write(w, 204, nil)
	return nil
}
func (s *Server) Maintain(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Single SQL statements are idempotent and safe across API replicas.
			_, _ = s.DB.Exec(ctx, `UPDATE sessions SET state='expired' WHERE state IN ('pending','issued','active') AND (expires_at<=now() OR (state='pending' AND created_at<now()-interval '30 seconds'))`)
			_, _ = s.DB.Exec(ctx, `DELETE FROM proof_nonces WHERE expires_at<now()`)
			_, _ = s.DB.Exec(ctx, `DELETE FROM tokens WHERE expires_at<now()-interval '1 day'`)
			_, _ = s.DB.Exec(ctx, `DELETE FROM login_limits WHERE resets_at<now()-interval '1 hour'`)
			_, _ = s.DB.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at<now()`)
			_, _ = s.DB.Exec(ctx, `DELETE FROM sessions WHERE state IN ('released','revoked','expired') AND expires_at<now()-interval '7 days'`)
			_, _ = s.DB.Exec(ctx, `DELETE FROM audit_events WHERE created_at<now()-interval '90 days'`)
		}
	}
}
