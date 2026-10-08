package control_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"nimbus/internal/control"
	"nimbus/internal/security"
	"nimbus/internal/store"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	t             *testing.T
	s             *control.Server
	api, internal *httptest.Server
	http          *http.Client
	gatewayToken  string
	certDir       string
	cancel        context.CancelFunc
}
type account struct {
	id, token, refresh, device string
	key                        ed25519.PrivateKey
}

func testPKI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	private, _ := rsa.GenerateKey(rand.Reader, 2048)
	pub := &private.PublicKey
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, private)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	for i, name := range []string{"gateway"} {
		k, _ := rsa.GenerateKey(rand.Reader, 2048)
		p := &k.PublicKey
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: name}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}}
		d, e := x509.CreateCertificate(rand.Reader, c, ca, p, private)
		if e != nil {
			t.Fatal(e)
		}
		key, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(dir, name+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), 0600)
		os.WriteFile(filepath.Join(dir, name+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)
	}
	return dir
}
func newFixture(t *testing.T, mock bool) *fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	base, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + strings.ToLower(strings.ReplaceAll(security.ID(), "-", "_"))
	schema = strings.ReplaceAll(schema, "_", "a")
	if _, e = base.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); base.Close() })
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := store.Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Close)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	dummy, _ := security.Password("dummy-password-value")
	s := &control.Server{DB: db, AdminKey: "test-admin-key-long-enough-for-testing", Master: bytes.Repeat([]byte{3}, 32), Signing: key, LeaseTTL: 30 * time.Second, AckTimeout: time.Second, DummyPassword: dummy}
	f := &fixture{t: t, s: s, api: httptest.NewServer(s.Public()), certDir: testPKI(t), cancel: cancel}
	t.Cleanup(f.api.Close)
	f.internal = httptest.NewServer(s.Internal())
	t.Cleanup(f.internal.Close)
	f.http = &http.Client{Timeout: 5 * time.Second}
	f.gatewayToken = "test-gateway-secret-value-that-is-long-enough"

	f.must("POST", "/admin/countries", map[string]any{"code": "SG", "name": "Singapore"}, nil, 201)
	f.must("POST", "/admin/endpoints", map[string]any{"id": "gateway-1", "country_code": "SG", "host": "localhost", "port": 4433, "server_name": "localhost", "capacity": 100, "auth_token": f.gatewayToken}, nil, 201)
	if mock {
		f.sync()
		go func() {
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					f.sync()
				}
			}
		}()
	}
	go s.Maintain(ctx)
	return f
}
func (f *fixture) sync() {
	var snap control.Snapshot
	if f.internalReq("GET", "/internal/gateways/gateway-1/snapshot", nil, &snap) == 200 {
		f.internalReq("POST", "/internal/gateways/gateway-1/ack", control.Ack{Ready: true, Grants: snap.Grants}, nil)
	}
}
func (f *fixture) internalReq(method, path string, in, out any) int {
	var b []byte
	if in != nil {
		b, _ = json.Marshal(in)
	}
	r, _ := http.NewRequest(method, f.internal.URL+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+f.gatewayToken)
	res, e := f.http.Do(r)
	if e != nil {
		f.t.Logf("internal request failed: %v", e)
		return 0
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}
func (f *fixture) request(method, path string, in any, a *account, nonce, key string) (int, map[string]any) {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	r, _ := http.NewRequest(method, f.api.URL+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(path, "/admin/") {
		r.Header.Set("Authorization", "Bearer "+f.s.AdminKey)
	} else if a != nil {
		r.Header.Set("Authorization", "Bearer "+a.token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if a != nil && a.device != "" && strings.Contains(path, "connection-sessions") {
		if nonce == "" {
			nonce = security.Random(18)
		}
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		r.Header.Set("X-Device-ID", a.device)
		r.Header.Set("X-Device-Timestamp", stamp)
		r.Header.Set("X-Device-Nonce", nonce)
		r.Header.Set("X-Device-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(a.key, security.ProofMessage(method, path, stamp, nonce, body))))
	}
	res, e := http.DefaultClient.Do(r)
	if e != nil {
		f.t.Error(e)
		return 0, nil
	}
	defer res.Body.Close()
	v := map[string]any{}
	json.NewDecoder(res.Body).Decode(&v)
	return res.StatusCode, v
}
func (f *fixture) must(method, path string, in any, a *account, status int) map[string]any {
	f.t.Helper()
	got, v := f.request(method, path, in, a, "", security.Random(18))
	if got != status {
		f.t.Fatalf("%s %s: got %d want %d response=%v", method, path, got, status, v)
	}
	return v
}
func (f *fixture) account(deviceLimit, concurrent int) *account {
	email := security.ID() + "@example.test"
	user := f.must("POST", "/admin/users", map[string]any{"email": email, "password": "test-password-long", "subscription": map[string]any{"plan": "test", "expires_at": time.Now().Add(time.Hour), "device_limit": deviceLimit, "concurrent_limit": concurrent, "enabled": true}}, nil, 201)
	v := f.must("POST", "/v1/auth/login", map[string]any{"email": email, "password": "test-password-long"}, nil, 200)
	a := &account{id: user["id"].(string), token: v["access_token"].(string), refresh: v["refresh_token"].(string)}
	f.addDevice(a, 201)
	return a
}
func (f *fixture) addDevice(a *account, status int) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	v := f.must("POST", "/v1/devices", map[string]any{"name": "test", "os": "linux", "public_key": base64.StdEncoding.EncodeToString(pub)}, a, status)
	if status == 201 {
		a.device = v["id"].(string)
		a.key = key
	}
}
func planInput() map[string]any {
	return map[string]any{"mode": "smart", "country_code": "SG", "protocols": []string{"hysteria2"}}
}
func TestAccountSessionLifecycle(t *testing.T) {
	f := newFixture(t, true)
	a := f.account(1, 1)
	f.addDevice(a, 409)
	f.must("GET", "/v1/me", nil, a, 200)
	key := security.Random(18)
	status, p := f.request("POST", "/v1/connection-sessions", planInput(), a, "", key)
	if status != 200 {
		t.Fatalf("plan: %d %v", status, p)
	}
	id := p["lease_id"].(string)
	status, replay := f.request("POST", "/v1/connection-sessions", planInput(), a, "", key)
	if status != 200 || replay["lease_id"] != id {
		t.Fatal("idempotency failed")
	}
	altered := planInput()
	altered["mode"] = "global"
	status, _ = f.request("POST", "/v1/connection-sessions", altered, a, "", key)
	if status != 409 {
		t.Fatal("idempotency conflict accepted")
	}
	f.must("POST", "/v1/connection-sessions/"+id+"/activate", nil, a, 200)
	f.must("POST", "/v1/connection-sessions/"+id+"/renew", nil, a, 200)
	other := f.account(1, 1)
	f.must("DELETE", "/v1/connection-sessions/"+id, nil, other, 404)
	f.must("DELETE", "/v1/devices/"+a.device, nil, a, 204)
	var state string
	f.s.DB.QueryRow(context.Background(), `SELECT state FROM sessions WHERE id=$1`, id).Scan(&state)
	if state != "revoked" {
		t.Fatal("device deletion did not revoke")
	}
	f.must("POST", "/v1/connection-sessions/"+id+"/renew", nil, a, 403)
}
func TestAtomicQuotaAndProofReplay(t *testing.T) {
	f := newFixture(t, true)
	a := f.account(10, 1)
	devices := []account{*a}
	for i := 0; i < 5; i++ {
		b := *a
		f.addDevice(&b, 201)
		devices = append(devices, b)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, limited := 0, 0
	for i := range devices {
		wg.Add(1)
		go func(a account) {
			defer wg.Done()
			status, _ := f.request("POST", "/v1/connection-sessions", planInput(), &a, "", security.Random(18))
			mu.Lock()
			defer mu.Unlock()
			if status == 200 {
				success++
			} else if status == 409 {
				limited++
			} else {
				t.Errorf("unexpected quota status %d", status)
			}
		}(devices[i])
	}
	wg.Wait()
	if success != 1 || limited != 5 {
		t.Fatalf("quota success=%d limited=%d", success, limited)
	}
	b := f.account(1, 1)
	nonce := security.Random(18)
	key := security.Random(18)
	status, _ := f.request("POST", "/v1/connection-sessions", planInput(), b, nonce, key)
	if status != 200 {
		t.Fatal(status)
	}
	status, v := f.request("POST", "/v1/connection-sessions", planInput(), b, nonce, key)
	if status != 409 || v["code"] != "PROOF_REPLAY" {
		t.Fatal("proof replay accepted")
	}
}
func TestRefreshReplayAndSubscriptionRevocation(t *testing.T) {
	f := newFixture(t, true)
	a := f.account(2, 1)
	p := f.must("POST", "/v1/connection-sessions", planInput(), a, 200)
	var originalDeadline time.Time
	if e := f.s.DB.QueryRow(context.Background(), `SELECT expires_at FROM tokens WHERE hash=$1`, security.Hash(a.refresh)).Scan(&originalDeadline); e != nil {
		t.Fatal(e)
	}
	v := f.must("POST", "/v1/auth/refresh", map[string]any{"refresh_token": a.refresh}, nil, 200)
	var rotatedDeadline time.Time
	if e := f.s.DB.QueryRow(context.Background(), `SELECT expires_at FROM tokens WHERE hash=$1`, security.Hash(v["refresh_token"].(string))).Scan(&rotatedDeadline); e != nil {
		t.Fatal(e)
	}
	if !originalDeadline.Equal(rotatedDeadline) {
		t.Fatal("refresh rotation extended original login lifetime")
	}
	f.must("POST", "/v1/auth/refresh", map[string]any{"refresh_token": a.refresh}, nil, 401)
	b := *a
	b.token = v["access_token"].(string)
	f.must("GET", "/v1/me", nil, &b, 401)
	f.must("PUT", "/admin/users/"+a.id+"/subscription", map[string]any{"plan": "expired", "expires_at": time.Now().Add(-time.Minute), "device_limit": 1, "concurrent_limit": 1, "enabled": false}, nil, 200)
	var state string
	f.s.DB.QueryRow(context.Background(), `SELECT state FROM sessions WHERE id=$1`, p["lease_id"]).Scan(&state)
	if state != "revoked" {
		t.Fatal("subscription update left lease active")
	}
}
func TestGatewayIdentityAndSignedDocument(t *testing.T) {
	f := newFixture(t, true)
	if status := f.internalReq("GET", "/internal/gateways/other/snapshot", nil, nil); status != 401 {
		t.Fatalf("cross-gateway access: %d", status)
	}
	a := f.account(1, 1)
	f.must("POST", "/admin/documents/policy", map[string]any{"version": 1, "payload": map[string]any{"schema_version": 1, "protocol_order": []string{"hysteria2"}}}, nil, 201)
	f.must("POST", "/admin/documents/policy", map[string]any{"version": 1, "payload": map[string]any{}}, nil, 409)
	v := f.must("GET", "/v1/policies/current", nil, a, 200)
	payload, _ := base64.StdEncoding.DecodeString(v["payload"].(string))
	sig, _ := base64.StdEncoding.DecodeString(v["signature"].(string))
	if !ed25519.Verify(f.s.Signing.Public().(ed25519.PublicKey), payload, sig) {
		t.Fatal("invalid document signature")
	}
}
func TestNoACKNoCredentialAndCountryIsolation(t *testing.T) {
	f := newFixture(t, false)
	a := f.account(1, 1)
	f.must("POST", "/v1/connection-sessions", planInput(), a, 503)
	f.internalReq("POST", "/internal/gateways/gateway-1/ack", control.Ack{Ready: true, Grants: []control.Grant{}}, nil)
	in := planInput()
	in["country_code"] = "JP"
	f.must("POST", "/v1/connection-sessions", in, a, 503)
	key := security.Random(18)
	status, v := f.request("POST", "/v1/connection-sessions", planInput(), a, "", key)
	if status != 202 || v["candidates"] != nil {
		t.Fatalf("unacknowledged credential returned: %d", status)
	}
	f.sync()
	status, v = f.request("POST", "/v1/connection-sessions", planInput(), a, "", key)
	if status != 200 {
		t.Fatalf("ACK retry failed %d %v", status, v)
	}
}
func TestLogoutRevokesSessions(t *testing.T) {
	f := newFixture(t, true)
	a := f.account(1, 1)
	p := f.must("POST", "/v1/connection-sessions", planInput(), a, 200)
	f.must("POST", "/v1/auth/logout", nil, a, 204)
	f.must("GET", "/v1/me", nil, a, 401)
	var state string
	f.s.DB.QueryRow(context.Background(), `SELECT state FROM sessions WHERE id=$1`, p["lease_id"]).Scan(&state)
	if state != "revoked" {
		t.Fatal(fmt.Sprint("logout: ", state))
	}
}

func TestCredentialKeyBinding(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	if e := store.BindCredentialKey(ctx, f.s.DB, "fingerprint-a"); e != nil {
		t.Fatal(e)
	}
	if e := store.BindCredentialKey(ctx, f.s.DB, "fingerprint-a"); e != nil {
		t.Fatal(e)
	}
	if e := store.BindCredentialKey(ctx, f.s.DB, "fingerprint-b"); e == nil {
		t.Fatal("different credential key accepted")
	}
}

func TestGatewayTokenRotation(t *testing.T) {
	f := newFixture(t, false)
	if status := f.internalReq("GET", "/internal/gateways/gateway-1/snapshot", nil, nil); status != 200 {
		t.Fatalf("initial gateway authentication: %d", status)
	}
	f.must("POST", "/internal/gateways/gateway-1/snapshot", nil, nil, 404) // The public listener has no internal route.
	newToken := "second-gateway-secret-that-is-long-enough"
	f.must("PATCH", "/admin/endpoints/gateway-1", map[string]any{"auth_token": newToken}, nil, 204)
	if status := f.internalReq("GET", "/internal/gateways/gateway-1/snapshot", nil, nil); status != 401 {
		t.Fatalf("old gateway token accepted: %d", status)
	}
	req, _ := http.NewRequest("GET", f.internal.URL+"/internal/gateways/gateway-1/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+newToken)
	res, e := f.http.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("rotated token rejected: %d", res.StatusCode)
	}
	var ready bool
	if e = f.s.DB.QueryRow(context.Background(), `SELECT ready FROM endpoints WHERE id='gateway-1'`).Scan(&ready); e != nil {
		t.Fatal(e)
	}
	if ready {
		t.Fatal("rotated gateway remained ready")
	}
}

func TestEndpointAddressUpdateChangesCandidate(t *testing.T) {
	f := newFixture(t, true)
	a := f.account(2, 2)
	status, first := f.request("POST", "/v1/connection-sessions", planInput(), a, "", security.Random(18))
	if status != 200 {
		t.Fatalf("initial plan: %d %v", status, first)
	}
	f.must("PATCH", "/admin/endpoints/gateway-1", map[string]any{"host": "bad host"}, nil, 400)
	f.must("PATCH", "/admin/endpoints/gateway-1", map[string]any{"host": "192.168.194.128", "server_name": "vpn.lan", "port": 5443}, nil, 204)
	var state string
	if e := f.s.DB.QueryRow(context.Background(), `SELECT state FROM sessions WHERE id=$1`, first["lease_id"]).Scan(&state); e != nil || state != "revoked" {
		t.Fatalf("old lease not revoked after endpoint address update: %q %v", state, e)
	}
	f.sync()
	status, second := f.request("POST", "/v1/connection-sessions", planInput(), a, "", security.Random(18))
	if status != 200 {
		t.Fatalf("updated plan: %d %v", status, second)
	}
	candidate := second["candidates"].([]any)[0].(map[string]any)
	if candidate["host"] != "192.168.194.128" || candidate["port"] != float64(5443) || candidate["public_params"].(map[string]any)["server_name"] != "vpn.lan" {
		t.Fatalf("candidate did not use updated endpoint: %v", candidate)
	}
}
