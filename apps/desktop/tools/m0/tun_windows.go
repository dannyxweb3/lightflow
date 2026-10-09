//go:build windows

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"nimbus/internal/securefile"
)

func ownCoreJob(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, errors.New("CORE_JOB_CREATE_FAILED")
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, errors.New("CORE_JOB_SETUP_FAILED")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, errors.New("CORE_JOB_PROCESS_FAILED")
	}
	defer windows.CloseHandle(process)
	if windows.AssignProcessToJobObject(job, process) != nil {
		windows.CloseHandle(job)
		return nil, errors.New("CORE_JOB_ASSIGN_FAILED")
	}
	return func() { windows.CloseHandle(job) }, nil
}

func requireElevation() error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return errors.New("TOKEN_READ_FAILED")
	}
	defer token.Close()
	if !token.IsElevated() {
		return errors.New("TUN_REQUIRES_ADMINISTRATOR")
	}
	return nil
}

func bindGatewayInterface(ctx context.Context, dir, device string, config []byte) ([]byte, error) {
	if err := networkCheck(ctx, "Gateway", dir, device, ""); err != nil {
		return nil, err
	}
	name, err := os.ReadFile(filepath.Join(dir, "gateway-interface.txt"))
	if err != nil || len(name) == 0 || len(name) > 256 || strings.ContainsAny(string(name), "\r\n\x00") {
		return nil, errors.New("GATEWAY_INTERFACE_UNAVAILABLE")
	}
	var parsed map[string]any
	if json.Unmarshal(config, &parsed) != nil {
		return nil, errors.New("INVALID_TUN_TEMPLATE")
	}
	parsed["proxies"].([]any)[0].(map[string]any)["interface-name"] = string(name)
	parsed["proxies"].([]any)[0].(map[string]any)["ip-version"] = "ipv4" // S0 gateway is an IPv4 literal.
	return json.MarshalIndent(parsed, "", "  ")
}

// All captures are bounded to a fixed public test address and the test gateway.
// Do not touch another PktMon session or its filters.
func beginExperiment(ctx context.Context, dir, device string) (func() error, error) {
	if err := networkCheck(ctx, "Before", dir, device, ""); err != nil {
		return nil, err
	}
	target, err := fixedTargetIP(ctx)
	if err != nil {
		return nil, errors.New("FIXED_TARGET_DNS_FAILED")
	}
	if os.WriteFile(filepath.Join(dir, "fixed-target-ip.txt"), []byte(target), 0600) != nil {
		return nil, errors.New("TARGET_WRITE_FAILED")
	}
	// Localized status is inspected conservatively. Unknown output refuses capture.
	status, _ := packetCommand(ctx, "status") // PktMon can return nonzero when no session is running.
	if !strings.Contains(status, "Stopped") && !strings.Contains(status, "没有运行") {
		return nil, errors.New("PKTMON_SESSION_NOT_CONFIRMED_IDLE")
	}
	filters, _ := packetCommand(ctx, "filter", "list")
	if !strings.Contains(filters, "No filters") && strings.Join(strings.Fields(filters), " ") != "数据包筛选器: 无" {
		return nil, errors.New("PKTMON_EXISTING_FILTERS_OR_UNKNOWN_STATE")
	}
	names := []string{device + "-gw", device + "-target"}
	cleanupFilters := func(cleanup context.Context) {
		for _, name := range names {
			packetCommand(cleanup, "filter", "remove", name)
		}
	}
	if _, err := packetCommand(ctx, "filter", "add", names[0], "-i", "192.168.194.128", "-t", "UDP", "-p", "4433"); err != nil {
		return nil, errors.New("CAPTURE_FILTER_FAILED")
	}
	if _, err := packetCommand(ctx, "filter", "add", names[1], "-i", target, "-t", "TCP", "-p", "443"); err != nil {
		cleanupFilters(ctx)
		return nil, errors.New("CAPTURE_FILTER_FAILED")
	}
	etl := filepath.Join(dir, "packets.etl")
	if _, err := packetCommand(ctx, "start", "--capture", "--comp", "all", "--flags", "0x012", "--pkt-size", "128", "--file-size", "16", "--file-name", etl); err != nil {
		cleanupFilters(ctx)
		return nil, errors.New("CAPTURE_START_FAILED")
	}
	fmt.Println("PASS: network baseline recorded; fixed-target-only PktMon capture started")
	// Before TUN activation, calibrate capture with one fixed-target TCP handshake.
	// It is marked separately and is never evidence of VPN forwarding.
	calibration, calibrationErr := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort(target, "443"))
	if calibrationErr == nil {
		calibration.Close()
		fmt.Println("INFO: pre-TUN direct TCP capture calibration completed; not VPN evidence")
	} else {
		fmt.Println("INFO: pre-TUN direct TCP capture calibration failed; not VPN evidence")
	}
	return func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var failures []error
		if _, err := packetCommand(cleanup, "stop"); err != nil {
			failures = append(failures, errors.New("CAPTURE_STOP_FAILED"))
		}
		cleanupFilters(cleanup)
		recovery := networkCheck(cleanup, "After", dir, device, "")
		if recovery != nil {
			failures = append(failures, recovery)
		}
		// Preserve only our controlled experiment's evidence, never a core config.
		evidence, _ := filepath.Abs(filepath.Join(".local/m0/evidence", device))
		if os.MkdirAll(evidence, 0700) != nil || securefile.RestrictDirectory(evidence) != nil {
			failures = append(failures, errors.New("EVIDENCE_ACL_FAILED"))
		} else {
			for _, name := range []string{"packets.etl", "network-before.json", "network-after.json", "network-error-id.txt"} {
				if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
					if os.WriteFile(filepath.Join(evidence, name), data, 0600) != nil {
						failures = append(failures, errors.New("EVIDENCE_WRITE_FAILED"))
					}
				}
			}
			pcap := filepath.Join(evidence, "packets.pcapng")
			if _, err := packetCommand(cleanup, "etl2pcap", filepath.Join(evidence, "packets.etl"), "--out", pcap); err != nil {
				failures = append(failures, errors.New("CAPTURE_CONVERSION_FAILED"))
			}
			fmt.Println("INFO: controlled capture and network evidence saved in ignored .local/m0/evidence/" + device)
		}
		return errors.Join(failures...)
	}, nil
}

