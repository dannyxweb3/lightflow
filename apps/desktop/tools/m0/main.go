// Isolated M0 developer probe. TUN requires an explicit flag and elevation.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nimbus/internal/controlclient"
	"nimbus/internal/securefile"
)

func main() {
	account := flag.String("account", ".local/windows-s0-account.json", "S0 lab account file")
	control := flag.String("control", "https://lightflow.u26d.local:8443", "explicit S0 control origin; no automatic fallback")
	mode := flag.String("mode", "global", "global or smart")
	check := flag.Bool("check", false, "only validate locked assets and placeholder config; no API login")
	tun := flag.Bool("tun", false, "explicit isolated-machine TUN experiment; requires elevation")
	forced := flag.Bool("force-kill", false, "TUN experiment: kill owned core instead of disabling TUN normally")
	dnsOff := flag.Bool("tun-dns-off", false, "diagnostic only: isolate TUN forwarding with DNS module disabled")
	resolveProbe := flag.Bool("resolve-probe", false, "diagnostic only: resolve fixed target via explicit DNS before core startup")
	relaxed := flag.Bool("tun-relaxed", false, "diagnostic only: disable strict-route, no DNS protection claim")
	osRoute := flag.Bool("tun-os-route", false, "diagnostic only: use OS gateway route instead of interface binding")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	if err := run(ctx, *control, *account, *mode, *check, *tun, *forced, *dnsOff, *resolveProbe, *relaxed, *osRoute); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		var api *controlclient.APIError
		if errors.As(err, &api) && api.RequestID != "" {
			fmt.Fprintln(os.Stderr, "X-Request-ID:", api.RequestID)
		}
		os.Exit(1)
	}
}

func verifyAssets() error {
	raw, err := os.ReadFile("apps/desktop/tools/m0/assets.lock.json")
	if err != nil {
		return errors.New("LOCK_FILE_NOT_FOUND")
	}
	var lock struct {
		Assets []struct{ Name, SHA256 string }
		Core   string `json:"core_executable_sha256"`
	}
	if json.Unmarshal(raw, &lock) != nil || len(lock.Assets) != 3 || len(lock.Core) != 64 {
		return errors.New("INVALID_ASSET_LOCK")
	}
	for _, asset := range append(lock.Assets, struct{ Name, SHA256 string }{"mihomo-windows-amd64.exe", lock.Core}) {
		if filepath.Base(asset.Name) != asset.Name {
			return errors.New("INVALID_ASSET_NAME")
		}
		f, err := os.Open(filepath.Join(".local/m0/assets", asset.Name))
		if err != nil {
			return errors.New("ASSET_NOT_FOUND")
		}
		hash := sha256.New()
		_, err = io.Copy(hash, f)
		f.Close()
		if err != nil || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
			return errors.New("ASSET_HASH_MISMATCH")
		}
	}
	return nil
}

func configuration(candidate controlclient.Candidate, mode, ca, pipe string, port int, auth string) ([]byte, error) {
	if mode != "global" && mode != "smart" {
		return nil, errors.New("INVALID_CONNECTION_MODE")
	}
	if candidate.Protocol != "hysteria2" || candidate.Transport != "quic" || candidate.Host == "" || candidate.Credential == "" || candidate.PublicParams.ServerName == "" || candidate.Port < 1 || candidate.Port > 65535 {
		return nil, errors.New("INVALID_HYSTERIA_CANDIDATE")
	}
	if candidate.Host == "localhost" || (net.ParseIP(candidate.Host) != nil && net.ParseIP(candidate.Host).IsLoopback()) {
		return nil, errors.New("CANDIDATE_NOT_REMOTE_REACHABLE")
	}
	raw, err := os.ReadFile("apps/desktop/tools/m0/baseline.json")
	if err != nil {
		return nil, errors.New("BASELINE_NOT_FOUND")
	}
	var config map[string]any
	if json.Unmarshal(raw, &config) != nil {
		return nil, errors.New("INVALID_BASELINE")
	}
	config["socks-port"], config["authentication"], config["external-controller-pipe"] = port, []string{"m0:" + auth}, pipe
	config["tls"] = map[string]any{"custom-certifactes": []string{ca}} // Fixed upstream spelling in v1.19.32.
	config["proxies"] = []any{map[string]any{"name": "LF-TUNNEL", "type": "hysteria2", "server": candidate.Host, "port": candidate.Port, "password": candidate.Credential, "sni": candidate.PublicParams.ServerName, "skip-cert-verify": false, "udp": true}}
	if mode == "smart" {
		config["rules"] = []string{"GEOSITE,cn,DIRECT", "GEOIP,cn,DIRECT,no-resolve", "MATCH,LF-TUNNEL"}
	}
	// Global also uses rule mode: its sole MATCH rule forces the selected tunnel.
	return json.MarshalIndent(config, "", "  ")
}

