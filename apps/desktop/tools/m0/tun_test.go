package main

import (
	"encoding/json"
	"testing"

	"nimbus/internal/controlclient"
)

func TestTUNInterceptsIPv6AndPreservesMandatoryTunnelRule(t *testing.T) {
	t.Chdir("../../../..")
	candidate := controlclient.Candidate{Protocol: "hysteria2", Transport: "quic", Host: "192.0.2.1", Port: 4433, Credential: "test-only"}
	candidate.PublicParams.ServerName = "gateway.example"
	raw, err := configuration(candidate, "global", "", `\\.\pipe\test`, 12345, "test-auth")
	if err != nil {
		t.Fatal(err)
	}
	merged, err := tunConfiguration(raw, "LightflowM0-012345abcdef")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	json.Unmarshal(merged, &config)
	tun := config["tun"].(map[string]any)
	if tun["enable"] != true || tun["strict-route"] != true || tun["device"] != "LightflowM0-012345abcdef" || config["ipv6"] != true {
		t.Fatal("TUN interception not explicit")
	}
	rules := config["rules"].([]any)
	rejectsV6 := false
	for _, rule := range rules {
		if rule == "IP-CIDR6,::/0,REJECT,no-resolve" {
			rejectsV6 = true
		}
	}
	if !rejectsV6 || rules[len(rules)-1] != "MATCH,LF-TUNNEL" {
		t.Fatal("IPv6 or unmatched traffic bypasses selected policy")
	}
}