func packetCommand(ctx context.Context, args ...string) (string, error) {
	script, _ := filepath.Abs("apps/desktop/tools/m0/packet.ps1")
	raw, _ := json.Marshal(args)
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-EncodedArguments", base64.StdEncoding.EncodeToString(raw))
	hide(cmd)
	data, err := cmd.CombinedOutput()
	return string(data), err // Never emit localized raw diagnostics.
}

func networkCheck(ctx context.Context, operation, dir, device, target string) error {
	script, _ := filepath.Abs("apps/desktop/tools/m0/network.ps1")
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script, "-Operation", operation, "-Directory", dir, "-Device", device}
	if target != "" {
		args = append(args, "-TargetIP", target)
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", args...)
	hide(cmd)
	data, err := cmd.Output()
	code := strings.TrimSpace(string(data))
	if err != nil {
		switch code {
		case "OTHER_VPN_ACTIVE", "TUN_ADDRESS_CONFLICT", "TUN_ADAPTER_NOT_CREATED", "TUN_ROUTE_NOT_SELECTED", "IPV6_ROUTE_NOT_INTERCEPTED", "NETWORK_RECOVERY_MISMATCH":
			return errors.New(code)
		}
		return errors.New("NETWORK_INSPECTION_FAILED")
	}
	if strings.HasPrefix(code, "NETWORK_CHECK_MS=") {
		fmt.Println("PASS:", code, "; original interfaces, routes and DNS match baseline")
	}
	return nil
}

func proveTUN(ctx context.Context, dir, device string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "fixed-target-ip.txt"))
	if err != nil {
		return errors.New("TARGET_READ_FAILED")
	}
	target := string(raw)
	// A successful plain Dial alone is never evidence: verify OS-selected routes first.
	deadline := time.Now().Add(15 * time.Second)
	var routeErr error
	for time.Now().Before(deadline) {
		routeErr = networkCheck(ctx, "Route", dir, device, target)
		if routeErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("PROBE_CANCELLED")
		case <-time.After(300 * time.Millisecond):
		}
	}
	if routeErr != nil {
		return routeErr
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(target, "443"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("REDIRECT_REJECTED") }}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://example.com/", nil)
	response, err := client.Do(request)
	if err != nil {
		var unknown x509.UnknownAuthorityError
		var hostname x509.HostnameError
		if errors.As(err, &unknown) || errors.As(err, &hostname) {
			return errors.New("TUN_TARGET_TLS_VALIDATION_FAILED")
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return errors.New("TUN_TARGET_TIMEOUT")
		}
		if strings.Contains(strings.ToLower(err.Error()), "connection reset") {
			return errors.New("TUN_TARGET_RESET")
		}
		return errors.New("TUN_HTTPS_FORWARDING_FAILED")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), "Example Domain") {
		return errors.New("TUN_RESPONSE_INVALID")
	}
	return nil
}
