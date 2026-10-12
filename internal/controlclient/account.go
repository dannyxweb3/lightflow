package controlclient

import (
	"context"
	"errors"
	"time"
)

type Subscription struct {
	Plan            string    `json:"plan"`
	ExpiresAt       time.Time `json:"expires_at"`
	DeviceLimit     int       `json:"device_limit"`
	ConcurrentLimit int       `json:"concurrent_limit"`
	Enabled         bool      `json:"enabled"`
}
type Profile struct {
	ID           string       `json:"id"`
	Email        string       `json:"email"`
	Subscription Subscription `json:"subscription"`
}

func (c *Client) Profile(ctx context.Context) (Profile, error) {
	var p Profile
	_, _, err := c.call(ctx, "GET", "/v1/me", nil, false, "", &p)
	if err == nil && (p.ID == "" || p.Email == "" || p.Subscription.ExpiresAt.IsZero()) {
		err = errors.New("INVALID_ACCOUNT_RESPONSE")
	}
	return p, err
}
func (c *Client) DeviceCount(ctx context.Context) (int, error) {
	var result struct {
		Devices []struct {
			ID string `json:"id"`
		} `json:"devices"`
	}
	_, _, err := c.call(ctx, "GET", "/v1/devices", nil, false, "", &result)
	return len(result.Devices), err
}
