package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type accountTestBackend struct {
	heldBackend
	mu           sync.Mutex
	account      Account
	logins       int
	loginGate    chan struct{}
	logoutCalled chan struct{}
}

func (b *accountTestBackend) Account() Account { b.mu.Lock(); defer b.mu.Unlock(); return b.account }
func (b *accountTestBackend) Login(ctx context.Context, email, password string) error {
	if b.loginGate != nil {
		select {
		case <-b.loginGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logins++
	b.account = Account{Authenticated: true, Email: email, Enabled: true, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	return nil
}
func (b *accountTestBackend) RefreshAccount(context.Context) error { return nil }
func (b *accountTestBackend) Logout(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.account = Account{}
	close(b.logoutCalled)
	return nil
}
func waitAccount(t *testing.T, c *Controller) *Account {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		a := snapshot(c).Account
		if a != nil && !a.Busy {
			return a
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("account operation stuck")
	return nil
}
func TestLoginDoesNotCachePasswordAndBlocksConnect(t *testing.T) {
	b := &accountTestBackend{loginGate: make(chan struct{})}
	c := NewReal(b)
	defer c.Close()
	if r := c.Handle(request("unsigned", "connect", "{}")); r.Error == nil || r.Error.Code != "LOGIN_REQUIRED" {
		t.Fatal("unsigned connect allowed")
	}
	req := request("login", "login", `{"email":"a@example.test","password":"private-test-secret"}`)
	response := c.Handle(req)
	if response.Error != nil || !response.Result.Account.Busy {
		t.Fatal("login not asynchronous")
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), "private-test-secret") {
		t.Fatal("password in snapshot")
	}
	c.mu.Lock()
	for _, entry := range c.cache {
		if strings.Contains(entry.fingerprint, "private-test-secret") {
			t.Fatal("password in cache")
		}
	}
	c.mu.Unlock()
	if r := c.Handle(request("busy", "connect", "{}")); r.Error == nil || r.Error.Code != "ACCOUNT_BUSY" {
		t.Fatal("connect raced login")
	}
	c.Handle(req)
	if r := c.Handle(request("login", "login", `{"email":"a@example.test","password":"different"}`)); r.Error == nil || r.Error.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed request reused")
	}
	close(b.loginGate)
	if !waitAccount(t, c).Authenticated {
		t.Fatal("login lost")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logins != 1 {
		t.Fatal("duplicate cloud login")
	}
}
func TestLogoutWaitsForTunnelCleanup(t *testing.T) {
	b := &accountTestBackend{heldBackend: heldBackend{started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}, account: Account{Authenticated: true, Enabled: true, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, logoutCalled: make(chan struct{})}
	c := NewReal(b)
	defer c.Close()
	c.Handle(request("connect", "connect", "{}"))
	<-b.started
	c.Handle(request("logout", "logout", "{}"))
	if snapshot(c).State != "disconnecting" {
		t.Fatal("logout skipped disconnect")
	}
	select {
	case <-b.logoutCalled:
		t.Fatal("tokens removed before tunnel cleanup")
	default:
	}
	if r := c.Handle(request("reconnect", "connect", "{}")); r.Error == nil {
		t.Fatal("reconnect raced logout")
	}
	close(b.release)
	if waitAccount(t, c).Authenticated {
		t.Fatal("logout still authenticated")
	}
	if snapshot(c).State != "disconnected" {
		t.Fatal("logout did not stop tunnel")
	}
}
func TestExpiredSubscriptionCannotConnect(t *testing.T) {
	b := &accountTestBackend{account: Account{Authenticated: true, Enabled: true, ExpiresAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}}
	c := NewReal(b)
	defer c.Close()
	if r := c.Handle(request("expired", "connect", "{}")); r.Error == nil || r.Error.Code != "SUBSCRIPTION_INACTIVE" {
		t.Fatal("expired subscription connected")
	}
}
