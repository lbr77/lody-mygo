package cloud

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ProjectControl asks a machine about one of its local projects, through
// its local-project/control method: request's type names what, as
// "local-project/list-files". It returns the request's result.
func (w *Conn) ProjectControl(ctx context.Context, machineID string, request map[string]any, timeout time.Duration) (json.RawMessage, error) {
	req := map[string]any{"workspaceId": w.ID, "machineId": machineID}
	for k, v := range request {
		req[k] = v
	}
	raw, err := w.MachineRPC(ctx, machineID, "local-project/control", map[string]any{"request": req}, timeout)
	if err != nil {
		return nil, err
	}
	var reply struct {
		OK      bool            `json:"ok"`
		Type    string          `json:"type"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("machine: %w", err)
	}
	if !reply.OK {
		return nil, &RPCError{Code: reply.Error, Message: reply.Message}
	}
	if reply.Type != request["type"] {
		return nil, errors.New("machine: unexpected reply " + reply.Type)
	}
	return reply.Result, nil
}

// Code Collab's methods, file/preview and the others that read a session's
// files, carry their params and results sealed with AES-256-GCM under a
// key derived from the session tree's root, as Lody's clients seal them.
const (
	collabKeyLabel   = "lody-code-collab-v2-bootstrap-content-key-v1"
	collabKeyIDLabel = "lody-code-collab-v2-bootstrap-content-key-id-v1"
	collabSalt       = "lody-code-collab-v2-bootstrap-salt-v1"
	collabAADLabel   = "lody-code-collab-v2-machine-rpc-payload-v1"
)

// Envelope is a sealed payload.
type Envelope struct {
	Type           string `json:"type"`
	KeyVersion     int    `json:"keyVersion"`
	Algorithm      string `json:"algorithm"`
	OwnerSessionID string `json:"ownerSessionId"`
	KeyID          string `json:"keyId"`
	IV             string `json:"iv"`
	Ciphertext     string `json:"ciphertext"`
}

func collabKey(owner string) []byte {
	sum := sha256.Sum256([]byte(collabKeyLabel + "\x00" + collabSalt + "\x00" + owner))
	return sum[:]
}

func collabKeyID(owner string) string {
	sum := sha256.Sum256([]byte(collabKeyIDLabel + "\x00" + collabSalt + "\x00" + owner))
	return "ccv2:" + hex.EncodeToString(sum[:])[:24]
}

func collabAAD(owner, keyID string, version int) []byte {
	return []byte(collabAADLabel + "\x00" + owner + "\x00" + keyID + "\x00" + strconv.Itoa(version))
}

// Seal seals payload for the session tree whose root is owner.
func Seal(owner string, payload any) (*Envelope, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, errors.New("seal: no owner session")
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	gcm, err := collabGCM(owner)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	keyID := collabKeyID(owner)
	sealed := gcm.Seal(nil, iv, plain, collabAAD(owner, keyID, 1))
	return &Envelope{
		Type: "code-collab-v2-content-envelope", KeyVersion: 1, Algorithm: "AES-256-GCM",
		OwnerSessionID: owner, KeyID: keyID,
		IV: base64.RawURLEncoding.EncodeToString(iv), Ciphertext: base64.RawURLEncoding.EncodeToString(sealed),
	}, nil
}

// Open opens an envelope sealed for owner into out.
func Open(owner string, env *Envelope, out any) error {
	owner = strings.TrimSpace(owner)
	if env == nil || env.Type != "code-collab-v2-content-envelope" {
		return errors.New("open: not an envelope")
	}
	if strings.TrimSpace(env.OwnerSessionID) != owner {
		return errors.New("open: owner session mismatch")
	}
	if env.KeyID != collabKeyID(owner) {
		return errors.New("open: key id mismatch")
	}
	iv, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(env.IV, "="))
	if err != nil {
		return err
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(env.Ciphertext, "="))
	if err != nil {
		return err
	}
	gcm, err := collabGCM(owner)
	if err != nil {
		return err
	}
	if len(iv) != gcm.NonceSize() {
		return errors.New("open: bad nonce")
	}
	plain, err := gcm.Open(nil, iv, sealed, collabAAD(owner, env.KeyID, env.KeyVersion))
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, out)
}

func collabGCM(owner string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(collabKey(owner))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// SealedRPC calls a method whose params and result are sealed for the
// session tree whose root is owner, as code-collab/* and file/preview are.
// An error's code stays in the clear; its data, when the machine sealed
// it, is opened into the error's message.
func (w *Conn) SealedRPC(ctx context.Context, machineID, owner, method string, params, out any, timeout time.Duration) error {
	env, err := Seal(owner, params)
	if err != nil {
		return err
	}
	raw, err := w.MachineRPC(ctx, machineID, method, env, timeout)
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && len(rpcErr.Data) > 0 {
		var data Envelope
		var detail struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(rpcErr.Data, &data) == nil && Open(owner, &data, &detail) == nil && detail.Message != "" {
			return &RPCError{Code: firstOf(detail.Code, rpcErr.Code), Message: detail.Message}
		}
	}
	if err != nil {
		return err
	}
	var result Envelope
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("machine: %w", err)
	}
	return Open(owner, &result, out)
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
