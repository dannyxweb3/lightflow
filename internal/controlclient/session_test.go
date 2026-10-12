package controlclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestRefreshConsumesBeforeSendingAndCommitsRotation(t *testing.T) {
	var saved []Session
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/refresh" {
			if len(saved) != 1 || saved[0].RefreshToken != "" {
				t.Error("old refresh token not invalidated before transmission")
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "next-access", "refresh_token": "next-refresh", "expires_at": time.Now().Add(15 * time.Minute)})
		} else {
			if r.Header.Get("Authorization") != "Bearer next-access" {
				t.Error("stale access token used")
			}
			json.NewEncoder(w).Encode(map[string]any{"countries": []any{}})
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	c := &Client{base: base, http: server.Client(), token: "old-access", refresh: "old-refresh", accessUntil: time.Now().Add(-time.Minute)}
	c.SaveSession = func(s Session) error { saved = append(saved, s); return nil }
	if _, err := c.Countries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[1].RefreshToken != "next-refresh" {
		t.Fatal("rotation not committed")
	}
}
func TestAmbiguousRefreshCannotReplay(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.WriteHeader(500) }))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	c := &Client{base: base, http: server.Client(), token: "old-access", refresh: "old-refresh", accessUntil: time.Now().Add(-time.Minute)}
	var saved Session
	c.SaveSession = func(s Session) error { saved = s; return nil }
	for range 2 {
		if _, err := c.Countries(context.Background()); err == nil {
			t.Fatal("ambiguous refresh accepted")
		}
	}
	if count != 1 || saved.RefreshToken != "" {
		t.Fatal("possibly consumed refresh token replayed")
	}
}
