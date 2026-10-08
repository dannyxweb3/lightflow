// Package controlclient implements the synced server API without exposing
// tokens or proxy credentials to the desktop UI.
package controlclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type APIError struct {
	Code   string
	Status int
}

func (e *APIError) Error() string { return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status) }

type Client struct {
	base     *url.URL
	http     *http.Client
	token    string
	deviceID string
	key      ed25519.PrivateKey
}

type Candidate struct {
	EndpointID   string `json:"endpoint_id"`
	Protocol     string `json:"protocol"`
	Transport    string `json:"transport"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Credential   string `json:"credential"`
	PublicParams struct {
		ServerName string `json:"server_name"`
	} `json:"public_params"`
}

type Plan struct {
	SchemaVersion     int         `json:"schema_version"`
	LeaseID           string      `json:"lease_id"`
	DeviceID          string      `json:"device_id"`
	State             string      `json:"state"`
	ExpiresAt         time.Time   `json:"expires_at"`
	CountryCode       string      `json:"country_code"`
	RenewAfterSeconds int         `json:"renew_after_seconds"`
	Candidates        []Candidate `json:"candidates"`
}

func New(baseURL, caFile string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("INVALID_CONTROL_URL")
	}
	// A nil pool uses the OS trust store for public production certificates.
	var pool *x509.CertPool
	if caFile != "" {
		ca, err := os.ReadFile(caFile)
		if err != nil {
			return nil, errors.New("CA_READ_FAILED")
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			return nil, errors.New("INVALID_CA")
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REDIRECT_NOT_ALLOWED") }}
	return &Client{base: base, http: client}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Login(ctx context.Context, email, password string) error {
	body, _ := json.Marshal(struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{email, password})
	var response struct {
		AccessToken string `json:"access_token"`
	}
	status, _, err := c.call(ctx, "POST", "/v1/auth/login", body, false, "", &response)
	if err != nil {
		return err
	}
	if status != http.StatusOK || response.AccessToken == "" {
		return errors.New("INVALID_LOGIN_RESPONSE")
	}
	c.token = response.AccessToken
	return nil
}

func (c *Client) RegisterDevice(ctx context.Context, key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return errors.New("INVALID_DEVICE_KEY")
	}
	body, _ := json.Marshal(struct {
		Name      string `json:"name"`
		OS        string `json:"os"`
		PublicKey string `json:"public_key"`
	}{"Nimbus Windows temporary connectivity probe", "windows", base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))})
	var response struct {
		ID string `json:"id"`
	}
	status, _, err := c.call(ctx, "POST", "/v1/devices", body, false, "", &response)
	if err != nil {
		return err
	}
	if (status != 200 && status != 201) || response.ID == "" {
		return errors.New("INVALID_DEVICE_RESPONSE")
	}
	c.deviceID, c.key = response.ID, key
	return nil
}

func (c *Client) DeleteDevice(ctx context.Context) error {
	if c.deviceID == "" {
		return nil
	}
	_, _, err := c.call(ctx, "DELETE", "/v1/devices/"+url.PathEscape(c.deviceID), nil, false, "", nil)
	return err
}

func (c *Client) Create(ctx context.Context, idempotencyKey string) (Plan, error) {
	// The body remains byte-identical across pending retries; nonce/signature do not.
	body := []byte(`{"country_code":"SG","mode":"global","protocols":["hysteria2"]}`)
	return c.plan(ctx, "POST", "/v1/connection-sessions", body, idempotencyKey)
}

func (c *Client) Renew(ctx context.Context, id string) (Plan, error) {
	return c.plan(ctx, "POST", "/v1/connection-sessions/"+url.PathEscape(id)+"/renew", nil, "")
}

func (c *Client) Activate(ctx context.Context, id string) error {
	_, _, err := c.call(ctx, "POST", "/v1/connection-sessions/"+url.PathEscape(id)+"/activate", nil, true, "", nil)
	return err
}

func (c *Client) Release(ctx context.Context, id string) error {
	_, _, err := c.call(ctx, "DELETE", "/v1/connection-sessions/"+url.PathEscape(id), nil, true, "", nil)
	return err
}

func (c *Client) plan(ctx context.Context, method, path string, body []byte, idempotency string) (Plan, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var latest Plan
	for {
		var response Plan
		status, retry, err := c.call(ctx, method, path, body, true, idempotency, &response)
		if err != nil {
			return latest, err
		}
		latest = response // Preserve pending lease ID for cleanup if retries fail.
		if status == 200 {
			if response.SchemaVersion != 1 || response.LeaseID == "" || response.DeviceID != c.deviceID || !response.ExpiresAt.After(time.Now()) || len(response.Candidates) == 0 {
				return latest, errors.New("INVALID_CONNECTION_PLAN")
			}
			return latest, nil
		}
		if status != 202 || response.LeaseID == "" {
			return latest, errors.New("INVALID_PENDING_RESPONSE")
		}
		if retry < time.Second || retry > 5*time.Second {
			retry = time.Second
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return latest, errors.New("GATEWAY_ACK_TIMEOUT")
		case <-timer.C:
		}
	}
}

var safeCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (c *Client) call(ctx context.Context, method, path string, body []byte, signed bool, idempotency string, out any) (int, time.Duration, error) {
	endpoint := *c.base
	parsed, err := url.Parse(path)
	if err != nil {
		return 0, 0, errors.New("INVALID_API_PATH")
	}
	endpoint.Path, endpoint.RawPath = parsed.Path, parsed.RawPath
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return 0, 0, errors.New("REQUEST_BUILD_FAILED")
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
	if signed {
		if c.deviceID == "" || len(c.key) != ed25519.PrivateKeySize {
			return 0, 0, errors.New("DEVICE_NOT_REGISTERED")
		}
		nonce, err := RandomID()
		if err != nil {
			return 0, 0, err
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("X-Device-ID", c.deviceID)
		req.Header.Set("X-Device-Timestamp", timestamp)
		req.Header.Set("X-Device-Nonce", nonce)
		req.Header.Set("X-Device-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(c.key, SigningBytes(method, req.URL.EscapedPath(), timestamp, nonce, body))))
	}
	response, err := c.http.Do(req)
	if err != nil {
		// Do not propagate request/body or upstream error text into logs.
		var unknown x509.UnknownAuthorityError
		var hostname x509.HostnameError
		if errors.As(err, &unknown) {
			return 0, 0, errors.New("CONTROL_CA_UNTRUSTED")
		}
		if errors.As(err, &hostname) {
			return 0, 0, errors.New("CONTROL_CERT_NAME_MISMATCH")
		}
		return 0, 0, errors.New("CONTROL_REQUEST_FAILED")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return response.StatusCode, 0, errors.New("INVALID_RESPONSE_SIZE")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Code string `json:"code"`
		}
		json.Unmarshal(data, &failure)
		if !safeCode.MatchString(failure.Code) {
			failure.Code = "API_ERROR"
		}
		return response.StatusCode, 0, &APIError{Code: failure.Code, Status: response.StatusCode}
	}
	if out != nil && len(data) != 0 {
		if json.Unmarshal(data, out) != nil {
			return response.StatusCode, 0, errors.New("INVALID_API_RESPONSE")
		}
	}
	retrySeconds, _ := strconv.Atoi(response.Header.Get("Retry-After"))
	return response.StatusCode, time.Duration(retrySeconds) * time.Second, nil
}

func SigningBytes(method, escapedPath, timestamp, nonce string, body []byte) []byte {
	digest := sha256.Sum256(body)
	return []byte(strings.Join([]string{strings.ToUpper(method), escapedPath, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n"))
}

func RandomID() (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("ENTROPY_UNAVAILABLE")
	}
	return hex.EncodeToString(random[:]), nil
}
