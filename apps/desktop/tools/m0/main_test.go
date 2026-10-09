package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"nimbus/internal/controlclient"
)

func TestBaselineHasNoImplicitDirectFallbackOrTLSBypass(t *testing.T) {
	t.Chdir("../../../..")
	candidate := controlclient.Candidate{Protocol: "hysteria2", Transport: "quic", Host: "192.0.2.1", Port: 4433, Credential: "test-only"}
	candidate.PublicParams.ServerName = "localhost"
	for _, mode := range []string{"global", "smart"} {
		raw, err := configuration(candidate, mode, "test-ca", `\\.\pipe\test`, 12345, "test-auth")
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		json.Unmarshal(raw, &config)
		proxies := config["proxies"].([]any)
		if proxies[0].(map[string]any)["skip-cert-verify"] != false || config["external-controller"] != "" || config["allow-lan"] != false || config["tun"].(map[string]any)["enable"] != false {
			t.Fatal("unsafe baseline")
		}
		rules := config["rules"].([]any)
		if rules[len(rules)-1] != "MATCH,LF-TUNNEL" {
			t.Fatal("unmatched traffic could bypass tunnel")
		}
	}
	candidate.Host = "127.0.0.1"
	if _, err := configuration(candidate, "global", "ca", "pipe", 12345, "auth"); err == nil {
		t.Fatal("remote candidate silently remapped")
	}
}

func TestForwardDoesNotFallBackWhenProxyUnavailable(t *testing.T) {
	// A local origin traps any accidental direct Dial attempt.
	direct := false
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct = true }))
	defer origin.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	if err := forwardTarget(context.Background(), port, "test-only", origin.URL); err == nil || direct {
		t.Fatal("unavailable proxy must fail")
	}
}

func TestSafeCoreSinkNeverReturnsRawLog(t *testing.T) {
	sink := &safeEvents{}
	sink.Write([]byte("x509: certificate failure password=secret url=https://private.example/"))
	if sink.failure().Error() != "CORE_TLS_VALIDATION_FAILED" {
		t.Fatal("unsafe diagnostic")
	}
}

func TestCoreTCPStageDoesNotClaimTargetVerified(t *testing.T) {
	sink := &safeEvents{}
	sink.Write([]byte("[TCP] private-source --> private-target match Match using LF-TUNNEL"))
	if sink.failure().Error() != "CORE_TCP_TUNNEL_ESTABLISHED_TARGET_NOT_VERIFIED" {
		t.Fatal("core TCP selection must not count as verified target forwarding")
	}
}
