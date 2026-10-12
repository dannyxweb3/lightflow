package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"nimbus/internal/controlclient"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopSchedulesRenewalsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var renewals atomic.Int32
	plan := func() controlclient.Plan {
		return controlclient.Plan{SchemaVersion: 1, LeaseID: "lease", DeviceID: "device", ExpiresAt: time.Now().Add(time.Minute), RenewAfterSeconds: 1, Candidates: []controlclient.Candidate{{Credential: "fixed-test-credential"}}}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/login":
			json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
		case "/v1/devices":
			json.NewEncoder(w).Encode(map[string]string{"id": "device"})
		case "/v1/connection-sessions/lease/renew":
			if renewals.Add(1) == 2 {
				time.AfterFunc(50*time.Millisecond, cancel)
			}
			json.NewEncoder(w).Encode(plan())
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := controlclient.New(server.URL, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Login(ctx, "test", "test"); err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if err := client.RegisterDevice(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := holdDesktop(ctx, client, "lease", plan(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if renewals.Load() != 2 {
		t.Fatal("scheduled renewals missing or cancellation ignored")
	}
}
