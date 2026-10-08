// nimbus-probe exercises real control-plane authorization and Hysteria2 through
// an isolated loopback SOCKS proxy. It never changes system networking.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nimbus/internal/controlclient"
	"nimbus/internal/securefile"
)

const officialDigest = "162ef8fe55dc7ec810dda662908f8e65ec36bd6da39cb87c3bd66184ab68f067"

func main() {
	base := flag.String("control", "https://192.168.194.128:8443", "control HTTPS origin")
	ca := flag.String("ca", ".local/ca.crt", "explicit lab CA; use -ca= for production system trust")
	account := flag.String("account", ".local/initial-account.json", "user credential file; never use admin/gateway credentials")
	core := flag.String("core", ".local/core/hysteria.exe", "fixed SHA256-verified Hysteria2 v2.13.0 binary")
	override := flag.String("lab-gateway-host", "", "explicit lab-only replacement of a loopback candidate host; keeps TLS SNI")
	resolveLocally := flag.Bool("lab-resolve-probe", false, "resolve only the fixed test site on Windows to diagnose gateway DNS; preserves HTTPS hostname validation")
	controlProbe := flag.Bool("lab-control-probe", false, "verify tunnel forwarding to the numeric-IP control HTTPS endpoint; does not prove public internet access")
	handshakeOnly := flag.Bool("handshake-only", false, "test QUIC authentication and lease lifecycle only; does not prove traffic forwarding")
	showCandidates := flag.Bool("show-candidates", false, "request a fresh lease, print only public candidate fields, then release it without starting the core")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, *base, *ca, *account, *core, *override, *resolveLocally, *controlProbe, *handshakeOnly, *showCandidates); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

type probeTarget struct {
	URL     string
	TLS     *tls.Config
	Control bool
}

