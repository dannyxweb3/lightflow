package control_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"nimbus/internal/security"
	"testing"
)

func TestConsoleSessionAndCSRF(t *testing.T) {
	f := newFixture(t, false)
	hash, e := security.Password("a-long-console-password")
	if e != nil {
		t.Fatal(e)
	}
	f.s.AdminPasswordHash = hash
	f.s.AdminCookieSecure = false
	request := func(method, path string, body any, cookie *http.Cookie, csrf, origin string) (int, map[string]any, *http.Cookie) {
		t.Helper()
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		r, _ := http.NewRequest(method, f.api.URL+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		res, e := f.http.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		var session *http.Cookie
		for _, c := range res.Cookies() {
			if c.Name == "nimbus_console" {
				session = c
			}
		}
		return res.StatusCode, out, session
	}
	if code, _, _ := request("GET", "/console/api/overview", nil, nil, "", ""); code != 401 {
		t.Fatalf("unauthorized overview: %d", code)
	}
	if code, _, _ := request("POST", "/console/api/login", map[string]any{"password": "a-long-console-password"}, nil, "", ""); code != 403 {
		t.Fatalf("login origin: %d", code)
	}
	if code, _, _ := request("POST", "/console/api/login", map[string]any{"password": "wrong"}, nil, "", f.api.URL); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	code, data, cookie := request("POST", "/console/api/login", map[string]any{"password": "a-long-console-password"}, nil, "", f.api.URL)
	if code != 200 || cookie == nil {
		t.Fatalf("login: %d %v", code, data)
	}
	if cookie.HttpOnly != true || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie flags: %+v", cookie)
	}
	csrf := data["csrf_token"].(string)
	if code, _, _ := request("GET", "/console/api/overview", nil, cookie, "", ""); code != 200 {
		t.Fatalf("overview: %d", code)
	}
	if code, _, _ := request("POST", "/console/api/countries", map[string]any{"code": "JP", "name": "Japan"}, cookie, "", f.api.URL); code != 403 {
		t.Fatalf("missing csrf: %d", code)
	}
	if code, _, _ := request("POST", "/console/api/countries", map[string]any{"code": "JP", "name": "Japan"}, cookie, csrf, "https://evil.example"); code != 403 {
		t.Fatalf("cross origin: %d", code)
	}
	if code, _, _ := request("POST", "/console/api/countries", map[string]any{"code": "JP", "name": "Japan"}, cookie, csrf, f.api.URL); code != 201 {
		t.Fatalf("authorized mutation: %d", code)
	}
	if code, _, _ := request("POST", "/console/api/logout", nil, cookie, csrf, f.api.URL); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _, _ := request("GET", "/console/api/overview", nil, cookie, "", ""); code != 401 {
		t.Fatalf("revoked session: %d", code)
	}
}
