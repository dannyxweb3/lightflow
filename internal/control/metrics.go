package control

import (
	"fmt"
	"net/http"
)

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) error {
	var live, pending, ready int64
	if e := s.DB.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE state IN ('issued','active') AND expires_at>now()),count(*) FILTER(WHERE state='pending' AND expires_at>now()) FROM sessions`).Scan(&live, &pending); e != nil {
		return e
	}
	if e := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM endpoints WHERE enabled AND ready AND last_seen_at>now()-interval '10 seconds'`).Scan(&ready); e != nil {
		return e
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, e := fmt.Fprintf(w, "# HELP nimbus_live_leases Unexpired issued and active leases.\n# TYPE nimbus_live_leases gauge\nnimbus_live_leases %d\n# HELP nimbus_pending_leases Leases awaiting gateway acknowledgement.\n# TYPE nimbus_pending_leases gauge\nnimbus_pending_leases %d\n# HELP nimbus_ready_gateways Gateways with a recent healthy ACK.\n# TYPE nimbus_ready_gateways gauge\nnimbus_ready_gateways %d\n", live, pending, ready)
	return e
}
