package security

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

func Random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func ID() string             { return Random(18) }
func Hash(s string) string   { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func Password(password string) (string, error) {
	if len(password) < 12 || len(password) > 128 {
		return "", errors.New("password must be 12 to 128 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != "600000" || len(password) > 128 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[2])
	if e != nil || len(salt) != 16 {
		return false
	}
	actual, e := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if e != nil {
		return false
	}
	return Equal(base64.RawStdEncoding.EncodeToString(actual), parts[3])
}
func Credential(master []byte, id string) string {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("nimbus/lease/v1/" + id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func ProofMessage(method, path, timestamp, nonce string, body []byte) []byte {
	return []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, Hash(string(body))))
}
func VerifyProof(public, signature string, message []byte) bool {
	pub, e := base64.StdEncoding.DecodeString(public)
	if e != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, e := base64.StdEncoding.DecodeString(signature)
	return e == nil && ed25519.Verify(pub, message, sig)
}
func DecodeKey(value string) ([]byte, error) {
	b, e := base64.StdEncoding.DecodeString(value)
	if e != nil || len(b) != 32 {
		return nil, errors.New("key must be base64 encoded 32 bytes")
	}
	return b, nil
}
