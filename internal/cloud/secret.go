package cloud

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// A secret a machine asks for during a request, an API key or a sign-in's
// authorization code, travels sealed to a key the machine made for that
// request: ECDH on P-256, the shared secret the AES-256-GCM key, as Lody's
// rpc-secret envelopes are.
const (
	secretKeyType      = "rpc-secret-public-key-v1"
	secretEnvelopeType = "rpc-secret-envelope-v1"
	secretAlgorithm    = "ECDH-P256-AES-256-GCM"
	secretAADLabel     = "lody-machine-rpc-secret-v1"
)

// JWK is a P-256 public key.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// SecretKey is the key a machine publishes for secrets sent to it.
type SecretKey struct {
	Type      string `json:"type"`
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	PublicKey JWK    `json:"publicKey"`
}

// SecretEnvelope is a secret sealed to a SecretKey.
type SecretEnvelope struct {
	Type               string `json:"type"`
	Algorithm          string `json:"algorithm"`
	KeyID              string `json:"keyId"`
	EphemeralPublicKey JWK    `json:"ephemeralPublicKey"`
	IV                 string `json:"iv"`
	Ciphertext         string `json:"ciphertext"`
}

var b64url = base64.RawURLEncoding

func (k JWK) key() (*ecdh.PublicKey, error) {
	if k.Kty != "EC" || k.Crv != "P-256" {
		return nil, errors.New("secret: not a P-256 key")
	}
	x, err := b64url.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	y, err := b64url.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	if len(x) != 32 || len(y) != 32 {
		return nil, errors.New("secret: bad key coordinates")
	}
	return ecdh.P256().NewPublicKey(append(append([]byte{4}, x...), y...))
}

func jwkOf(k *ecdh.PublicKey) JWK {
	raw := k.Bytes() // 0x04 || x || y
	return JWK{Kty: "EC", Crv: "P-256", X: b64url.EncodeToString(raw[1:33]), Y: b64url.EncodeToString(raw[33:])}
}

func secretGCM(private *ecdh.PrivateKey, public *ecdh.PublicKey) (cipher.AEAD, error) {
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func secretAAD(keyID, context string) []byte {
	return []byte(secretAADLabel + "\x00" + keyID + "\x00" + context)
}

// SealSecret seals plaintext to the machine's key; context names what the
// secret is for, so the machine opens it for nothing else.
func SealSecret(to SecretKey, plaintext, context string) (*SecretEnvelope, error) {
	if to.Type != secretKeyType || to.Algorithm != secretAlgorithm || to.KeyID == "" {
		return nil, errors.New("secret: unsupported key")
	}
	public, err := to.PublicKey.key()
	if err != nil {
		return nil, err
	}
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	gcm, err := secretGCM(private, public)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), secretAAD(to.KeyID, context))
	return &SecretEnvelope{
		Type: secretEnvelopeType, Algorithm: secretAlgorithm, KeyID: to.KeyID,
		EphemeralPublicKey: jwkOf(private.PublicKey()),
		IV:                 b64url.EncodeToString(iv), Ciphertext: b64url.EncodeToString(sealed),
	}, nil
}

// SecretRecipient holds the private half of a SecretKey.
type SecretRecipient struct {
	Key     SecretKey
	private *ecdh.PrivateKey
}

// NewSecretRecipient makes a key to receive secrets with, as a machine does.
func NewSecretRecipient() (*SecretRecipient, error) {
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	return &SecretRecipient{private: private, Key: SecretKey{
		Type: secretKeyType, Algorithm: secretAlgorithm, KeyID: b64url.EncodeToString(id), PublicKey: jwkOf(private.PublicKey()),
	}}, nil
}

// Open reads an envelope sealed to r for context.
func (r *SecretRecipient) Open(env *SecretEnvelope, context string) (string, error) {
	if env.KeyID != r.Key.KeyID {
		return "", errors.New("secret: key mismatch")
	}
	public, err := env.EphemeralPublicKey.key()
	if err != nil {
		return "", err
	}
	gcm, err := secretGCM(r.private, public)
	if err != nil {
		return "", err
	}
	iv, err := b64url.DecodeString(env.IV)
	if err != nil {
		return "", err
	}
	data, err := b64url.DecodeString(env.Ciphertext)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, data, secretAAD(env.KeyID, context))
	if err != nil {
		return "", errors.New("secret: cannot open")
	}
	return string(plain), nil
}

// AuthCodeContext is the context of a sign-in's authorization code.
func AuthCodeContext(workspace, machine, authRequest string) string {
	return "machine/acp-authenticate\x00" + workspace + "\x00" + machine + "\x00" + authRequest
}

// AuthInputContext is the context of an answer to a sign-in's question.
func AuthInputContext(workspace, machine, authRequest, interaction string) string {
	return "machine/acp-authenticate-input\x00" + workspace + "\x00" + machine + "\x00" + authRequest + "\x00" + interaction
}
