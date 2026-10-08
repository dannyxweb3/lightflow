package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestPasswordAndProof(t *testing.T) {
	hash, e := Password("correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	if !CheckPassword(hash, "correct horse battery staple") || CheckPassword(hash, "wrong") {
		t.Fatal("password verification")
	}
	if _, e = Password("short"); e == nil {
		t.Fatal("short password accepted")
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	message := ProofMessage("POST", "/v1/connection-sessions", "123", "nonce", []byte(`{"mode":"smart"}`))
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, message))
	encoded := base64.StdEncoding.EncodeToString(pub)
	if !VerifyProof(encoded, sig, message) {
		t.Fatal("valid proof rejected")
	}
	message[0] = 'G'
	if VerifyProof(encoded, sig, message) {
		t.Fatal("altered proof accepted")
	}
}
func TestCredentialIsolation(t *testing.T) {
	a := Credential([]byte("key-a"), "session")
	if a == Credential([]byte("key-a"), "other") || a == Credential([]byte("key-b"), "session") {
		t.Fatal("credentials not scoped")
	}
	if len(a) < 40 {
		t.Fatal("credential too short")
	}
}