func run(ctx context.Context, base, ca, accountFile, coreFile, override string, resolveLocally, controlProbe, handshakeOnly, showCandidates bool) (result error) {
	if showCandidates && (resolveLocally || controlProbe || handshakeOnly || override != "") {
		return errors.New("CONFLICTING_DIAGNOSTIC_MODES")
	}
	if resolveLocally && controlProbe || handshakeOnly && (resolveLocally || controlProbe) {
		return errors.New("CONFLICTING_LAB_PROBE_MODES")
	}
	testHost := "example.com"
	if resolveLocally {
		resolveContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		addresses, err := net.DefaultResolver.LookupIPAddr(resolveContext, testHost)
		cancel()
		if err != nil {
			return errors.New("LOCAL_PROBE_DNS_FAILED")
		}
		for _, address := range addresses {
			if address.IP.To4() != nil {
				testHost = address.IP.String()
				break
			}
		}
		if testHost == "example.com" {
			return errors.New("LOCAL_PROBE_IPV4_NOT_FOUND")
		}
		fmt.Println("LAB: fixed test-site DNS resolved locally; gateway DNS is NOT verified")
	}
	coreBytes, err := os.ReadFile(coreFile)
	if err != nil {
		return errors.New("CORE_NOT_FOUND")
	}
	digest := sha256.Sum256(coreBytes)
	if hex.EncodeToString(digest[:]) != officialDigest {
		return errors.New("CORE_DIGEST_MISMATCH")
	}
	corePath, err := filepath.Abs(coreFile)
	if err != nil {
		return errors.New("INVALID_CORE_PATH")
	}
	caPath := ""
	if ca != "" {
		caPath, err = filepath.Abs(ca)
		if err != nil {
			return errors.New("INVALID_CA_PATH")
		}
	}
	client, err := controlclient.New(base, caPath)
	if err != nil {
		return err
	}
	defer client.Close()
	target := probeTarget{URL: "https://" + testHost + "/", TLS: &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12}}
	if controlProbe {
		origin, _ := url.Parse(base)
		if net.ParseIP(origin.Hostname()) == nil {
			return errors.New("LAB_CONTROL_REQUIRES_NUMERIC_IP")
		}
		caBytes, err := os.ReadFile(caPath)
		if err != nil {
			return errors.New("CA_READ_FAILED")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return errors.New("INVALID_CA")
		}
		target = probeTarget{URL: strings.TrimRight(base, "/") + "/v1/countries", TLS: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, Control: true}
		fmt.Println("LAB: numeric-IP control HTTPS target; this proves LAN forwarding only, not public internet/DNS")
	}
	accountBytes, err := os.ReadFile(accountFile)
	if err != nil {
		return errors.New("ACCOUNT_FILE_NOT_FOUND")
	}
	var account struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.Unmarshal(accountBytes, &account) != nil || account.Email == "" || account.Password == "" {
		return errors.New("INVALID_USER_ACCOUNT_FILE")
	}
	if err := client.Login(ctx, account.Email, account.Password); err != nil {
		return err
	}
	account.Password = ""
	accountBytes = nil
	fmt.Println("PASS: control HTTPS CA and hostname validation; user login")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("DEVICE_KEY_GENERATION_FAILED")
	}
	if err := client.RegisterDevice(ctx, key); err != nil {
		return err
	}
	fmt.Println("PASS: temporary Ed25519 Windows device registered")
	var leaseID string
	defer func() {
		// Cleanup must remain possible after Ctrl+C or a failed connection.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if leaseID != "" {
			if err := client.Release(cleanup, leaseID); err != nil {
				fmt.Fprintln(os.Stderr, "CLEANUP: lease release failed:", err)
				if result == nil {
					result = errors.New("LEASE_CLEANUP_FAILED")
				}
			} else {
				fmt.Println("PASS: temporary lease released")
			}
		}
		if err := client.DeleteDevice(cleanup); err != nil {
			fmt.Fprintln(os.Stderr, "CLEANUP: temporary device deletion failed:", err)
			if result == nil {
				result = errors.New("DEVICE_CLEANUP_FAILED")
			}
		} else {
			fmt.Println("PASS: temporary device removed")
		}
	}()
	idempotency, err := controlclient.RandomID()
	if err != nil {
		return err
	}
	plan, err := client.Create(ctx, idempotency)
	leaseID = plan.LeaseID
	if err != nil {
		return err
	}
	fmt.Println("PASS: device proof accepted; gateway-ACKed temporary lease issued")
	if showCandidates {
		publicFields, err := candidateDiagnostic(base, plan.Candidates)
		if err != nil {
			return errors.New("PUBLIC_CANDIDATE_DIAGNOSTIC_FAILED")
		}
		fmt.Println(string(publicFields))
		return nil
	}
	candidate := plan.Candidates[0]
	if candidate.Protocol != "hysteria2" || candidate.Transport != "quic" || candidate.Credential == "" || candidate.Port < 1 || candidate.Port > 65535 || candidate.PublicParams.ServerName == "" {
		return errors.New("INVALID_HYSTERIA_CANDIDATE")
	}
	host := candidate.Host
	if override != "" {
		if net.ParseIP(override) == nil {
			return errors.New("INVALID_LAB_HOST_OVERRIDE")
		}
		if host == override {
			fmt.Println("PASS: candidate already matches the requested test gateway; no override needed")
		} else if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			host = override
			fmt.Println("LAB: loopback candidate mapped to explicit test gateway; original TLS SNI preserved")
		} else {
			return errors.New("LAB_GATEWAY_HOST_MISMATCH")
		}
	} else if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return errors.New("CANDIDATE_NOT_REMOTE_REACHABLE")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("SOCKS_PORT_ALLOCATION_FAILED")
	}
	socksAddress := listener.Addr().String()
	listener.Close()
	privateRoot, err := filepath.Abs(".local")
	if err != nil {
		return errors.New("PRIVATE_DIRECTORY_FAILED")
	}
	directory, err := os.MkdirTemp(privateRoot, "probe-")
	if err != nil {
		return errors.New("PRIVATE_DIRECTORY_FAILED")
	}
	// Verify the absolute recursive cleanup target stays in the intended root.
	if !strings.HasPrefix(strings.ToLower(filepath.Clean(directory)), strings.ToLower(privateRoot)+string(filepath.Separator)) {
		return errors.New("UNSAFE_CLEANUP_PATH")
	}
	defer os.RemoveAll(directory)
	if securefile.RestrictDirectory(directory) != nil {
		return errors.New("PRIVATE_DIRECTORY_ACL_FAILED")
	}
	config := struct {
		Server string `json:"server"`
		Auth   string `json:"auth"`
		TLS    struct {
			SNI string `json:"sni"`
			CA  string `json:"ca,omitempty"`
		} `json:"tls"`
		SOCKS5 struct {
			Listen string `json:"listen"`
		} `json:"socks5"`
	}{Server: net.JoinHostPort(host, fmt.Sprint(candidate.Port)), Auth: candidate.Credential}
	config.TLS.SNI, config.TLS.CA, config.SOCKS5.Listen = candidate.PublicParams.ServerName, caPath, socksAddress
	configBytes, err := json.Marshal(config)
	if err != nil {
		return errors.New("CORE_CONFIG_FAILED")
	}
	configPath := filepath.Join(directory, "client.json")
	if os.WriteFile(configPath, configBytes, 0600) != nil {
		return errors.New("CORE_CONFIG_WRITE_FAILED")
	}
	cmd := exec.CommandContext(ctx, corePath, "client", "--config", configPath, "--disable-update-check", "--log-level", "info")
	coreEvents := &safeCoreEvents{}
	cmd.Stdout, cmd.Stderr = coreEvents, coreEvents // Classify in memory; never retain/print raw core output.
	hideProcess(cmd)
	if cmd.Start() != nil {
		return errors.New("CORE_START_FAILED")
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if handshakeOnly {
		if err := waitForHandshake(ctx, coreEvents, 20*time.Second); err != nil {
			return err
		}
		fmt.Println("PASS: Hysteria2 QUIC/TLS handshake and temporary credential authentication (forwarding NOT verified)")
	} else {
		if err := probeUntil(ctx, socksAddress, target, 15*time.Second); err != nil {
			if coreEvents.authenticated() {
				fmt.Println("PASS: Hysteria2 QUIC/TLS handshake and temporary credential authentication")
			}
			return fmt.Errorf("%s (%s)", err, coreEvents.classification())
		}
		if controlProbe {
			fmt.Println("PASS: Hysteria2 QUIC/TLS authentication and validated LAN HTTPS forwarding through SOCKS5")
		} else {
			fmt.Println("PASS: Hysteria2 QUIC/TLS authentication and HTTPS forwarding through SOCKS5")
		}
	}
	if err := client.Activate(ctx, leaseID); err != nil {
		return err
	}
	fmt.Println("PASS: lease activated")
	// Exercise renew immediately in this bounded smoke; a persistent client must
	// schedule using renew_after_seconds and never extend an unacknowledged lease.
	select {
	case <-ctx.Done():
		return errors.New("PROBE_CANCELLED")
	case <-time.After(3 * time.Second):
	}
	renewed, err := client.Renew(ctx, leaseID)
	if err != nil {
		return err
	}
	if renewed.LeaseID != leaseID || !renewed.ExpiresAt.After(plan.ExpiresAt) {
		return errors.New("LEASE_NOT_EXTENDED")
	}
	if renewed.Candidates[0].Credential != candidate.Credential {
		return errors.New("RENEWED_CREDENTIAL_CHANGED")
	}
	fmt.Println("PASS: renewed lease ACKed; expiry extended")
	if !handshakeOnly {
		if err := probeUntil(ctx, socksAddress, target, 8*time.Second); err != nil {
			return err
		}
		fmt.Println("PASS: HTTPS forwarding remains usable after renewal")
	}
	if err := client.Release(ctx, leaseID); err != nil {
		return err
	}
	leaseID = ""
	fmt.Println("PASS: lease release accepted")
	if handshakeOnly {
		cmd.Process.Kill()
		cmd.Wait()
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(3 * time.Second):
		}
		// Reuse the exact released credential in a NEW core handshake. This checks
		// new-session rejection, not the termination of already established flows.
		rejectedEvents := &safeCoreEvents{}
		rejected := exec.CommandContext(ctx, corePath, "client", "--config", configPath, "--disable-update-check", "--log-level", "info")
		rejected.Stdout, rejected.Stderr = rejectedEvents, rejectedEvents
		hideProcess(rejected)
		if rejected.Start() != nil {
			return errors.New("REVOCATION_TEST_START_FAILED")
		}
		defer func() { rejected.Process.Kill(); rejected.Wait() }()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if rejectedEvents.authenticated() {
				return errors.New("RELEASED_CREDENTIAL_ACCEPTED")
			}
			if rejectedEvents.classification() == "CORE_AUTHENTICATION_FAILED" {
				fmt.Println("PASS: new handshake with released credential explicitly rejected")
				return nil
			}
			select {
			case <-ctx.Done():
				return errors.New("PROBE_CANCELLED")
			case <-time.After(100 * time.Millisecond):
			}
		}
		return errors.New("RELEASE_REJECTION_NOT_CONFIRMED")
	}
	// Verify the running core can no longer forward after server revocation sync.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(time.Second):
		}
		if probe(ctx, socksAddress, target) != nil {
			fmt.Println("PASS: forwarding denied after lease release")
			return nil
		}
	}
	return errors.New("RELEASED_LEASE_STILL_FORWARDS")
}

