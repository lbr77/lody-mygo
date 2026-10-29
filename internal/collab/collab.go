// Package collab reads a session's files from its machine, as Lody's
// inspector does: a local project's folders through local-project/control,
// a file's content through file/preview, and the session's changes through
// Code Collab's diff methods, whose payloads are sealed (cloud.SealedRPC).
package collab

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lbr77/lody-mygo/internal/cloud"
)

// Session is where a session's files are: its machine, its local project
// if it has one, and the root of its tree, which owns the Code Collab key.
type Session struct {
	Machine      string
	LocalProject string
	ID           string
	Owner        string // the root session: the parent's id, or the session's own
	User         string
}

// Text is a file's text or bytes as the machine sends them: plain, or
// gzipped, or base64.
type Text struct {
	Encoding string `json:"encoding"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	RawBytes int    `json:"rawBytes"`
}

// Bytes decodes t.
func (t *Text) Bytes() ([]byte, error) {
	if t == nil {
		return nil, errors.New("no content")
	}
	switch t.Encoding {
	case "plain", "utf8-plain":
		return []byte(t.Text), nil
	case "gzip-base64", "utf8-gzip-base64":
		zipped, err := decodeBase64(t.Data)
		if err != nil {
			return nil, err
		}
		r, err := gzip.NewReader(bytes.NewReader(zipped))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(io.LimitReader(r, 16<<20))
	case "base64":
		return decodeBase64(t.Data)
	}
	return nil, errors.New("unknown encoding " + t.Encoding)
}

func decodeBase64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// Snapshot is one side of a diff: text, or why there is none.
type Snapshot struct {
	Kind string `json:"kind"` // text, missing, binary or too_large
	Text *Text  `json:"text,omitempty"`
}

// String returns the side's text; a missing file is empty, and binary or
// large files have none.
func (s *Snapshot) String() (string, bool) {
	if s == nil {
		return "", false
	}
	switch s.Kind {
	case "missing":
		return "", true
	case "text":
		b, err := s.Text.Bytes()
		return string(b), err == nil
	}
	return "", false
}

// Change is a file the session changed.
type Change struct {
	Status string    `json:"status"` // ok, deferred or unavailable
	Path   string    `json:"path"`
	Old    *Snapshot `json:"oldSnapshot,omitempty"`
	New    *Snapshot `json:"newSnapshot,omitempty"`
	Add    *int      `json:"add,omitempty"`
	Del    *int      `json:"del,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// Kind is added, deleted or modified, by which side is missing.
func (c *Change) Kind() string {
	switch {
	case c.Old != nil && c.Old.Kind == "missing":
		return "added"
	case c.New != nil && c.New.Kind == "missing":
		return "deleted"
	}
	return "modified"
}

// Changes is a session's changes against a base.
type Changes struct {
	Status    string   `json:"status"` // ok or unavailable
	Base      string   `json:"base"`
	Entries   []Change `json:"entries"`
	Truncated bool     `json:"truncated"`
	Reason    string   `json:"reason"`
	Message   string   `json:"message"`
}

// Scopes of changes: against the merge base, or the last commit.
const (
	ScopeBranch      = "branch"
	ScopeUncommitted = "uncommitted"
)

// AllChanges lists what the session changed in scope.
func AllChanges(ctx context.Context, conn *cloud.Conn, s Session, scope string) (*Changes, error) {
	var res Changes
	err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/open-all-changes-diff",
		map[string]any{"sessionId": s.ID, "scope": scope}, &res, 60*time.Second)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(res.Entries, func(i, j int) bool { return res.Entries[i].Path < res.Entries[j].Path })
	return &res, nil
}

// CurrentDiff is one file's change, for those AllChanges deferred.
func CurrentDiff(ctx context.Context, conn *cloud.Conn, s Session, path, scope string) (*Change, error) {
	var res Change
	err := conn.SealedRPC(ctx, s.Machine, s.Owner, "code-collab/open-current-diff",
		map[string]any{"sessionId": s.ID, "path": path, "scope": scope}, &res, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// Preview is a file's content.
type Preview struct {
	Status    string `json:"status"` // ok or error
	Path      string `json:"path"`
	Kind      string `json:"kind"` // text or binary
	Content   *Text  `json:"content,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	SizeBytes int    `json:"sizeBytes"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// MaxPreview is how much of a file is asked for, as lody-ios asks.
const MaxPreview = 2 << 20

// ReadFile reads a file of the session's workspace, its path relative to
// the workspace's root.
func ReadFile(ctx context.Context, conn *cloud.Conn, s Session, path string) (*Preview, error) {
	var res Preview
	err := conn.SealedRPC(ctx, s.Machine, s.Owner, "file/preview",
		map[string]any{"v": 3, "sessionId": s.ID, "path": path, "maxBytes": MaxPreview}, &res, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if res.Status == "error" {
		return nil, &cloud.RPCError{Code: res.Code, Message: res.Message}
	}
	return &res, nil
}

// DirEntry is a file or folder of a project.
type DirEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // file or directory
}

// Dir reports whether the entry is a folder.
func (e DirEntry) Dir() bool { return e.Type == "directory" }

// ListDir lists a folder of the session's local project, folders first,
// rel relative to the project's root ("" for the root).
func ListDir(ctx context.Context, conn *cloud.Conn, s Session, rel string) ([]DirEntry, bool, error) {
	if s.LocalProject == "" {
		return nil, false, errors.New("not a local project")
	}
	raw, err := conn.ProjectControl(ctx, s.Machine, map[string]any{
		"type": "local-project/list-dir", "localProjectId": s.LocalProject, "relativePath": rel,
		"limit": 2000, "requestedByUserId": s.User,
	}, 20*time.Second)
	if err != nil {
		return nil, false, err
	}
	var res struct {
		Entries   []DirEntry `json:"entries"`
		Truncated bool       `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, false, err
	}
	SortEntries(res.Entries)
	return res.Entries, res.Truncated, nil
}

// SortEntries puts folders first, then sorts by name, case aside.
func SortEntries(entries []DirEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir() != entries[j].Dir() {
			return entries[i].Dir()
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}
