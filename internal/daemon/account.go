package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Account is a public summary. It must never contain any authentication secret.
type Account struct {
	Authenticated   bool   `json:"authenticated"`
	Busy            bool   `json:"busy"`
	Email           string `json:"email"`
	Plan            string `json:"plan"`
	ExpiresAt       string `json:"expires_at"`
	Enabled         bool   `json:"enabled"`
	DeviceCount     int    `json:"device_count"`
	DeviceLimit     int    `json:"device_limit"`
	ConcurrentLimit int    `json:"concurrent_limit"`
	CheckedAt       string `json:"checked_at"`
}
type AccountBackend interface {
	Account() Account
	Login(context.Context, string, string) error
	RefreshAccount(context.Context) error
	Logout(context.Context) error
}

func requestFingerprint(req Request) string {
	digest := sha256.Sum256(req.Params)
	return req.Method + ":" + hex.EncodeToString(digest[:])
}
func (c *Controller) startAccountLocked(method, email, password string) {
	backend := c.backend.(AccountBackend)
	if method == "logout" {
		c.stopLocked()
	}
	done := c.realDone
	c.snapshot.Account.Busy = true
	c.snapshot.LastError = ""
	c.snapshot.Sequence++
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	c.accountCancel = cancel
	c.accountDone = make(chan struct{})
	finished := c.accountDone
	go func() {
		defer close(finished)
		defer cancel()
		var err error
		if method == "logout" && done != nil {
			select {
			case <-done:
			case <-ctx.Done():
			}
			c.mu.Lock()
			if c.snapshot.LastError != "" {
				err = errors.New(c.snapshot.LastError)
			}
			c.mu.Unlock()
		}
		if err == nil {
			switch method {
			case "login":
				err = backend.Login(ctx, email, password)
				password = ""
			case "refresh_account":
				err = backend.RefreshAccount(ctx)
			case "logout":
				err = backend.Logout(ctx)
			}
		}
		account := backend.Account()
		countries := c.backend.Countries()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.snapshot.Account = &account
		c.snapshot.Countries = countries
		c.accountCancel = nil
		if err != nil {
			c.snapshot.LastError = safeCode(err)
		}
		c.snapshot.Sequence++
	}()
}
func validLogin(email, password string) bool {
	return len(email) <= 254 && strings.Contains(email, "@") && strings.TrimSpace(email) == email && len(password) > 0 && len(password) <= 128
}
