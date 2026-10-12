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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nimbus/internal/client/sessionstore"
	"nimbus/internal/controlclient"
	"nimbus/internal/securefile"
)

func main() {
	account := flag.String("account", ".local/windows-s0-account.json", "S0 lab account file")
	control := flag.String("control", "https://lightflow.u26d.local:8443", "explicit S0 control origin; no automatic fallback")
	insecureControl := flag.Bool("insecure-test-control", false, "explicit S0 HTTPS certificate bypass; never production")
	mode := flag.String("mode", "global", "global or smart")
	check := flag.Bool("check", false, "only validate locked assets and placeholder config; no API login")
	tun := flag.Bool("tun", false, "explicit isolated-machine TUN experiment; requires elevation")
	forced := flag.Bool("force-kill", false, "TUN experiment: kill owned core instead of disabling TUN normally")
	dnsOff := flag.Bool("tun-dns-off", false, "diagnostic only: isolate TUN forwarding with DNS module disabled")
	resolveProbe := flag.Bool("resolve-probe", false, "diagnostic only: resolve fixed target via explicit DNS before core startup")
	relaxed := flag.Bool("tun-relaxed", false, "diagnostic only: disable strict-route, no DNS protection claim")
	osRoute := flag.Bool("tun-os-route", false, "diagnostic only: use OS gateway route instead of interface binding")
	quicCompat := flag.Bool("quic-compat", false, "diagnostic only: disable QUIC GSO and ECN in owned core")
	vmwareBypass := flag.Bool("lab-vmware-nat-bypass", false, "S0 host-only experiment: vmnat.exe DIRECT to prevent guest egress re-entering its gateway")
	domainProbe := flag.Bool("probe-domain", false, "TUN experiment: require system fake-IP DNS and domain HTTPS forwarding")
	production := flag.Bool("production", false, "explicit production experiment with system certificate trust; requires explicit control and account")
	egress := flag.Bool("probe-egress", false, "compare pre-TUN and real TUN public IPv4 using one fixed HTTPS probe")
	desktop := flag.Bool("desktop-session", false, "service-owned persistent desktop connection")
	sessionFile := flag.String("session-file", "", "service-private DPAPI session")
	stopFile := flag.String("stop-file", "", "service-owned cancellation file")
	country := flag.String("country", "", "selected country; empty means server quick connect")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var cancel context.CancelFunc
	if *desktop {
		if !*production || !*tun || *sessionFile == "" || *stopFile == "" {
			fmt.Fprintln(os.Stderr, "FAIL: INVALID_DESKTOP_SESSION")
			os.Exit(1)
		}
		ctx, cancel = context.WithCancel(ctx)
		ctx = context.WithValue(ctx, desktopKey{}, desktopOptions{*sessionFile, *stopFile, *country})
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := os.Stat(*stopFile); err == nil {
						cancel()
						return
					}
				}
			}
		}()
	} else {
		ctx, cancel = context.WithTimeout(ctx, 100*time.Second)
	}
	defer cancel()
	if err := run(ctx, *control, *account, *mode, *check, *tun, *forced, *dnsOff, *resolveProbe, *relaxed, *osRoute, *insecureControl, *quicCompat, *vmwareBypass, *domainProbe, *production, *egress); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		var api *controlclient.APIError
		if errors.As(err, &api) && api.RequestID != "" {
			fmt.Fprintln(os.Stderr, "X-Request-ID:", api.RequestID)
		}
		os.Exit(1)
	}
	if *desktop {
		fmt.Println("DESKTOP_STOPPED_CLEAN")
	}
}

type desktopKey struct{}
type desktopOptions struct{ SessionFile, StopFile, Country string }

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
	if ca != "" {
		config["tls"] = map[string]any{"custom-certifactes": []string{ca}} // Fixed upstream spelling in v1.19.32.
	}
	config["proxies"] = []any{map[string]any{"name": "LF-TUNNEL", "type": "hysteria2", "server": candidate.Host, "port": candidate.Port, "password": candidate.Credential, "sni": candidate.PublicParams.ServerName, "skip-cert-verify": false, "udp": true}}
	if mode == "smart" {
		config["rules"] = []string{"GEOSITE,cn,DIRECT", "GEOIP,cn,DIRECT,no-resolve", "MATCH,LF-TUNNEL"}
	}
	// Global also uses rule mode: its sole MATCH rule forces the selected tunnel.
	return json.MarshalIndent(config, "", "  ")
}

