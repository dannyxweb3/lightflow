package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nimbus/internal/control"
	"nimbus/internal/security"
	"strings"
	"testing"
	"time"
)

func TestExpiryRevocationDuplicateAndRestart(t *testing.T) {
	online := map[string]int{"old-before-restart": 1}
	var kicked []string
	stats := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "secret" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/online" {
			json.NewEncoder(w).Encode(online)
			return
		}
		json.NewDecoder(r.Body).Decode(&kicked)
		for _, id := range kicked {
			delete(online, id)
		}
	}))
	defer stats.Close()
	a := New(stats.URL, "secret")
	if e := a.Enforce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(online) != 0 {
		t.Fatal("restart left unknown sessions online")
	}
	snap := control.Snapshot{ServerTime: time.Now(), Grants: []control.Grant{{ID: "lease", Hash: security.Hash("password"), Expires: time.Now().Add(time.Minute)}}}
	if e := a.Apply(snap, 0); e != nil {
		t.Fatal(e)
	}
	auth := func() bool {
		w := httptest.NewRecorder()
		a.Auth(w, httptest.NewRequest("POST", "/auth", strings.NewReader(`{"addr":"127.0.0.1:5","auth":"password","tx":0}`)))
		var v struct{ OK bool }
		json.Unmarshal(w.Body.Bytes(), &v)
		return v.OK
	}
	if !auth() {
		t.Fatal("valid credential rejected")
	}
	if auth() {
		t.Fatal("concurrent handshake reservation bypass")
	}
	online["lease"] = 2
	if e := a.Enforce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(online) != 0 {
		t.Fatal("duplicate connections not kicked")
	}
	online["lease"] = 1
	snap.Grants = nil
	snap.ServerTime = time.Now()
	a.Apply(snap, 0)
	a.Enforce(context.Background())
	if len(online) != 0 {
		t.Fatal("revocation not enforced")
	}
	a.mu.Lock()
	a.grants[security.Hash("password")] = grant{control.Grant{ID: "lease"}, time.Now().Add(-time.Second)}
	a.mu.Unlock()
	online["lease"] = 1
	a.Enforce(context.Background())
	if len(online) != 0 || auth() {
		t.Fatal("expiry not enforced")
	}
}
func TestStatsFailureClosesAuth(t *testing.T) {
	stats := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer stats.Close()
	a := New(stats.URL, "secret")
	if a.Enforce(context.Background()) == nil {
		t.Fatal("stats failure ignored")
	}
	if a.ACK().Ready {
		t.Fatal("unhealthy gateway marked ready")
	}
}
