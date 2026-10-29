package collab

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/lbr77/lody-mygo/internal/cloud"
)

// Editing a session's files, as Lody's code view does through Code
// Collab: open a file's text with its digest, save over that digest, and
// ask whether the disk moved on; a reply's changes to a file; and the
// language server's answers, where a machine wires one.

// Format is how a file's text is stored: its line endings and BOM.
type Format struct {
	Encoding string `json:"encoding"` // utf8
	BOM      bool   `json:"bom,omitempty"`
	EOL      string `json:"eol,omitempty"` // lf, crlf, mixed or unknown
}

// File is a file opened for editing.
type File struct {
	Path     string
	Digest   string // sha256:… of the bytes on disk
	Text     string
	Format   *Format
	ReadOnly bool
}

type textReply struct {
	Status   string  `json:"status"`
	Path     string  `json:"path"`
	Digest   string  `json:"digest"`
	Text     *Text   `json:"text"`
	Format   *Format `json:"format"`
	ReadOnly bool    `json:"readonly"`
	Code     string  `json:"code"`
	Message  string  `json:"message"`
}

func (r *textReply) err() error {
	if r.Status == "error" {
		return &cloud.RPCError{Code: r.Code, Message: firstOf(r.Message, r.Code)}
	}
	return nil
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func (r *textReply) file() (*File, error) {
	b, err := r.Text.Bytes()
	if err != nil {
		return nil, err
	}
	return &File{Path: r.Path, Digest: r.Digest, Text: string(b), Format: r.Format, ReadOnly: r.ReadOnly}, nil
}

func params(s Session, extra map[string]any) map[string]any {
	p := map[string]any{"sessionId": s.ID}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// OpenText opens a file of the session's workspace for editing.
func OpenText(ctx context.Context, conn *cloud.Conn, s Session, path string) (*File, error) {
	var res textReply
	if err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/open-text", params(s, map[string]any{"path": path}), &res, 30*time.Second); err != nil {
		return nil, err
	}
	if err := res.err(); err != nil {
		return nil, err
	}
	return res.file()
}

// RefreshText asks whether the file still has digest: nil when it does,
// its new text when the disk moved on.
func RefreshText(ctx context.Context, conn *cloud.Conn, s Session, path, digest string) (*File, error) {
	var res textReply
	if err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/refresh-text", params(s, map[string]any{"path": path, "digest": digest}), &res, 30*time.Second); err != nil {
		return nil, err
	}
	if err := res.err(); err != nil {
		return nil, err
	}
	if res.Status == "up_to_date" {
		return nil, nil
	}
	return res.file()
}

// Conflict is a save the disk refused: it changed since the base, or the
// path did.
type Conflict struct {
	Reason     string // digest_mismatch, path_changed, file_deleted or path_conflict
	DiskDigest string
	DiskText   string // what the disk has, when the machine sent it
	HasText    bool
}

func (c *Conflict) Error() string { return "collab: save conflict: " + c.Reason }

// plainLimit is the largest text sent as is; larger goes gzipped.
const plainLimit = 64 * 1024

// encodeText is a payload of text as Code Collab carries one.
func encodeText(text string) (map[string]any, error) {
	raw := []byte(text)
	if len(raw) <= plainLimit {
		return map[string]any{"encoding": "plain", "text": text, "rawBytes": len(raw)}, nil
	}
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	if _, err := z.Write(raw); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > 1024*1024 {
		return nil, errors.New("collab: the file is too large to save")
	}
	return map[string]any{"encoding": "gzip-base64", "data": base64.StdEncoding.EncodeToString(buf.Bytes()),
		"rawBytes": len(raw), "compressedBytes": buf.Len()}, nil
}

// SaveText writes text over the file it was opened from, base its digest
// then. A disk that moved on is a *Conflict.
func SaveText(ctx context.Context, conn *cloud.Conn, s Session, path, base, text string, format *Format) (string, error) {
	payload, err := encodeText(text)
	if err != nil {
		return "", err
	}
	p := params(s, map[string]any{"requestedByUserId": s.User, "path": path, "baseDigest": base, "text": payload})
	if format != nil {
		p["format"] = format
	}
	var res struct {
		textReply
		Reason     string `json:"reason"`
		DiskDigest string `json:"diskDigest"`
		DiskText   *Text  `json:"diskText"`
	}
	if err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/save-text", p, &res, 60*time.Second); err != nil {
		return "", err
	}
	if err := res.err(); err != nil {
		return "", err
	}
	if res.Status == "conflict" {
		c := &Conflict{Reason: res.Reason, DiskDigest: res.DiskDigest}
		if res.DiskText != nil {
			if b, err := res.DiskText.Bytes(); err == nil {
				c.DiskText, c.HasText = string(b), true
			}
		}
		return "", c
	}
	return res.Digest, nil
}

// TurnDiff is what one reply changed in a file.
func TurnDiff(ctx context.Context, conn *cloud.Conn, s Session, turn, path string) (*Change, error) {
	var res struct {
		Change
		Message string `json:"message"`
	}
	if err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/open-turn-diff", params(s, map[string]any{"turnId": turn, "path": path}), &res, 60*time.Second); err != nil {
		return nil, err
	}
	if res.Status == "error" {
		return nil, &cloud.RPCError{Code: res.Reason, Message: res.Message}
	}
	return &res.Change, nil
}

// InitDirectory has the machine publish a folder's entries to the
// session's file tree.
func InitDirectory(ctx context.Context, conn *cloud.Conn, s Session, path string) (int, error) {
	var res struct {
		Status    string `json:"status"`
		Published int    `json:"publishedEntries"`
		Code      string `json:"code"`
		Message   string `json:"message"`
	}
	if err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/init-directory", params(s, map[string]any{"path": path}), &res, 30*time.Second); err != nil {
		return 0, err
	}
	if res.Status != "ok" {
		return 0, &cloud.RPCError{Code: res.Code, Message: firstOf(res.Message, res.Code)}
	}
	return res.Published, nil
}

// ErrNoLanguageServer is a machine without a language server for the
// file.
var ErrNoLanguageServer = errors.New("collab: no language server")

// Lsp asks the machine's language server for the definition or the
// references ("definition" or "references") of what is at line,
// character (both from 0) of a file.
func Lsp(ctx context.Context, conn *cloud.Conn, s Session, what, path string, line, character int) (json.RawMessage, error) {
	var raw json.RawMessage
	err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/lsp-"+what,
		params(s, map[string]any{"path": path, "line": line, "character": character}), &raw, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var head struct {
		Status  string `json:"status"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal(raw, &head)
	switch head.Status {
	case "unsupported":
		return nil, ErrNoLanguageServer
	case "error":
		return nil, &cloud.RPCError{Code: head.Code, Message: firstOf(head.Message, head.Code)}
	}
	return raw, nil
}
