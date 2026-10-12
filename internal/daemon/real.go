package daemon

import (
	"context"
	"regexp"
	"time"
)

// Backend runs outside the controller lock. Ready is legal only after real
// forwarding and activation. Returning means all teardown has completed.
type Backend interface {
	Countries() []Country
	Run(context.Context, string, Settings, func(string)) error
}

func NewReal(backend Backend) *Controller {
	c := New(false, 0)
	c.backend = backend
	c.snapshot.Countries = backend.Countries()
	c.snapshot.Settings = Settings{Mode: "global", Transport: "quic", AllowLAN: true}
	c.snapshot.Version = "0.2.0-dev"
	if auth, ok := backend.(AccountBackend); ok {
		account := auth.Account()
		c.snapshot.Account = &account
	}
	return c
}

func (c *Controller) runReal(ctx context.Context, generation uint64, operation, country string, settings Settings, done chan struct{}) {
	defer close(done)
	err := c.backend.Run(ctx, country, settings, func(actualCountry string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.generation != generation || ctx.Err() != nil {
			return
		}
		c.snapshot.CountryCode = actualCountry
		c.snapshot.ConnectedAt = time.Now().UTC().Format(time.RFC3339)
		c.setState("connected")
	})
	var account *Account
	if auth, ok := c.backend.(AccountBackend); ok {
		a := auth.Account()
		account = &a
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot.OperationID != operation {
		return
	}
	c.realCancel = nil
	if account != nil && !c.snapshot.Account.Busy {
		c.snapshot.Account = account
	}
	c.snapshot.CountryCode, c.snapshot.ConnectedAt, c.snapshot.OperationID = "", "", ""
	if err != nil {
		c.snapshot.LastError = safeCode(err)
	}
	c.setState("disconnected")
}

func safeCode(err error) string {
	code := err.Error()
	if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`).MatchString(code) {
		return "REAL_CONNECTION_FAILED"
	}
	return code
}
