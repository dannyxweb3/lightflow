package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"nimbus/internal/agent"
	"nimbus/internal/control"
	"nimbus/internal/security"
	"nimbus/internal/store"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func required(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic("missing configuration: " + k)
	}
	return v
}
func httpServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
}
func main() {
	if e := run(); e != nil {
		slog.Error("service stopped", "error", e.Error())
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: nimbus control | agent | health URL")
	}
	if os.Args[1] == "health" {
		if len(os.Args) != 3 {
			return errors.New("health URL required")
		}
		c := http.Client{Timeout: 2 * time.Second}
		r, e := c.Get(os.Args[2])
		if e != nil {
			return errors.New("unhealthy")
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			return errors.New("unhealthy")
		}
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch os.Args[1] {
	case "control":
		master, e := security.DecodeKey(required("CREDENTIAL_KEY"))
		if e != nil {
			return e
		}
		seed, e := security.DecodeKey(required("SIGNING_KEY"))
		if e != nil {
			return e
		}
		admin := required("ADMIN_KEY")
		if len(admin) < 32 {
			return errors.New("ADMIN_KEY must be at least 32 characters")
		}
		ttl, e := strconv.Atoi(env("LEASE_SECONDS", "600"))
		if e != nil || ttl < 10 || ttl > 3600 {
			return errors.New("LEASE_SECONDS must be 10..3600")
		}
		connectCtx, c := context.WithTimeout(ctx, 15*time.Second)
		db, e := store.Open(connectCtx, required("DATABASE_URL"))
		c()
		if e != nil {
			return errors.New("database connection or migration failed")
		}
		defer db.Close()
		if e = store.BindCredentialKey(ctx, db, security.Hash(string(master))); e != nil {
			return e
		}
		dummy, e := security.Password(security.Random(24))
		if e != nil {
			return e
		}
		s := &control.Server{DB: db, AdminKey: admin, AdminPasswordHash: os.Getenv("ADMIN_CONSOLE_PASSWORD_HASH"), AdminCookieSecure: env("ADMIN_COOKIE_SECURE", "true") != "false", Master: master, Signing: ed25519.NewKeyFromSeed(seed), LeaseTTL: time.Duration(ttl) * time.Second, AckTimeout: 4 * time.Second, DummyPassword: dummy}
		public := httpServer(env("HTTP_ADDR", ":8080"), s.Public())
		internal := httpServer(env("INTERNAL_ADDR", ":8443"), s.Internal())

		errs := make(chan error, 2)
		go func() { errs <- public.ListenAndServe() }()
		go func() { errs <- internal.ListenAndServe() }()
		go s.Maintain(ctx)
		slog.Info("control plane ready")
		select {
		case <-ctx.Done():
		case e = <-errs:
			if !errors.Is(e, http.ErrServerClosed) {
				cancel()
			}
		}
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = public.Shutdown(stop)
		_ = internal.Shutdown(stop)
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case "agent":
		return agent.Run(ctx, agent.Config{ControlURL: required("CONTROL_URL"), ID: required("GATEWAY_ID"), Token: required("GATEWAY_AUTH_TOKEN"), Hysteria: env("HYSTERIA_BIN", "/usr/local/bin/hysteria"), TLSCert: required("GATEWAY_TLS_CERT"), TLSKey: required("GATEWAY_TLS_KEY"), Listen: env("GATEWAY_LISTEN", ":4433"), AuthListen: env("AUTH_LISTEN", "127.0.0.1:9080"), StatsListen: env("STATS_LISTEN", "127.0.0.1:9090"), AllowPrivate: os.Getenv("ALLOW_PRIVATE_TARGETS") == "true"})
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
