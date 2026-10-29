package cloud

import (
	"crypto/ecdh"
	"encoding/json"
	"testing"
)

// An envelope WebCrypto sealed as Lody's clients do, to the key whose
// private scalar is 0x01…0x20.
const webCryptoEnvelope = `{"type":"rpc-secret-envelope-v1","algorithm":"ECDH-P256-AES-256-GCM","keyId":"a2V5LWlkLWZvci10ZXN0",
"ephemeralPublicKey":{"kty":"EC","crv":"P-256","x":"AGjI54Jl5z0jm5A2bJKQEzEWvRB98apUggo_RatxI6k","y":"y5ralkeHCIlM63zqc-JnPMu6xh6anpby3CCkYN4T59Q"},
"iv":"lRPhT-ZmUxYaHSCT","ciphertext":"Z3Ikmw5n1ddZBjMROWmdsTsjydqY4_QRJkmFWtuaDyJudLltiHp7jl1Nh0Cms1L2Gmtkd0S5_Po8ds4EtuCjYSe5"}`

func fixedRecipient(t *testing.T) *SecretRecipient {
	d := make([]byte, 32)
	for i := range d {
		d[i] = byte(i + 1)
	}
	private, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		t.Fatal(err)
	}
	r := &SecretRecipient{private: private, Key: SecretKey{Type: secretKeyType, Algorithm: secretAlgorithm, KeyID: "a2V5LWlkLWZvci10ZXN0", PublicKey: jwkOf(private.PublicKey())}}
	if r.Key.PublicKey.X != "UVw9brnjlrkE0_7Kf1T9zQzB6Ze_N13KUVrQpsO0A18" || r.Key.PublicKey.Y != "RTa-OlDzGPv5pUdZAqIhUCvvDVfgjFOyzApW8X2fk1Q" {
		t.Fatalf("jwk = %+v", r.Key.PublicKey)
	}
	return r
}

func TestOpenWebCryptoSecret(t *testing.T) {
	r := fixedRecipient(t)
	var env SecretEnvelope
	if err := json.Unmarshal([]byte(webCryptoEnvelope), &env); err != nil {
		t.Fatal(err)
	}
	got, err := r.Open(&env, AuthInputContext("ws-1", "m-1", "req-1", "int-1"))
	if err != nil || got != `{"action":"accept","content":{"apiKey":"sk-test"}}` {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := r.Open(&env, AuthInputContext("ws-1", "m-1", "req-1", "int-2")); err == nil {
		t.Fatal("opened for another interaction")
	}
}

func TestSealSecret(t *testing.T) {
	r := fixedRecipient(t)
	ctx := AuthCodeContext("ws-1", "m-1", "req-1")
	env, err := SealSecret(r.Key, "code-123", ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(env)
	var fields map[string]any
	json.Unmarshal(b, &fields)
	if len(fields) != 6 || fields["keyId"] != r.Key.KeyID || fields["type"] != "rpc-secret-envelope-v1" {
		t.Fatalf("envelope = %s", b)
	}
	if got, err := r.Open(env, ctx); err != nil || got != "code-123" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := SealSecret(SecretKey{Type: "other"}, "x", ctx); err == nil {
		t.Fatal("sealed to an unsupported key")
	}
}
