package main

import (
	"encoding/json"

	"nimbus.local/client/internal/controlclient"
)

// Explicit field projection prevents secrets from entering diagnostic output.
func candidateDiagnostic(base string, candidates []controlclient.Candidate) ([]byte, error) {
	type publicCandidate struct {
		Host         string `json:"host"`
		Port         int    `json:"port"`
		PublicParams struct {
			ServerName string `json:"server_name"`
		} `json:"public_params"`
	}
	output := struct {
		ControlBaseURL string            `json:"control_base_url"`
		Candidates     []publicCandidate `json:"candidates"`
	}{ControlBaseURL: base, Candidates: make([]publicCandidate, 0, len(candidates))}
	for _, candidate := range candidates {
		item := publicCandidate{Host: candidate.Host, Port: candidate.Port}
		item.PublicParams.ServerName = candidate.PublicParams.ServerName
		output.Candidates = append(output.Candidates, item)
	}
	return json.MarshalIndent(output, "", "  ")
}
