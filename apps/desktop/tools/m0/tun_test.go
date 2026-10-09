package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTUNInterceptsIPv6AndPreservesMandatoryTunnelRule(t *testing.T) {
	t.Chdir("../../../..")
	raw, err := os.ReadFile("apps/desktop/tools/m0/baseline.json")
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