func waitForHandshake(ctx context.Context, events *safeCoreEvents, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if events.authenticated() {
			return nil
		}
		if code := events.classification(); code == "CORE_AUTHENTICATION_FAILED" || code == "CORE_TLS_VALIDATION_FAILED" || code == "CORE_CONFIG_FAILED" {
			return errors.New(code)
		}
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("QUIC_HANDSHAKE_NOT_CONFIRMED")
}

func probeUntil(ctx context.Context, address string, target probeTarget, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	var last error
	for time.Now().Before(deadline) {
		last = probe(ctx, address, target)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("HYSTERIA_FORWARDING_FAILED: %w", last)
}

func probe(ctx context.Context, socksAddress string, target probeTarget) error {
	proxy, _ := url.Parse("socks5://" + socksAddress)
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSClientConfig: target.TLS.Clone()}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	request, _ := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
	if !target.Control {
		request.Host = "example.com"
	}
	response, err := client.Do(request)
	if err != nil {
		var authority x509.UnknownAuthorityError
		var hostname x509.HostnameError
		var invalid x509.CertificateInvalidError
		if errors.As(err, &authority) || errors.As(err, &hostname) || errors.As(err, &invalid) {
			return errors.New("PROBE_TARGET_TLS_VALIDATION_FAILED")
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return errors.New("PROBE_TARGET_TIMEOUT")
		}
		if strings.Contains(strings.ToLower(err.Error()), "socks") {
			return errors.New("PROBE_SOCKS_CONNECTION_FAILED")
		}
		return errors.New("PROBE_FORWARDING_FAILED")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		return errors.New("PROBE_RESPONSE_INVALID")
	}
	if target.Control {
		if !json.Valid(body) || (response.StatusCode != 200 && response.StatusCode != 401) {
			return fmt.Errorf("PROBE_CONTROL_RESPONSE_INVALID_HTTP_%d", response.StatusCode)
		}
		return nil
	}
	if response.StatusCode != 200 || !strings.Contains(string(body), "Example Domain") {
		return errors.New("PROBE_RESPONSE_INVALID")
	}
	return nil
}

