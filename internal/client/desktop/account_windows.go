//go:build windows

package desktop

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"nimbus/internal/client/sessionstore"
	"nimbus/internal/controlclient"
	"nimbus/internal/daemon"
)

func (b *Backend) sessionPath() string { return filepath.Join(b.Root, "session.dpapi") }
func (b *Backend) identityPath(id string) string {
	sum := sha256.Sum256([]byte(b.Config.Control + "\n" + id))
	return filepath.Join(b.Root, "identity-"+hex.EncodeToString(sum[:])+".dpapi")
}
func (b *Backend) Account() daemon.Account {
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	s, err := sessionstore.Load(b.sessionPath())
	if err != nil || s.Control != b.Config.Control || s.AccessToken == "" || s.RefreshToken == "" {
		return daemon.Account{}
	}
	a := daemon.Account{Authenticated: true}
	if s.Profile != nil {
		p := s.Profile
		a.Email, a.Plan, a.Enabled = p.Email, p.Subscription.Plan, p.Subscription.Enabled
		a.ExpiresAt = p.Subscription.ExpiresAt.UTC().Format(time.RFC3339)
		a.DeviceLimit, a.ConcurrentLimit, a.DeviceCount = p.Subscription.DeviceLimit, p.Subscription.ConcurrentLimit, s.DeviceCount
		a.CheckedAt = s.CheckedAt.UTC().Format(time.RFC3339)
	}
	return a
}
func accountError(err error) error {
	var api *controlclient.APIError
	if errors.As(err, &api) {
		if api.Code == "INVALID_CREDENTIALS" {
			return errors.New("INVALID_CREDENTIALS")
		}
		if api.Code == "DEVICE_LIMIT" {
			return errors.New("DEVICE_LIMIT_REACHED")
		}
		switch api.Status {
		case 401:
			return errors.New("LOGIN_REQUIRED")
		case 429:
			return errors.New("LOGIN_RATE_LIMITED")
		}
		switch api.Code {
		case "DEVICE_LIMIT_REACHED", "DEVICE_REVOKED", "INVALID_CREDENTIALS":
			return errors.New(api.Code)
		}
	}
	if err.Error() == "LOGIN_REQUIRED" {
		return err
	}
	return errors.New("ACCOUNT_REQUEST_FAILED")
}
func (b *Backend) noLease() error {
	_, err := os.Stat(b.sessionPath() + ".lease")
	if !os.IsNotExist(err) {
		return errors.New("PREVIOUS_LEASE_UNRESOLVED")
	}
	return nil
}
func (b *Backend) updateMetadata(ctx context.Context, c *controlclient.Client) error {
	p, err := c.Profile(ctx)
	if err != nil {
		return accountError(err)
	}
	count, err := c.DeviceCount(ctx)
	if err != nil {
		return accountError(err)
	}
	countries, err := c.Countries(ctx)
	if err != nil {
		return accountError(err)
	}
	c.SetAccountMetadata(p, count)
	if err = sessionstore.Save(b.sessionPath(), c.Session()); err != nil {
		return err
	}
	b.Config.Countries = nil
	for _, country := range countries {
		if country.Available {
			b.Config.Countries = append(b.Config.Countries, daemon.Country{Code: country.Code, Name: country.Name})
		}
	}
	return nil
}
func (b *Backend) Login(ctx context.Context, email, password string) error {
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if err := b.noLease(); err != nil {
		return err
	}
	c, err := controlclient.New(b.Config.Control, "")
	if err != nil {
		return errors.New("ACCOUNT_REQUEST_FAILED")
	}
	defer c.Close()
	if err = c.Login(ctx, email, password); err != nil {
		return accountError(err)
	}
	password = ""
	p, err := c.Profile(ctx)
	if err != nil {
		return accountError(err)
	}
	var key ed25519.PrivateKey
	if saved, e := sessionstore.Load(b.identityPath(p.ID)); e == nil && saved.Control == b.Config.Control {
		key = saved.DeviceKey
	}
	if saved, e := sessionstore.Load(b.sessionPath()); e == nil && saved.Profile != nil && saved.Profile.ID == p.ID && saved.Control == b.Config.Control {
		key = saved.DeviceKey
	}
	if len(key) != ed25519.PrivateKeySize {
		_, key, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return errors.New("DEVICE_KEY_FAILED")
		}
	}
	// Save before registration: a lost HTTP response must reuse the same key.
	if err = sessionstore.Save(b.identityPath(p.ID), controlclient.Session{Control: b.Config.Control, DeviceKey: key, Profile: &p}); err != nil {
		return err
	}
	if err = c.RegisterDevice(ctx, key); err != nil {
		return accountError(err)
	}
	// Persist the reusable identity without login tokens even if a later summary request fails.
	identity := controlclient.Session{Control: b.Config.Control, DeviceID: c.Session().DeviceID, DeviceKey: key, Profile: &p}
	if err = sessionstore.Save(b.identityPath(p.ID), identity); err != nil {
		return err
	}
	return b.updateMetadata(ctx, c)
}
func (b *Backend) RefreshAccount(ctx context.Context) error {
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if err := b.noLease(); err != nil {
		return err
	}
	saved, err := sessionstore.Load(b.sessionPath())
	if err != nil {
		return errors.New("LOGIN_REQUIRED")
	}
	if saved.Control != b.Config.Control {
		return errors.New("LOGIN_REQUIRED")
	}
	c, err := controlclient.Restore(saved)
	if err != nil {
		return errors.New("LOGIN_REQUIRED")
	}
	defer c.Close()
	c.SaveSession = func(s controlclient.Session) error { return sessionstore.Save(b.sessionPath(), s) }
	return b.updateMetadata(ctx, c)
}
func (b *Backend) Logout(ctx context.Context) error {
	b.accountMu.Lock()
	defer b.accountMu.Unlock()
	if err := ctx.Err(); err != nil {
		return errors.New("ACCOUNT_REQUEST_FAILED")
	}
	if err := b.noLease(); err != nil {
		return err
	}
	saved, err := sessionstore.Load(b.sessionPath())
	if err != nil {
		if _, e := os.Stat(b.sessionPath()); os.IsNotExist(e) {
			return nil
		}
		return err
	}
	// Never call cloud logout: its scope is the entire account, including other devices.
	if saved.Profile != nil {
		identity := controlclient.Session{Control: saved.Control, DeviceID: saved.DeviceID, DeviceKey: saved.DeviceKey, Profile: saved.Profile}
		if err = sessionstore.Save(b.identityPath(saved.Profile.ID), identity); err != nil {
			return err
		}
	}
	if err = os.Remove(b.sessionPath()); err != nil {
		return errors.New("LOCAL_LOGOUT_FAILED")
	}
	b.Config.Countries = nil
	return nil
}
