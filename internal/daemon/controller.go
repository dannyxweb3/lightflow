package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
	"time"
)

const APIVersion = 1

type Request struct {
	APIVersion int             `json:"api_version"`
	RequestID  string          `json:"request_id"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
}

type RPCError struct {
	Code string `json:"code"`
}

type Response struct {
	APIVersion int       `json:"api_version"`
	RequestID  string    `json:"request_id"`
	Result     *Snapshot `json:"result,omitempty"`
	Error      *RPCError `json:"error,omitempty"`
}

type Country struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	City      string `json:"city"`
	LatencyMS int    `json:"latency_ms"`
}

type Settings struct {
	Mode      string `json:"mode"`
	Transport string `json:"transport_preference"`
	AllowLAN  bool   `json:"allow_lan"`
}

type Snapshot struct {
	InstanceID  string    `json:"instance_id"`
	Sequence    uint64    `json:"sequence"`
	State       string    `json:"state"`
	CountryCode string    `json:"country_code"`
	OperationID string    `json:"operation_id"`
	ConnectedAt string    `json:"connected_at,omitempty"`
	Simulation  bool      `json:"simulation"`
	Version     string    `json:"daemon_version"`
	Settings    Settings  `json:"settings"`
	Countries   []Country `json:"countries"`
	LastError   string    `json:"last_error,omitempty"`
	Account     *Account  `json:"account,omitempty"`
}

type ConnectParams struct {
	CountryCode string `json:"country_code"`
}

type cached struct {
	fingerprint string
	response    Response
}

// Controller owns all connection state. This milestone contains ONLY an explicit
// development simulation. No core is launched and no system network is changed.
type Controller struct {
	mu            sync.Mutex
	development   bool
	snapshot      Snapshot
	generation    uint64
	cancel        chan struct{}
	cache         map[string]cached
	cacheOrder    []string
	step          time.Duration
	backend       Backend
	realCancel    context.CancelFunc
	realDone      chan struct{}
	accountCancel context.CancelFunc
	accountDone   chan struct{}
}

func New(development bool, step time.Duration) *Controller {
	return &Controller{
		development: development, step: step, cache: make(map[string]cached),
		snapshot: Snapshot{InstanceID: newID(), State: "disconnected", Simulation: development, Version: "0.1.0",
			Settings: Settings{Mode: "smart", Transport: "auto", AllowLAN: true},
			Countries: []Country{
				{Code: "SG", Name: "新加坡", City: "新加坡", LatencyMS: 38},
				{Code: "JP", Name: "日本", City: "东京", LatencyMS: 52},
				{Code: "US", Name: "美国", City: "洛杉矶", LatencyMS: 168},
				{Code: "DE", Name: "德国", City: "法兰克福", LatencyMS: 192},
				{Code: "AU", Name: "澳大利亚", City: "悉尼", LatencyMS: 126},
				{Code: "GB", Name: "英国", City: "伦敦", LatencyMS: 201},
			},
		},
	}
}

func decodeParams(raw json.RawMessage, target any) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

func (c *Controller) Handle(req Request) Response {
	c.mu.Lock()
	defer c.mu.Unlock()
	response := Response{APIVersion: APIVersion, RequestID: req.RequestID}
	fail := func(code string) Response { response.Error = &RPCError{Code: code}; return response }
	if req.APIVersion != APIVersion || len(req.RequestID) == 0 || len(req.RequestID) > 80 {
		return fail("INVALID_REQUEST")
	}
	fingerprint := requestFingerprint(req)
	if req.Method != "get_snapshot" {
		if previous, ok := c.cache[req.RequestID]; ok {
			if previous.fingerprint != fingerprint {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			return previous.response
		}
	}
	if !c.development && c.backend == nil {
		return fail("NETWORK_ADAPTER_NOT_READY")
	}
	switch req.Method {
	case "login", "logout", "refresh_account":
		if c.snapshot.LastError == "NETWORK_RECOVERY_FAILED" {
			return fail("NETWORK_RECOVERY_FAILED")
		}
		if _, ok := c.backend.(AccountBackend); !ok {
			return fail("ACCOUNT_NOT_AVAILABLE")
		}
		if c.snapshot.Account.Busy {
			return fail("ACCOUNT_BUSY")
		}
		var params struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if req.Method == "login" {
			if !decodeParams(req.Params, &params) || !validLogin(params.Email, params.Password) {
				return fail("INVALID_PARAMS")
			}
			if c.snapshot.Account.Authenticated {
				return fail("LOGOUT_BEFORE_LOGIN")
			}
		} else if !decodeParams(req.Params, &struct{}{}) {
			return fail("INVALID_PARAMS")
		}
		if req.Method != "logout" && c.snapshot.State != "disconnected" {
			return fail("DISCONNECT_BEFORE_ACCOUNT")
		}
		c.startAccountLocked(req.Method, params.Email, params.Password)
	case "get_snapshot":
		if !decodeParams(req.Params, &struct{}{}) {
			return fail("INVALID_PARAMS")
		}
	case "connect":
		if c.snapshot.Account != nil {
			if c.snapshot.Account.Busy {
				return fail("ACCOUNT_BUSY")
			}
			if !c.snapshot.Account.Authenticated {
				return fail("LOGIN_REQUIRED")
			}
			expires, _ := time.Parse(time.RFC3339, c.snapshot.Account.ExpiresAt)
			if !c.snapshot.Account.Enabled || !expires.After(time.Now()) {
				return fail("SUBSCRIPTION_INACTIVE")
			}
		}
		var params ConnectParams
		if !decodeParams(req.Params, &params) {
			return fail("INVALID_PARAMS")
		}
		if c.snapshot.Settings.Mode == "direct" {
			return fail("DIRECT_MODE")
		}
		if c.backend != nil && c.snapshot.Settings.Transport == "tcp" {
			return fail("TRANSPORT_NOT_AVAILABLE")
		}
		country := params.CountryCode
		if country == "" && c.backend == nil {
			country = "SG"
		}
		found := country == "" && c.backend != nil
		for _, available := range c.snapshot.Countries {
			if available.Code == country {
				found = true
			}
		}
		if !found {
			return fail("INVALID_COUNTRY")
		}
		if c.snapshot.State != "disconnected" {
			return fail("CONNECTION_BUSY")
		}
		c.generation++
		c.cancel = make(chan struct{})
		c.snapshot.CountryCode, c.snapshot.OperationID = country, newID()
		c.snapshot.LastError = ""
		c.setState("authorizing")
		if c.backend != nil {
			ctx, cancel := context.WithCancel(context.Background())
			c.realCancel = cancel
			c.realDone = make(chan struct{})
			go c.runReal(ctx, c.generation, c.snapshot.OperationID, country, c.snapshot.Settings, c.realDone)
		} else {
			go c.simulate(c.generation, c.cancel)
		}
	case "disconnect":
		if !decodeParams(req.Params, &struct{}{}) {
			return fail("INVALID_PARAMS")
		}
		c.stopLocked()
	case "update_settings":
		var params struct {
			Mode      string `json:"mode"`
			Transport string `json:"transport_preference"`
			AllowLAN  *bool  `json:"allow_lan"`
		}
		if !decodeParams(req.Params, &params) || params.AllowLAN == nil || !oneOf(params.Mode, "smart", "global", "direct") || !oneOf(params.Transport, "auto", "quic", "tcp") {
			return fail("INVALID_PARAMS")
		}
		// Switching policies during a live session requires a network transaction,
		// which is not implemented yet. Explicitly disconnect before changing it.
		if c.snapshot.State != "disconnected" {
			return fail("DISCONNECT_BEFORE_SETTINGS")
		}
		if c.backend != nil && (params.Transport == "tcp" || !*params.AllowLAN) {
			return fail("SETTING_NOT_AVAILABLE")
		}
		c.snapshot.Settings = Settings{Mode: params.Mode, Transport: params.Transport, AllowLAN: *params.AllowLAN}
		c.snapshot.Sequence++
	default:
		return fail("UNKNOWN_METHOD")
	}
	snapshot := c.snapshot
	if snapshot.Account != nil {
		account := *snapshot.Account
		snapshot.Account = &account
	}
	snapshot.Countries = append([]Country{}, c.snapshot.Countries...)
	response.Result = &snapshot
	if req.Method != "get_snapshot" {
		if len(c.cacheOrder) >= 256 {
			delete(c.cache, c.cacheOrder[0])
			c.cacheOrder = c.cacheOrder[1:]
		}
		c.cacheOrder = append(c.cacheOrder, req.RequestID)
		c.cache[req.RequestID] = cached{fingerprint: fingerprint, response: response}
	}
	return response
}

func (c *Controller) simulate(generation uint64, cancel <-chan struct{}) {
	for _, state := range []string{"preparing", "connecting", "verifying", "connected"} {
		timer := time.NewTimer(c.step)
		select {
		case <-cancel:
			timer.Stop()
			return
		case <-timer.C:
		}
		c.mu.Lock()
		if c.generation != generation {
			c.mu.Unlock()
			return
		}
		c.setState(state)
		if state == "connected" {
			c.snapshot.ConnectedAt = time.Now().UTC().Format(time.RFC3339)
		}
		c.mu.Unlock()
	}
}

func (c *Controller) setState(state string) { c.snapshot.State = state; c.snapshot.Sequence++ }

func (c *Controller) stopLocked() {
	if c.backend != nil && c.realCancel != nil {
		c.generation++
		c.realCancel()
		c.setState("disconnecting")
		return
	}
	c.generation++
	if c.cancel != nil {
		close(c.cancel)
		c.cancel = nil
	}
	c.snapshot.CountryCode, c.snapshot.ConnectedAt, c.snapshot.OperationID = "", "", ""
	c.setState("disconnected")
}

func (c *Controller) Close() {
	c.mu.Lock()
	c.stopLocked()
	done := c.realDone
	accountDone := c.accountDone
	if c.accountCancel != nil {
		c.accountCancel()
	}
	c.mu.Unlock()
	if done != nil {
		<-done
	}
	if accountDone != nil {
		<-accountDone
	}
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func newID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("system entropy unavailable")
	}
	return hex.EncodeToString(data[:])
}