// This sink only retains a fixed diagnostic category. It cannot leak arbitrary
// log strings, destination addresses, credentials, URLs or config content.
type safeCoreEvents struct {
	mu        sync.Mutex
	category  string
	connected bool
}

func (s *safeCoreEvents) Write(data []byte) (int, error) {
	text := strings.ToLower(string(data))
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(text, "connected to server") {
		s.connected = true
	}
	switch {
	case strings.Contains(text, "x509"), strings.Contains(text, "certificate"):
		s.category = "CORE_TLS_VALIDATION_FAILED"
	case strings.Contains(text, "authentication"), strings.Contains(text, "unauthorized"):
		s.category = "CORE_AUTHENTICATION_FAILED"
	case strings.Contains(text, "no recent network activity"), strings.Contains(text, "timeout"):
		s.category = "CORE_NETWORK_TIMEOUT"
	case strings.Contains(text, "failed to load"), strings.Contains(text, "failed to read"), strings.Contains(text, "invalid config"):
		s.category = "CORE_CONFIG_FAILED"
	case strings.Contains(text, "address already in use"):
		s.category = "CORE_LISTEN_FAILED"
	case strings.Contains(text, "no such host"), strings.Contains(text, "lookup"):
		s.category = "CORE_EGRESS_DNS_FAILED"
	case strings.Contains(text, "connection refused"):
		s.category = "CORE_EGRESS_CONNECTION_REFUSED"
	case strings.Contains(text, "network is unreachable"), strings.Contains(text, "no route to host"):
		s.category = "CORE_EGRESS_UNREACHABLE"
	case strings.Contains(text, "permission denied"), strings.Contains(text, "blocked"), strings.Contains(text, "rejected"), strings.Contains(text, "denied"):
		s.category = "CORE_EGRESS_POLICY_DENIED"
	case s.category == "":
		s.category = "CORE_START_OR_FORWARDING_ERROR"
	}
	return len(data), nil
}

func (s *safeCoreEvents) authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *safeCoreEvents) classification() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.category == "" {
		return "NO_CORE_ERROR_REPORTED"
	}
	return s.category
}
