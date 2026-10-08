package control

import (
	"embed"
	"errors"
	"net"
	"net/http"
	"net/url"
	"nimbus/internal/security"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed console.html console.js console.css
var consoleFiles embed.FS

const consoleCookie = "nimbus_console"

func (s *Server) mountConsole(m *http.ServeMux) {
	m.HandleFunc("GET /console", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/console/", http.StatusSeeOther) })
	m.HandleFunc("GET /console/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := consoleFiles.ReadFile("console.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Write(b)
	})
	m.HandleFunc("GET /console/console.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := consoleFiles.ReadFile("console.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	m.HandleFunc("GET /console/console.css", func(w http.ResponseWriter, r *http.Request) {
		b, _ := consoleFiles.ReadFile("console.css")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	m.HandleFunc("POST /console/api/login", wrap(s.consoleLogin))
	m.HandleFunc("GET /console/api/session", wrap(s.consoleSession))
	m.HandleFunc("POST /console/api/logout", wrap(s.consoleGuard(s.consoleLogout)))
	for p, h := range map[string]handler{
		"GET /console/api/overview":                s.overview,
		"GET /console/api/users":                   s.listUsers,
		"GET /console/api/users/{id}":              s.userDetail,
		"POST /console/api/users":                  s.addUser,
		"PUT /console/api/users/{id}/subscription": s.subscription,
		"GET /console/api/countries":               s.listCountries,
		"POST /console/api/countries":              s.addCountry,
		"GET /console/api/endpoints":               s.listEndpoints,
		"POST /console/api/endpoints":              s.addEndpoint,
		"PATCH /console/api/endpoints/{id}":        s.setEndpoint,
	} {
		m.HandleFunc(p, wrap(s.consoleGuard(h)))
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, e := url.Parse(origin)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}

func (s *Server) consoleLogin(w http.ResponseWriter, r *http.Request) error {
	if !sameOrigin(r) {
		return fail(403, "INVALID_ORIGIN")
	}
	var in struct {
		Password string `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	w.Header().Set("Retry-After", "300")
	if e := s.rate(r.Context(), "console-login:"+ip, 10); e != nil {
		return e
	}
	w.Header().Del("Retry-After")
	hash := s.AdminPasswordHash
	if hash == "" {
		hash = s.DummyPassword
	}
	if len(in.Password) > 128 || !security.CheckPassword(hash, in.Password) || s.AdminPasswordHash == "" {
		return fail(401, "INVALID_CREDENTIALS")
	}
	token, csrf := security.Random(32), security.Random(32)
	_, e := s.DB.Exec(r.Context(), `INSERT INTO admin_sessions(token_hash,csrf_hash,expires_at) VALUES($1,$2,now()+interval '8 hours')`, security.Hash(token), security.Hash(csrf))
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: consoleCookie, Value: token, Path: "/console", HttpOnly: true, Secure: s.AdminCookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
	write(w, 200, map[string]any{"csrf_token": csrf, "expires_in_seconds": 8 * 3600})
	return nil
}

func (s *Server) consoleAuth(r *http.Request) (string, error) {
	c, e := r.Cookie(consoleCookie)
	if e != nil || len(c.Value) > 128 {
		return "", fail(401, "ADMIN_AUTH_REQUIRED")
	}
	var csrf string
	e = s.DB.QueryRow(r.Context(), `SELECT csrf_hash FROM admin_sessions WHERE token_hash=$1 AND expires_at>now()`, security.Hash(c.Value)).Scan(&csrf)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", fail(401, "ADMIN_AUTH_REQUIRED")
	}
	return csrf, e
}

func (s *Server) consoleSession(w http.ResponseWriter, r *http.Request) error {
	_, e := s.consoleAuth(r)
	if e != nil {
		return e
	}
	// The CSRF token is stored only as a hash; rotate it when the page loads.
	token := security.Random(32)
	c, _ := r.Cookie(consoleCookie)
	_, e = s.DB.Exec(r.Context(), `UPDATE admin_sessions SET csrf_hash=$2 WHERE token_hash=$1`, security.Hash(c.Value), security.Hash(token))
	if e != nil {
		return e
	}
	write(w, 200, map[string]any{"csrf_token": token})
	return nil
}

func (s *Server) consoleGuard(h handler) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		csrf, e := s.consoleAuth(r)
		if e != nil {
			return e
		}
		if r.Method != "GET" {
			if !sameOrigin(r) || len(r.Header.Get("X-CSRF-Token")) > 128 || !security.Equal(csrf, security.Hash(r.Header.Get("X-CSRF-Token"))) {
				return fail(403, "INVALID_CSRF")
			}
		}
		return h(w, r)
	}
}

func (s *Server) consoleLogout(w http.ResponseWriter, r *http.Request) error {
	c, _ := r.Cookie(consoleCookie)
	_, e := s.DB.Exec(r.Context(), `DELETE FROM admin_sessions WHERE token_hash=$1`, security.Hash(c.Value))
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: consoleCookie, Value: "", Path: "/console", HttpOnly: true, Secure: s.AdminCookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(0, 0)})
	write(w, 204, nil)
	return nil
}