func run(ctx context.Context, control, accountFile, mode string, check, tun, forced, dnsOff, resolveProbe, relaxed, osRoute bool) (result error) {
	if control != "https://lightflow.u26d.local:8443" && control != "https://192.168.194.128:8443" {
		return errors.New("S0_CONTROL_ORIGIN_NOT_ALLOWED")
	}
	fmt.Println("INFO: explicit test control origin:", control)
	probeIP := ""
	if resolveProbe && !check {
		var err error
		probeIP, err = fixedTargetIP(ctx)
		if err != nil {
			return err
		}
		fmt.Println("DIAGNOSTIC: fixed target resolved before core using explicit DNS; system/gateway DNS not verified")
	}
	probe := func(ctx context.Context, port int, auth string) error {
		if probeIP == "" {
			return forward(ctx, port, auth)
		}
		return forwardTargetTLS(ctx, port, auth, "https://"+probeIP+"/", "example.com")
	}
	if dnsOff && !tun {
		return errors.New("DNS_DIAGNOSTIC_REQUIRES_TUN")
	}
	if forced && !tun {
		return errors.New("FORCE_KILL_REQUIRES_TUN")
	}
	if tun && !check {
		if err := requireElevation(); err != nil {
			return err
		}
	}
	if err := verifyAssets(); err != nil {
		return err
	}
	fmt.Println("PASS: locked core and rule SHA256")
	core, _ := filepath.Abs(".local/m0/assets/mihomo-windows-amd64.exe")
	ca, err := os.ReadFile(".local/ca.crt")
	if err != nil {
		return errors.New("CA_READ_FAILED")
	}
	candidate := controlclient.Candidate{Protocol: "hysteria2", Transport: "quic", Host: "192.0.2.1", Port: 4433, Credential: "placeholder-not-valid"}
	candidate.PublicParams.ServerName = "localhost"
	var client *controlclient.Client
	var lease string
	if !check {
		client, err = controlclient.New(control, ".local/ca.crt")
		if err != nil {
			return err
		}
		defer client.Close()
		raw, err := os.ReadFile(accountFile)
		if err != nil {
			return errors.New("S0_ACCOUNT_FILE_NOT_FOUND")
		}
		var account struct{ Email, Password string }
		raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
		if json.Unmarshal(raw, &account) != nil || account.Email == "" || account.Password == "" {
			return errors.New("INVALID_ACCOUNT_FILE")
		}
		if err := client.Login(ctx, account.Email, account.Password); err != nil {
			return err
		}
		account.Password, raw = "", nil
		fmt.Println("PASS: test control HTTPS and S0 login")
		if date := client.ResponseDate(); !date.IsZero() {
			fmt.Printf("INFO: HTTPS Date present; local offset approximately %.0f seconds (no clock correction)\n", time.Since(date).Seconds())
		} else {
			fmt.Println("PENDING: control HTTPS Date missing or invalid")
		}
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return errors.New("KEY_GENERATION_FAILED")
		}
		if err := client.RegisterDevice(ctx, key); err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if lease != "" {
				if err := client.Release(cleanup, lease); err != nil {
					result = errors.Join(result, err)
				} else {
					fmt.Println("PASS: temporary lease released")
				}
			}
			if err := client.DeleteDevice(cleanup); err != nil {
				result = errors.Join(result, err)
			} else {
				fmt.Println("PASS: temporary device removed")
			}
		}()
		id, err := controlclient.RandomID()
		if err != nil {
			return err
		}
		plan, err := client.CreateMode(ctx, id, mode)
		lease = plan.LeaseID
		if err != nil {
			return err
		}
		candidate = plan.Candidates[0]
		if candidate.Host != "192.168.194.128" || candidate.Port != 4433 {
			return errors.New("S0_CANDIDATE_ADDRESS_MISMATCH")
		}
		fmt.Println("PASS: fresh signed lease; test gateway address matches")
	}
	root := os.Getenv("ProgramData")
	if root == "" {
		return errors.New("WINDOWS_PROGRAMDATA_REQUIRED")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return errors.New("INVALID_PRIVATE_ROOT")
	}
	dir, err := os.MkdirTemp(root, "LightflowM0-")
	if err != nil {
		return errors.New("PRIVATE_DIRECTORY_FAILED")
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return errors.New("UNSAFE_CLEANUP_PATH")
	}
	defer func() {
		if os.RemoveAll(dir) != nil {
			result = errors.Join(result, errors.New("PRIVATE_DIRECTORY_CLEANUP_FAILED"))
		}
	}()
	if securefile.RestrictDirectory(dir) != nil {
		return errors.New("PRIVATE_DIRECTORY_ACL_FAILED")
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		data, err := os.ReadFile(filepath.Join(".local/m0/assets", name))
		if err != nil || os.WriteFile(filepath.Join(dir, name), data, 0600) != nil {
			return errors.New("RULE_COPY_FAILED")
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("PORT_ALLOCATION_FAILED")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	auth, err := controlclient.RandomID()
	if err != nil {
		return err
	}
	pipe, env, err := privatePipe()
	if err != nil {
		return err
	}
	if tun {
		filtered := []string{}
		for _, item := range env {
			if !strings.HasPrefix(strings.ToUpper(item), "SKIP_SYSTEM_IPV6_CHECK=") {
				filtered = append(filtered, item)
			}
		}
		env = append(filtered, "SKIP_SYSTEM_IPV6_CHECK=1") // Own core only; intercept/reject even without an IPv6 uplink.
	}
	config, err := configuration(candidate, mode, string(ca), pipe, port, auth)
	if err != nil {
		return err
	}
	device := "LightflowM0-" + auth[:12]
	if tun {
		config, err = tunConfiguration(config, device)
		if err != nil {
			return err
		}
		if dnsOff {
			var diagnostic map[string]any
			json.Unmarshal(config, &diagnostic)
			diagnostic["dns"] = map[string]any{"enable": false}
			diagnostic["tun"].(map[string]any)["dns-hijack"] = []string{}
			config, _ = json.MarshalIndent(diagnostic, "", "  ")
			fmt.Println("DIAGNOSTIC: DNS module disabled for isolation; no DNS protection claim")
		}
		if relaxed {
			var diagnostic map[string]any
			json.Unmarshal(config, &diagnostic)
			diagnostic["tun"].(map[string]any)["strict-route"] = false
			config, _ = json.MarshalIndent(diagnostic, "", "  ")
			fmt.Println("DIAGNOSTIC: strict-route disabled for isolation; no DNS protection claim")
		}
		if osRoute {
			var diagnostic map[string]any
			json.Unmarshal(config, &diagnostic)
			diagnostic["tun"].(map[string]any)["auto-detect-interface"] = false
			config, _ = json.MarshalIndent(diagnostic, "", "  ")
			fmt.Println("DIAGNOSTIC: OS gateway route with explicit TUN exclusion; interface binding disabled")
		}
		if !check && !osRoute {
			config, err = bindGatewayInterface(ctx, dir, device, config)
			if err != nil {
				return err
			}
			fmt.Println("PASS: test gateway outbound bound to its pre-TUN physical route")
		}
		var diagnostics map[string]any
		json.Unmarshal(config, &diagnostics)
		diagnostics["log-level"] = "debug"
		diagnostics["find-process-mode"] = "off"
		config, _ = json.MarshalIndent(diagnostics, "", "  ")
	}
	path := filepath.Join(dir, "config.json")
	// Keep raw core output in memory only, retaining fixed diagnostic categories.
	var diagnosticConfig map[string]any
	json.Unmarshal(config, &diagnosticConfig)
	diagnosticConfig["log-level"] = "debug"
	config, _ = json.MarshalIndent(diagnosticConfig, "", "  ")
	if os.WriteFile(path, config, 0600) != nil {
		return errors.New("CONFIG_WRITE_FAILED")
	}
	validator := exec.CommandContext(ctx, core, "-d", dir, "-f", path, "-t")
	validator.Env = env
	hide(validator)
	if validator.Run() != nil {
		return errors.New("MIHOMO_CONFIG_CHECK_FAILED")
	}
	fmt.Printf("PASS: Mihomo config check (%s, TUN=%t, TLS verification enabled)\n", mode, tun)
	if check {
		return nil
	}
	// v1.19.32 builds outbound TLS pools before applying custom trust certificates.
	// Warm up trust with no outbound and a REJECT rule, then reload through our pipe.
	var bootstrap map[string]any
	json.Unmarshal(config, &bootstrap)
	bootstrap["socks-port"], bootstrap["proxies"], bootstrap["rules"] = 0, []any{}, []string{"MATCH,REJECT"}
	bootstrap["tun"], bootstrap["dns"] = map[string]any{"enable": false}, map[string]any{"enable": false}
	bootstrapPath := filepath.Join(dir, "bootstrap.json")
	bootstrapBytes, _ := json.Marshal(bootstrap)
	if os.WriteFile(bootstrapPath, bootstrapBytes, 0600) != nil {
		return errors.New("BOOTSTRAP_WRITE_FAILED")
	}
	process := exec.CommandContext(ctx, core, "-d", dir, "-f", bootstrapPath)
	process.Env = env
	events := &safeEvents{}
	process.Stdout, process.Stderr = events, events
	hide(process) // Only allowlisted categories retained; never persist raw logs.
	if process.Start() != nil {
		return errors.New("MIHOMO_START_FAILED")
	}
	defer func() { process.Process.Kill(); process.Wait() }()
	if tun {
		closeJob, err := ownCoreJob(process)
		if err != nil {
			return err
		}
		defer closeJob()
	}
	pipeDeadline := time.Now().Add(5 * time.Second)
	var pipeErr error
	for time.Now().Before(pipeDeadline) {
		pipeErr = verifyPipe(ctx, pipe)
		if pipeErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if pipeErr != nil {
		return pipeErr
	}
	fmt.Println("PASS: running core pipe ACL and local API checked")
	var stopExperiment func() error
	if tun {
		stopExperiment, err = beginExperiment(ctx, dir, device)
		if err != nil {
			return err
		}
		defer func() {
			cleanupStart := time.Now()
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if !forced {
				if err := reloadPipe(cleanup, pipe, bootstrapPath); err != nil {
					result = errors.Join(result, err)
				}
			}
			process.Process.Kill()
			process.Wait()
			if err := stopExperiment(); err != nil {
				result = errors.Join(result, err)
			}
			fmt.Printf("INFO: core stop, recovery inspection and evidence cleanup took %d ms\n", time.Since(cleanupStart).Milliseconds())
		}()
	}
	if err := reloadPipe(ctx, pipe, path); err != nil {
		return err
	}
	if err := verifyPipe(ctx, pipe); err != nil {
		return err
	}
	fmt.Println("PASS: fail-closed CA bootstrap and protected-pipe config reload")
	if tun {
		if err := verifyCoreTUN(ctx, pipe, device, port); err != nil {
			return err
		}
		fmt.Println("PASS: core API confirms intended TUN configuration")
		if deadline, ok := ctx.Deadline(); ok {
			fmt.Printf("INFO: probe budget remaining %d ms\n", time.Until(deadline).Milliseconds())
		}
		if err := proveTUN(ctx, dir, device); err != nil {
			// A SOCKS control is diagnostic; it must not prevent the actual TUN test.
			return errors.Join(err, probe(ctx, port, auth), events.failure())
		}
		fmt.Println("PASS: selected IPv4 route uses owned TUN; HTTPS without application proxy passed")
		probe = func(ctx context.Context, _ int, _ string) error { return proveTUN(ctx, dir, device) }
	}
	deadline := time.Now().Add(30 * time.Second)
	var probeErr error
	for time.Now().Before(deadline) && ctx.Err() == nil {
		probeErr = probe(ctx, port, auth)
		if probeErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(500 * time.Millisecond):
		}
	}
	if probeErr != nil {
		return errors.Join(probeErr, events.failure())
	}
	if tun {
		fmt.Println("PASS: fixed public HTTPS response reverified through owned TUN")
	} else {
		fmt.Println("PASS: fixed public HTTPS response verified through authenticated Mihomo SOCKS (no direct fallback)")
	}
	if err := client.Activate(ctx, lease); err != nil {
		return err
	}
	fmt.Println("PASS: lease activated after real forwarding")
	if _, err := client.Renew(ctx, lease); err != nil {
		return err
	}
	if err := probe(ctx, port, auth); err != nil {
		return err
	}
	fmt.Println("PASS: immediate renewal and subsequent forwarding (scheduled renewal not yet tested)")
	return nil
}

type safeEvents struct {
	mu                sync.Mutex
	code              string
	phase             string
	tunnelEstablished bool
}

func (s *safeEvents) Write(data []byte) (int, error) {
	text := strings.ToLower(string(data))
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.Contains(text, "[dns]") {
		s.phase = "DNS"
	}
	if strings.Contains(text, "[tcp]") {
		s.phase = "TCP"
		if strings.Contains(text, "using lf-tunnel") {
			s.tunnelEstablished = true
		}
	}
	if strings.Contains(text, "[metadata") {
		s.phase = "METADATA"
	}
	if !strings.Contains(text, "error") && !strings.Contains(text, "fail") && !strings.Contains(text, "timeout") && !strings.Contains(text, "x509") {
		return len(data), nil
	}
	switch {
	case strings.Contains(text, "unknown authority"):
		s.code = "CORE_CA_UNTRUSTED"
	case strings.Contains(text, "certificate is valid for"), strings.Contains(text, "cannot validate certificate"):
		s.code = "CORE_CERT_NAME_MISMATCH"
	case strings.Contains(text, "certificate has expired"):
		s.code = "CORE_CERT_TIME_INVALID"
	case strings.Contains(text, "x509"), strings.Contains(text, "certificate"):
		s.code = "CORE_TLS_VALIDATION_FAILED"
	case strings.Contains(text, "authentication"), strings.Contains(text, "unauthorized"):
		s.code = "CORE_AUTHENTICATION_FAILED"
	case strings.Contains(text, "no such host"), strings.Contains(text, "dns resolve failed"):
		s.code = "CORE_DNS_FAILED"
	case strings.Contains(text, "timeout"), strings.Contains(text, "no recent network activity"):
		if s.code == "" {
			s.code = "CORE_NETWORK_TIMEOUT"
		}
	}
	return len(data), nil
}
func (s *safeEvents) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.code == "" {
		if s.tunnelEstablished {
			return errors.New("CORE_TCP_TUNNEL_ESTABLISHED_TARGET_NOT_VERIFIED")
		}
		return errors.New("CORE_FAILURE_UNCLASSIFIED_PHASE_" + s.phase)
	}
	return errors.New(s.code)
}

