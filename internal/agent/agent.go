// Package agent supervises a Hysteria child and fails closed if enforcement fails.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"nimbus/internal/control"
	"nimbus/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ControlURL, ID, Token, Hysteria, TLSCert, TLSKey, Listen, AuthListen, StatsListen string
	AllowPrivate                                                                      bool
}
type grant struct {
	control.Grant
	Deadline time.Time
}
type Agent struct {
	mu               sync.Mutex
	grants           map[string]grant
	reservations     map[string]time.Time
	ready            bool
	statsURL, secret string
	stats            *http.Client
}

func New(statsURL, secret string) *Agent {
	return &Agent{grants: map[string]grant{}, reservations: map[string]time.Time{}, statsURL: statsURL, secret: secret, stats: &http.Client{Timeout: time.Second}}
}
func (a *Agent) statsRequest(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, a.statsURL+path, body)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", a.secret)
	r.Header.Set("Content-Type", "application/json")
	res, e := a.stats.Do(r)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 204 {
		return fmt.Errorf("stats status %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
	}
	return nil
}
func (a *Agent) online(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	e := a.statsRequest(ctx, "GET", "/online", nil, &out)
	return out, e
}
func (a *Agent) Auth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	deny := func() { _, _ = w.Write([]byte(`{"ok":false}`)) }
	var in struct {
		Addr string `json:"addr"`
		Auth string `json:"auth"`
		TX   uint64 `json:"tx"`
	}
	if r.Method != "POST" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || len(in.Auth) > 128 {
		deny()
		return
	}
	// Serial authentication and a short reservation close the gap before /online
	// reports a newly accepted QUIC connection. No remote address is persisted.
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.grants[security.Hash(in.Auth)]
	if !a.ready || !ok || !time.Now().Before(g.Deadline) || time.Now().Before(a.reservations[g.ID]) {
		deny()
		return
	}
	online, e := a.online(r.Context())
	if e != nil || online[g.ID] > 0 || !time.Now().Before(g.Deadline) {
		deny()
		return
	}
	a.reservations[g.ID] = time.Now().Add(3 * time.Second)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": g.ID})
}
func (a *Agent) Apply(s control.Snapshot, elapsed time.Duration) error {
	// Reject unreasonable clock differences, but enforce local lifetime using Go's
	// monotonic clock so a wall-clock rollback cannot extend an existing lease.
	if d := time.Since(s.ServerTime); d > 5*time.Second || d < -5*time.Second {
		return errors.New("control clock skew")
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := map[string]grant{}
	for _, g := range s.Grants {
		d := g.Expires.Sub(s.ServerTime) - elapsed - time.Second
		if d > 0 {
			deadline := now.Add(d)
			if old, ok := a.grants[g.Hash]; ok {
				conservative := old.Deadline.Add(g.Expires.Sub(old.Expires))
				if conservative.Before(deadline) {
					deadline = conservative
				}
			}
			next[g.Hash] = grant{g, deadline}
		}
	}
	a.grants = next
	for id, until := range a.reservations {
		if until.Before(now) {
			delete(a.reservations, id)
		}
	}
	return nil
}
func (a *Agent) Enforce(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	online, e := a.online(ctx)
	if e != nil {
		a.ready = false
		return e
	}
	allowed := map[string]bool{}
	for hash, g := range a.grants {
		if time.Now().Before(g.Deadline) {
			allowed[g.ID] = true
		} else {
			delete(a.grants, hash)
		}
	}
	kick := []string{}
	for id, count := range online {
		if !allowed[id] || count > 1 {
			kick = append(kick, id)
		}
	}
	if len(kick) > 0 {
		if e = a.statsRequest(ctx, "POST", "/kick", kick, nil); e != nil {
			a.ready = false
			return e
		}
	}
	a.ready = true
	return nil
}
func (a *Agent) ACK() control.Ack {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := control.Ack{Ready: a.ready, Grants: []control.Grant{}}
	for _, g := range a.grants {
		if time.Now().Before(g.Deadline) {
			out.Grants = append(out.Grants, g.Grant)
		}
	}
	return out
}
func controlClient() *http.Client {
	return &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var errControlUnauthorized = errors.New("gateway control authentication rejected")

func controlRequest(ctx context.Context, client *http.Client, url, token, method string, in, out any) error {
	var b io.Reader
	if in != nil {
		data, e := json.Marshal(in)
		if e != nil {
			return e
		}
		b = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, url, b)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return errControlUnauthorized
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("control status %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
	}
	return nil
}
func Run(ctx context.Context, c Config) error {
	if (!strings.HasPrefix(c.ControlURL, "http://") && !strings.HasPrefix(c.ControlURL, "https://")) || len(c.Token) < 32 {
		return errors.New("control URL or gateway token invalid")
	}
	for _, addr := range []string{c.AuthListen, c.StatsListen} {
		host, _, e := net.SplitHostPort(addr)
		if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return errors.New("auth and stats must bind to loopback")
		}
	}
	client := controlClient()
	secret := security.Random(32)
	a := New("http://"+c.StatsListen, secret)
	auth := &http.Server{Addr: c.AuthListen, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			a.Auth(w, r)
			return
		}
		if r.URL.Path == "/healthz" {
			if !a.ACK().Ready {
				w.WriteHeader(503)
			}
			return
		}
		w.WriteHeader(404)
	}), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 15 * time.Second}
	listener, e := net.Listen("tcp", c.AuthListen)
	if e != nil {
		return e
	}
	defer auth.Close()
	go func() { _ = auth.Serve(listener) }()
	dir, e := os.MkdirTemp("", "nimbus-gateway-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	cfg := map[string]any{"listen": c.Listen, "tls": map[string]string{"cert": c.TLSCert, "key": c.TLSKey}, "auth": map[string]any{"type": "http", "http": map[string]string{"url": "http://" + c.AuthListen + "/auth"}}, "trafficStats": map[string]string{"listen": c.StatsListen, "secret": secret}, "disableUDP": false}
	if !c.AllowPrivate {
		cfg["acl"] = map[string]any{"inline": []string{"reject(0.0.0.0/8)", "reject(10.0.0.0/8)", "reject(100.64.0.0/10)", "reject(127.0.0.0/8)", "reject(169.254.0.0/16)", "reject(172.16.0.0/12)", "reject(192.168.0.0/16)", "reject(224.0.0.0/4)", "reject(240.0.0.0/4)", "reject(::/128)", "reject(::1/128)", "reject(fc00::/7)", "reject(fe80::/10)", "reject(ff00::/8)"}}
	}
	data, e := json.Marshal(cfg)
	if e != nil {
		return e
	}
	path := filepath.Join(dir, "hysteria.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		return e
	}
	child := exec.CommandContext(ctx, c.Hysteria, "server", "--config", path, "--log-level", "error")
	child.Stdout = io.Discard
	child.Stderr = io.Discard // upstream logs may include destinations
	if e = child.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	exited := false
	defer func() {
		if !exited {
			_ = child.Process.Kill()
			<-done
		}
	}()
	syncCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	base := strings.TrimRight(c.ControlURL, "/") + "/internal/gateways/" + c.ID
	fatal := make(chan error, 1)
	syncState := func() {
		start := time.Now()
		var snap control.Snapshot
		if e := controlRequest(syncCtx, client, base+"/snapshot", c.Token, "GET", nil, &snap); e != nil {
			if errors.Is(e, errControlUnauthorized) {
				select {
				case fatal <- e:
				default:
				}
				return
			}
			slog.Warn("gateway sync unavailable")
			return
		}
		if e := a.Apply(snap, time.Since(start)); e != nil {
			slog.Warn("gateway snapshot rejected")
			return
		}
		if e := a.Enforce(syncCtx); e != nil {
			return
		}
		if e := controlRequest(syncCtx, client, base+"/ack", c.Token, "POST", a.ACK(), nil); e != nil {
			if errors.Is(e, errControlUnauthorized) {
				select {
				case fatal <- e:
				default:
				}
				return
			}
			slog.Warn("gateway ack unavailable")
		}
	}
	// Synchronization must never block local expiry enforcement.
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		syncState()
		for {
			select {
			case <-syncCtx.Done():
				return
			case <-t.C:
				syncState()
			}
		}
	}()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-fatal:
			return e
		case e := <-done:
			exited = true
			if ctx.Err() != nil {
				return nil
			}
			if e == nil {
				return errors.New("gateway stopped unexpectedly")
			}
			return errors.New("gateway process failed; check configuration and certificates")
		case <-tick.C:
			if e = a.Enforce(ctx); e != nil {
				failures++
				if failures >= 3 {
					return errors.New("cannot enforce leases; stopping gateway")
				}
			} else {
				failures = 0
			}
		}
	}
}
