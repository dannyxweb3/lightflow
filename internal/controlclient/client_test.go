package controlclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanonicalProofEmptyBodyAndEscapedPath(t *testing.T) {
	const expected = "DELETE\n/v1/connection-sessions/a%2Fb\n1791273600\n0123456789abcdef\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if actual := string(SigningBytes("delete", "/v1/connection-sessions/a%2Fb", "1791273600", "0123456789abcdef", nil)); actual != expected {
		t.Fatalf("unexpected proof bytes: %q", actual)
	}
	if string(SigningBytes("DELETE", "/v1/connection-sessions/a%2Fb", "1791273600", "0123456789abcdef", []byte("{}"))) == expected {
		t.Fatal("empty body conflated with JSON object")
	}
}

func TestCreateModeUsesRequestedModeAndRejectsInvalidBeforeRequest(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		var body struct {
			Mode string `json:"mode"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil || body.Mode != "smart" {
			t.Error("requested mode lost")
		}
		json.NewEncoder(w).Encode(Plan{SchemaVersion: 1, LeaseID: "lease", DeviceID: "device", ExpiresAt: time.Now().Add(time.Minute), Candidates: []Candidate{{Protocol: "hysteria2"}}})
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{base: base, http: server.Client(), deviceID: "device", key: key}
	if _, err := client.CreateMode(context.Background(), "0123456789abcdef", "smart"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateMode(context.Background(), "0123456789abcdef", "direct"); err == nil || calls != 1 {
		t.Fatal("invalid mode reached control plane")
	}
}

func TestPendingRetryPreservesBodyAndKeyButChangesNonce(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	var bodies [][]byte
	var nonces []string
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		bodies = append(bodies, body)
		nonces = append(nonces, req.Header.Get("X-Device-Nonce"))
		keys = append(keys, req.Header.Get("Idempotency-Key"))
		if req.Header.Get("Authorization") != "Bearer test-token" || req.Header.Get("X-Device-ID") != "device" {
			t.Error("missing account/device binding")
		}
		signature, _ := base64.StdEncoding.DecodeString(req.Header.Get("X-Device-Signature"))
		if !ed25519.Verify(public, SigningBytes(req.Method, req.URL.EscapedPath(), req.Header.Get("X-Device-Timestamp"), req.Header.Get("X-Device-Nonce"), body), signature) {
			t.Error("invalid proof")
		}
		if len(bodies) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(202)
			io.WriteString(w, `{"lease_id":"lease","state":"pending"}`)
			return
		}
		json.NewEncoder(w).Encode(Plan{SchemaVersion: 1, LeaseID: "lease", DeviceID: "device", ExpiresAt: time.Now().Add(time.Minute), Candidates: []Candidate{{Protocol: "hysteria2"}}})
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{base: base, http: server.Client(), token: "test-token", deviceID: "device", key: private}
	plan, err := client.Create(context.Background(), "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if plan.LeaseID != "lease" || len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) || keys[0] != keys[1] || nonces[0] == nonces[1] {
		t.Fatal("pending retry violated signing/idempotency contract")
	}
}

func TestUpstreamErrorsCannotLeakSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"code":"secret password/token","credential":"test-secret"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{base: base, http: server.Client()}
	err := client.Login(context.Background(), "user", "test-secret")
	if err == nil || err.Error() != "API_ERROR (HTTP 403)" {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestTLSUsesExplicitCAAndRejectsRedirects(t *testing.T) {
	redirectReceived := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectReceived = true }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	client, err := New(server.URL, caFile)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	err = client.Login(context.Background(), "user", "test-secret")
	if err == nil || redirectReceived {
		t.Fatal("redirect could disclose login credentials")
	}
	if _, err := New(strings.Replace(server.URL, "https", "http", 1), caFile); err == nil {
		t.Fatal("insecure origin accepted")
	}
}

func TestSystemTrustRejectsUntrustedServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted server received an authenticated request")
	}))
	defer server.Close()
	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	err = client.Login(context.Background(), "user", "test-secret")
	if err == nil || err.Error() != "CONTROL_CA_UNTRUSTED" {
		t.Fatalf("system trust did not reject private certificate: %v", err)
	}
}

func TestPendingLeaseIDSurvivesFailureForCleanup(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(202)
			io.WriteString(w, `{"lease_id":"pending-lease","state":"pending"}`)
			return
		}
		w.WriteHeader(503)
		io.WriteString(w, `{"code":"NO_CAPACITY"}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &Client{base: base, http: server.Client(), deviceID: "device", key: private}
	plan, err := client.Create(context.Background(), "0123456789abcdef")
	if err == nil || plan.LeaseID != "pending-lease" {
		t.Fatal("lost pending lease cleanup identity")
	}
}