func forward(ctx context.Context, port int, auth string) error {
	return forwardTarget(ctx, port, auth, "https://example.com/")
}

func forwardTarget(ctx context.Context, port int, auth, target string) error {
	return forwardTargetTLS(ctx, port, auth, target, "")
}

func forwardTargetTLS(ctx context.Context, port int, auth, target, serverName string) error {
	proxy := &url.URL{Scheme: "socks5", Host: net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), User: url.UserPassword("m0", auth)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, TLSClientConfig: &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REDIRECT_REJECTED") }}
	request, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
	if serverName != "" {
		request.Host = serverName
	}
	var stage atomic.Value
	stage.Store("CONNECT")
	trace := &httptrace.ClientTrace{
		TLSHandshakeStart: func() { stage.Store("TLS_HANDSHAKE") },
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				stage.Store("HTTP_RESPONSE")
			}
		},
	}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	response, err := client.Do(request)
	if err != nil {
		// Only fixed phases/categories escape this function, never URL/error text.
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return errors.New("SOCKS_TARGET_TIMEOUT_PHASE_" + stage.Load().(string))
		}
		lower := strings.ToLower(err.Error())
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errors.New("SOCKS_TARGET_EOF_PHASE_" + stage.Load().(string))
		}
		if strings.Contains(lower, "connection reset") || strings.Contains(lower, "forcibly closed") {
			return errors.New("SOCKS_TARGET_RESET")
		}
		if strings.Contains(lower, "connection refused") || strings.Contains(lower, "actively refused") {
			return errors.New("SOCKS_LISTENER_NOT_READY")
		}
		if strings.Contains(lower, "authentication failed") || strings.Contains(lower, "username/password") {
			return errors.New("SOCKS_AUTHENTICATION_FAILED")
		}
		if strings.Contains(lower, "unknown authority") || strings.Contains(lower, "certificate") {
			return errors.New("SOCKS_TARGET_TLS_VALIDATION_FAILED")
		}
		if strings.Contains(lower, "socks") {
			return errors.New("SOCKS_FORWARDING_FAILED")
		}
		return errors.New("MIHOMO_HTTPS_FORWARDING_FAILED")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), "Example Domain") {
		return errors.New("PROBE_RESPONSE_INVALID")
	}
	return nil
}

// Only the developer-controlled fixed target; no user browsing DNS is queried here.
func fixedTargetIP(ctx context.Context) (string, error) {
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, "1.1.1.1:53")
	}}
	addresses, err := resolver.LookupIP(ctx, "ip4", "example.com")
	if err != nil || len(addresses) == 0 {
		return "", errors.New("FIXED_TARGET_EXPLICIT_DNS_FAILED")
	}
	return addresses[0].String(), nil
}
