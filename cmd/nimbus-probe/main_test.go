package main

import (
	"encoding/json"
	"strings"
	"testing"

	"nimbus/internal/controlclient"
)

func TestCandidateDiagnosticExcludesCredentialAndIDs(t *testing.T) {
	candidate := controlclient.Candidate{Host: "localhost", Port: 4433, Credential: "secret-credential", EndpointID: "private-endpoint-id"}
	candidate.PublicParams.ServerName = "localhost"
	data, err := candidateDiagnostic("https://control.example", []controlclient.Candidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credential") || strings.Contains(string(data), "private-endpoint-id") {
		t.Fatal("candidate diagnostic exposed private fields")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 2 {
		t.Fatal("unexpected diagnostic fields")
	}
	var candidates []map[string]json.RawMessage
	if err := json.Unmarshal(fields["candidates"], &candidates); err != nil || len(candidates) != 1 || len(candidates[0]) != 3 {
		t.Fatal("unexpected public candidate projection")
	}
}

func TestCoreDiagnosticSinkRetainsOnlyFixedCategories(t *testing.T) {
	sink := &safeCoreEvents{}
	sink.Write([]byte(`error: x509 certificate rejected; auth=secret-credential destination=private.example`))
	if got := sink.classification(); got != "CORE_TLS_VALIDATION_FAILED" || strings.Contains(got, "secret") {
		t.Fatal("unsafe core diagnostics")
	}
	sink.Write([]byte("no recent network activity"))
	if sink.classification() != "CORE_NETWORK_TIMEOUT" {
		t.Fatal("wrong timeout category")
	}
}