func run(ctx context.Context, control, accountFile, mode string, check, tun, forced, dnsOff, resolveProbe, relaxed, osRoute, insecureControl, quicCompat, vmwareBypass, domainProbe, production, egress bool) (result error) {
	desktop, persistent := ctx.Value(desktopKey{}).(desktopOptions)
	origin, parseErr := url.Parse(control)
	if parseErr != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return errors.New("INVALID_CONTROL_URL")
	}
	if production && (insecureControl || vmwareBypass || dnsOff || relaxed || osRoute || accountFile == ".local/windows-s0-account.json") {
		return errors.New("PRODUCTION_REJECTS_TEST_EXCEPTIONS")
	}
	if !production && control != "https://lightflow.u26d.local:8443" && control != "https://192.168.194.128:8443" {
		return errors.New("S0_CONTROL_ORIGIN_NOT_ALLOWED")
	}
	fmt.Println("INFO: explicit control origin:", control)
	var directIP string
	if egress && (!tun || dnsOff) {
		return errors.New("EGRESS_PROBE_REQUIRES_TUN_DNS")
	}
	if egress && !check {
		var err error
		directIP, err = publicIPv4(ctx)
		if err != nil {
			return err
		}
		fmt.Println("INFO: pre-TUN direct public IPv4:", directIP, "(baseline only)")
	}
	if domainProbe && (!tun || dnsOff) {
		return errors.New("DOMAIN_PROBE_REQUIRES_TUN_DNS")
	}
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
	var ca []byte
	var err error
	if !production {
		ca, err = os.ReadFile(".local/ca.crt")
		if err != nil {
			return errors.New("CA_READ_FAILED")
		}
	}
	candidate := controlclient.Candidate{Protocol: "hysteria2", Transport: "quic", Host: "192.0.2.1", Port: 4433, Credential: "placeholder-not-valid"}
	candidate.PublicParams.ServerName = "localhost"
	var client *controlclient.Client
	var lease string
	var lastPlan controlclient.Plan
	if !check {
		if insecureControl {
			fmt.Println("TEST ONLY: control HTTPS certificate verification disabled by explicit user option; gateway/target verification remains enabled")
			client, err = controlclient.NewInsecureS0Test(control)
		} else if production {
			client, err = controlclient.New(control, "")
		} else {
			client, err = controlclient.New(control, ".local/ca.crt")
		}
		if err != nil {
			return err
		}
		defer client.Close()
		if persistent {
			session, loadErr := sessionstore.Load(desktop.SessionFile)
			if loadErr != nil || session.Control != control {
				return errors.New("INVALID_SAVED_SESSION")
			}
			client.Close()
			client, err = controlclient.Restore(session)
			if err != nil {
				return err
			}
			defer client.Close()
			client.SaveSession = func(s controlclient.Session) error { return sessionstore.Save(desktop.SessionFile, s) }
			if _, err := os.Stat(desktop.SessionFile + ".lease"); err == nil {
				return errors.New("PREVIOUS_LEASE_UNRESOLVED")
			}
		} else {
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
			fmt.Println("PASS: control HTTPS and account login")
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
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if lease != "" {
				if err := client.Release(cleanup, lease); err != nil {
					result = errors.Join(result, err)
				} else {
					fmt.Println("PASS: temporary lease released")
					if persistent {
						if err := os.Remove(desktop.SessionFile + ".lease"); err != nil && !os.IsNotExist(err) {
							result = errors.Join(result, errors.New("LEASE_MARKER_CLEANUP_FAILED"))
						}
					}
				}
			}
			if persistent {
				fmt.Println("INFO: persistent desktop device retained")
				return
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
		if persistent {
			if err := os.WriteFile(desktop.SessionFile+".lease", []byte(id), 0600); err != nil {
				return errors.New("LEASE_MARKER_WRITE_FAILED")
			}
		}
		var plan controlclient.Plan
		if persistent {
			plan, err = client.CreateCountryMode(ctx, id, desktop.Country, mode)
		} else {
			plan, err = client.CreateMode(ctx, id, mode)
		}
		lease = plan.LeaseID
		if err != nil {
			var api *controlclient.APIError
			if persistent && lease == "" && errors.As(err, &api) && api.Status >= 400 && api.Status < 500 {
				os.Remove(desktop.SessionFile + ".lease")
			}
			return err
		}
		lastPlan = plan
		if persistent {
			fmt.Println("DESKTOP_COUNTRY=" + plan.CountryCode)
		}
		candidate = plan.Candidates[0]
		if !production && (candidate.Host != "192.168.194.128" || candidate.Port != 4433) {
			return errors.New("S0_CANDIDATE_ADDRESS_MISMATCH")
		}
		if production {
			addresses, lookupErr := net.DefaultResolver.LookupIP(ctx, "ip4", candidate.Host)
			if lookupErr != nil || len(addresses) == 0 || !addresses[0].IsGlobalUnicast() || addresses[0].IsPrivate() {
				return errors.New("PRODUCTION_GATEWAY_IPV4_UNAVAILABLE")
			}
			candidate.Host = addresses[0].String() // Pin pre-TUN resolution, preserving candidate TLS SNI.
		}
		fmt.Println("PASS: fresh signed lease; gateway endpoint validated")
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
	endpoint, _ := json.Marshal(struct {
		Host string
		Port int
	}{candidate.Host, candidate.Port})
	if os.WriteFile(filepath.Join(dir, "gateway-endpoint.json"), endpoint, 0600) != nil {
		return errors.New("GATEWAY_ENDPOINT_WRITE_FAILED")
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
	if quicCompat {
		filtered := []string{}
		for _, item := range env {
			upper := strings.ToUpper(item)
			if !strings.HasPrefix(upper, "QUIC_GO_DISABLE_GSO=") && !strings.HasPrefix(upper, "QUIC_GO_DISABLE_ECN=") {
				filtered = append(filtered, item)
			}
		}
		env = append(filtered, "QUIC_GO_DISABLE_GSO=1", "QUIC_GO_DISABLE_ECN=1")
		fmt.Println("DIAGNOSTIC: QUIC GSO/ECN disabled in owned core only")
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
		if production {
			var parsed map[string]any
			json.Unmarshal(config, &parsed)
			parsed["rules"] = append([]any{"DOMAIN," + origin.Hostname() + ",DIRECT"}, parsed["rules"].([]any)...)
			config, _ = json.Marshal(parsed)
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
			fmt.Println("PASS: gateway outbound bound to its pre-TUN physical route")
		}
		var diagnostics map[string]any
		json.Unmarshal(config, &diagnostics)
		diagnostics["log-level"] = "debug"
		diagnostics["find-process-mode"] = "off"
		if vmwareBypass {
			diagnostics["find-process-mode"] = "always"
			diagnostics["rules"] = append([]any{"PROCESS-NAME,vmnat.exe,DIRECT"}, diagnostics["rules"].([]any)...)
			fmt.Println("TEST TOPOLOGY ONLY: VMware NAT egress DIRECT exception enabled; guest traffic is outside VPN protection scope")
		}
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
	coreContext := ctx
	if persistent {
		coreContext = context.WithoutCancel(ctx)
	} // Graceful disconnect owns teardown; Job still handles service crashes.
	process := exec.CommandContext(coreContext, core, "-d", dir, "-f", bootstrapPath)
	process.Env = env
	events := &safeEvents{targetIP: probeIP}
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
		probeDeadline := time.Now().Add(30 * time.Second)
		var tunErr error
		for time.Now().Before(probeDeadline) && ctx.Err() == nil {
			tunErr = proveTUN(ctx, dir, device)
			if tunErr == nil {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("PROBE_CANCELLED")
			case <-time.After(300 * time.Millisecond):
			}
		}
		if tunErr != nil {
			// A SOCKS control is diagnostic; it must not count as a TUN success.
			socksErr := probe(ctx, port, auth)
			if socksErr == nil {
				fmt.Println("PASS: diagnostic SOCKS control forwards while TUN probe fails; TUN NOT verified")
			}
			return errors.Join(tunErr, socksErr, events.failure())
		}
		fmt.Println("PASS: selected IPv4 route uses owned TUN; HTTPS without application proxy passed")
		probe = func(ctx context.Context, _ int, _ string) error { return proveTUN(ctx, dir, device) }
		if domainProbe {
			if err := proveDomainTUN(ctx, dir, device); err != nil {
				return err
			}
			fmt.Println("PASS: system fake-IP DNS and domain HTTPS without application proxy verified")
			probe = func(ctx context.Context, _ int, _ string) error { return proveDomainTUN(ctx, dir, device) }
		}
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
	if egress {
		tunnelIP, err := proveEgressTUN(ctx, dir, device)
		if err != nil {
			return err
		}
		if tunnelIP == directIP {
			return errors.New("EGRESS_IP_UNCHANGED")
		}
		fmt.Println("PASS: real TUN public IPv4:", tunnelIP, "; differs from direct baseline")
	}
	if lastPlan, err = client.Renew(ctx, lease); err != nil {
		return err
	}
	if err := probe(ctx, port, auth); err != nil {
		return err
	}
	fmt.Println("PASS: immediate renewal and subsequent forwarding (scheduled renewal not yet tested)")
	if persistent {
		fmt.Println("DESKTOP_CONNECTED")
		return holdDesktop(ctx, client, lease, lastPlan, func(ctx context.Context) error {
			if err := verifyPipe(ctx, pipe); err != nil {
				return err
			}
			return probe(ctx, port, auth)
		})
	}
	return nil
}

func holdDesktop(ctx context.Context, client *controlclient.Client, lease string, plan controlclient.Plan, probe func(context.Context) error) error {
	for {
		remaining := time.Until(plan.ExpiresAt)
		if remaining <= 5*time.Second {
			return errors.New("LEASE_EXPIRED")
		}
		delay := time.Duration(plan.RenewAfterSeconds) * time.Second
		if delay < time.Second || delay > remaining-5*time.Second {
			delay = remaining / 2
		}
		timer := time.NewTimer(delay)
		check := time.NewTicker(30 * time.Second)
		var renew bool
		for !renew {
			select {
			case <-ctx.Done():
				timer.Stop()
				check.Stop()
				return nil
			case <-check.C:
				pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				err := probe(pctx)
				cancel()
				if err != nil {
					timer.Stop()
					check.Stop()
					return errors.New("DESKTOP_FORWARDING_LOST")
				}
			case <-timer.C:
				renew = true
			}
		}
		check.Stop()
		rctx, cancel := context.WithTimeout(ctx, min(25*time.Second, time.Until(plan.ExpiresAt)-time.Second))
		next, err := client.Renew(rctx, lease)
		cancel()
		if err != nil {
			return err
		}
		if next.LeaseID != lease || len(next.Candidates) == 0 || next.Candidates[0].Credential != plan.Candidates[0].Credential {
			return errors.New("RENEWED_CREDENTIAL_CHANGED")
		}
		plan = next
		fmt.Println("DESKTOP_RENEWED")
	}
}

// This fixed diagnostic retains only an IPv4, never request or browsing logs.
func publicIPv4(ctx context.Context) (string, error) {
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REDIRECT_NOT_ALLOWED") }}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("EGRESS_HTTPS_PROBE_FAILED")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 128))
	ip := net.ParseIP(strings.TrimSpace(string(raw)))
	if err != nil || response.StatusCode != http.StatusOK || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return "", errors.New("EGRESS_RESPONSE_INVALID")
	}
	return ip.String(), nil
}

type safeEvents struct {
	mu                sync.Mutex
	code              string
	phase             string
	tunnelEstablished bool
	targetIP          string
	targetDecision    string
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
		if s.targetIP != "" && strings.Contains(text, s.targetIP+":443") {
			for _, decision := range []string{"LF-TUNNEL", "REJECT", "DIRECT"} {
				if strings.Contains(text, "using "+strings.ToLower(decision)) {
					s.targetDecision = decision
				}
			}
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
		if s.targetDecision != "" {
			return errors.New("CORE_FIXED_TARGET_RULE_" + s.targetDecision + "_TARGET_NOT_VERIFIED")
		}
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
	var candidates []string
	for _, address := range addresses {
		if address.To4() != nil && address.IsGlobalUnicast() && !address.IsPrivate() {
			candidates = append(candidates, address.String())
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("FIXED_TARGET_NOT_PUBLIC_IPV4")
	}
	sort.Strings(candidates) // Make sequential controls comparable despite DNS answer ordering.
	return candidates[0], nil
}
