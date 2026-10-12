package controlclient

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"time"
)

// Session is service-private. Never include it in local IPC or diagnostic output.
type Session struct {
	Control      string
	AccessToken  string
	RefreshToken string
	AccessUntil  time.Time
	DeviceID     string
	DeviceKey    ed25519.PrivateKey
	Profile      *Profile
	DeviceCount  int
	CheckedAt    time.Time
}

func (c *Client) Session() Session {
	return Session{Control: c.base.String(), AccessToken: c.token, RefreshToken: c.refresh, AccessUntil: c.accessUntil, DeviceID: c.deviceID, DeviceKey: append(ed25519.PrivateKey(nil), c.key...), Profile: c.profile, DeviceCount: c.deviceCount, CheckedAt: c.checkedAt}
}

func Restore(s Session) (*Client, error) {
	if s.AccessToken == "" || s.RefreshToken == "" || s.DeviceID == "" || len(s.DeviceKey) != ed25519.PrivateKeySize || s.AccessUntil.IsZero() {
		return nil, errors.New("INVALID_SAVED_SESSION")
	}
	c, err := New(s.Control, "")
	if err != nil {
		return nil, err
	}
	c.token, c.refresh, c.accessUntil, c.deviceID, c.key = s.AccessToken, s.RefreshToken, s.AccessUntil, s.DeviceID, s.DeviceKey
	c.profile, c.deviceCount, c.checkedAt = s.Profile, s.DeviceCount, s.CheckedAt
	return c, nil
}

func (c *Client) SetAccountMetadata(p Profile, count int) {
	c.profile, c.deviceCount, c.checkedAt = &p, count, time.Now().UTC()
}

func (c *Client) refreshAccess(ctx context.Context) error {
	if c.refresh == "" {
		return errors.New("LOGIN_REQUIRED")
	}
	body, _ := json.Marshal(struct {
		Refresh string `json:"refresh_token"`
	}{c.refresh})
	// Consume once. An ambiguous response must never replay the rotated token.
	c.refresh = ""
	// Persist consumption before sending. After an ambiguous response a restart
	// must require login, rather than replaying a possibly rotated refresh token.
	if c.SaveSession != nil {
		if err := c.SaveSession(c.Session()); err != nil {
			return err
		}
	}
	var response struct {
		AccessToken  string    `json:"access_token"`
		RefreshToken string    `json:"refresh_token"`
		ExpiresAt    time.Time `json:"expires_at"`
	}
	_, _, err := c.call(ctx, "POST", "/v1/auth/refresh", body, false, "", &response)
	if err != nil {
		return errors.New("LOGIN_REQUIRED")
	}
	if response.AccessToken == "" || response.RefreshToken == "" || !response.ExpiresAt.After(time.Now()) {
		return errors.New("INVALID_REFRESH_RESPONSE")
	}
	c.token, c.refresh, c.accessUntil = response.AccessToken, response.RefreshToken, response.ExpiresAt
	if c.SaveSession != nil {
		return c.SaveSession(c.Session())
	}
	return nil
}

type PublicCountry struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

func (c *Client) Countries(ctx context.Context) ([]PublicCountry, error) {
	var response struct {
		Countries []PublicCountry `json:"countries"`
	}
	_, _, err := c.call(ctx, "GET", "/v1/countries", nil, false, "", &response)
	return response.Countries, err
}
