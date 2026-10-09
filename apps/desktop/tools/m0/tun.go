package main

import (
	"encoding/json"
	"errors"
	"os"
)

func tunConfiguration(raw []byte, device string) ([]byte, error) {
	var base, overlay map[string]any
	if json.Unmarshal(raw, &base) != nil {
		return nil, errors.New("INVALID_BASELINE")
	}
	overlayBytes, err := os.ReadFile("apps/desktop/tools/m0/tun.json")
	if err != nil || json.Unmarshal(overlayBytes, &overlay) != nil {
		return nil, errors.New("INVALID_TUN_TEMPLATE")
	}
	for key, value := range overlay {
		base[key] = value
	}
	base["tun"].(map[string]any)["device"] = device
	// IPv6 is explicitly intercepted and rejected in this first prototype.
	// Merely disabling AAAA is insufficient. Full IPv6 forwarding is a later test.
	rules := []string{"IP-CIDR,192.168.194.128/32,DIRECT,no-resolve", "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve", "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", "IP-CIDR,172.16.0.0/12,DIRECT,no-resolve", "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve", "IP-CIDR6,::/0,REJECT,no-resolve"}
	for _, rule := range base["rules"].([]any) {
		rules = append(rules, rule.(string))
	}
	base["rules"] = rules
	return json.MarshalIndent(base, "", "  ")
}
