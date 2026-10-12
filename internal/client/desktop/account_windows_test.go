//go:build windows

package desktop

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"nimbus/internal/client/sessionstore"
	"nimbus/internal/controlclient"
)

func TestLocalLogoutArchivesIdentityAndBlocksUnreleasedLease(t *testing.T) {
	b := &Backend{Root: t.TempDir(), Config: Config{Control: "https://control.example.test"}}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	p := &controlclient.Profile{ID: "test-account", Email: "test@example.test", Subscription: controlclient.Subscription{Plan: "test", Enabled: true, ExpiresAt: time.Now().Add(time.Hour)}}
	s := controlclient.Session{Control: b.Config.Control, AccessToken: "test-access", RefreshToken: "test-refresh", DeviceID: "test-device", DeviceKey: key, Profile: p}
	if err := sessionstore.Save(b.sessionPath(), s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.sessionPath()+".lease", []byte("unresolved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.Logout(context.Background()); err == nil {
		t.Fatal("removed identity before lease released")
	}
	if !b.Account().Authenticated {
		t.Fatal("failed logout removed session")
	}
	os.Remove(b.sessionPath() + ".lease")
	if err := b.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.Account().Authenticated {
		t.Fatal("logout retained login")
	}
	identity, err := sessionstore.Load(b.identityPath(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if identity.AccessToken != "" || identity.RefreshToken != "" || !identity.DeviceKey.Equal(key) || identity.DeviceID != s.DeviceID {
		t.Fatal("identity archive lost key or retained token")
	}
	if _, err := os.Stat(b.sessionPath()); !os.IsNotExist(err) {
		t.Fatal("session file retained")
	}
}
