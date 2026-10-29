// Package shortcuts keeps a workspace's prompt shortcuts as Lody does: a
// directory in Convex of who may see which, an index per owner and
// visibility (a Flock stream, "shortcut-index:…") and a body per
// publication (a Loro document, "shortcut-body:…"), each stream opened
// with a token Convex grants for it. Saving follows lody-ios: stage the
// document, write its body, activate it, then list it in the index.
package shortcuts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	loro "github.com/aholstenson/loro-go"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// Shortcut is a prompt saved to be sent with /slug.
type Shortcut struct {
	V           int            `json:"v"`
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	OwnerUserID string         `json:"ownerUserId"`
	Visibility  string         `json:"visibility"` // private or workspace
	Name        string         `json:"name"`
	Emoji       string         `json:"emoji,omitempty"`
	Slug        string         `json:"slug"`
	Description string         `json:"description,omitempty"`
	Prompt      string         `json:"prompt"`
	Mentions    []any          `json:"mentions"`
	Scope       map[string]any `json:"scope"`
	Revision    string         `json:"revision"`
	CreatedAt   int64          `json:"createdAt"`
	UpdatedAt   int64          `json:"updatedAt"`
}

// Entry is a shortcut as its index lists it: all but the prompt.
type Entry struct {
	V                 int            `json:"v"`
	ID                string         `json:"id"`
	WorkspaceID       string         `json:"workspaceId"`
	OwnerUserID       string         `json:"ownerUserId"`
	Visibility        string         `json:"visibility"`
	Name              string         `json:"name"`
	Emoji             string         `json:"emoji,omitempty"`
	Slug              string         `json:"slug"`
	Description       string         `json:"description,omitempty"`
	Scope             map[string]any `json:"scope"`
	Revision          string         `json:"revision"`
	CreatedAt         int64          `json:"createdAt"`
	UpdatedAt         int64          `json:"updatedAt"`
	BodyDocID         string         `json:"bodyDocId"`
	DependencySummary []any          `json:"dependencySummary"`
}

// Simple reports whether a shortcut has no scope, as the shortcuts this
// client edits are.
func (e Entry) Simple() bool { return len(e.Scope) == 0 && len(e.DependencySummary) == 0 }

// Limits are Lody's.
const (
	MaxName        = 60
	MaxDescription = 240
	maxPromptBytes = 256 * 1024
	maxIndexBytes  = 8 * 1024
	maxEntries     = 100
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Slugify makes a slug of a name as Lody's editor does.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	return slug
}

// Validate checks a shortcut against Lody's rules.
func (s Shortcut) Validate() error {
	switch {
	case strings.TrimSpace(s.Name) == "" || utf8.RuneCountInString(s.Name) > MaxName:
		return errors.New("shortcuts: the name must have 1 to 60 characters")
	case !slugPattern.MatchString(s.Slug):
		return errors.New("shortcuts: the command must be lowercase letters, digits and dashes")
	case utf8.RuneCountInString(s.Description) > MaxDescription:
		return errors.New("shortcuts: the description is too long")
	case strings.TrimSpace(s.Prompt) == "":
		return errors.New("shortcuts: the prompt is empty")
	case len(s.Prompt) > maxPromptBytes:
		return errors.New("shortcuts: the prompt is too long")
	case s.Visibility != "private" && s.Visibility != "workspace":
		return errors.New("shortcuts: bad visibility")
	}
	return nil
}

// index projects a shortcut into its index entry.
func (s Shortcut) index(bodyDocID string) Entry {
	return Entry{
		V: 1, ID: s.ID, WorkspaceID: s.WorkspaceID, OwnerUserID: s.OwnerUserID, Visibility: s.Visibility,
		Name: s.Name, Emoji: s.Emoji, Slug: s.Slug, Description: s.Description, Scope: s.Scope,
		Revision: s.Revision, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		BodyDocID: bodyDocID, DependencySummary: []any{},
	}
}

// Store reaches a workspace's shortcuts as one user.
type Store struct {
	Convex    *cloud.Convex
	HTTP      *http.Client
	Workspace string
	User      string
}

type dirRow struct {
	ShortcutID  string  `json:"shortcutId"`
	BodyDocID   string  `json:"bodyDocId"`
	OwnerUserID string  `json:"ownerUserId"`
	Visibility  string  `json:"visibility"`
	Revision    *string `json:"revision"`
	Deleted     bool    `json:"deleted"`
}

func (s *Store) directory(ctx context.Context) ([]dirRow, error) {
	var rows []dirRow
	if err := s.Convex.Query(ctx, "promptShortcuts:listAccessibleDocuments", map[string]any{"workspaceId": s.Workspace}, &rows); err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if !r.Deleted && (r.Visibility == "workspace" || r.OwnerUserID == s.User) {
			out = append(out, r)
		}
	}
	return out, nil
}

func indexStream(workspace, owner, visibility string) string {
	return "shortcut-index:" + streams.EncodeURIComponent(workspace) + ":" + streams.EncodeURIComponent(owner) + ":" + visibility
}

func bodyStream(bodyDocID string) string {
	return "shortcut-body:" + streams.EncodeURIComponent(bodyDocID)
}

// client opens a shortcut stream with a token Convex grants for it,
// granting again as the token nears its end.
func (s *Store) client(ctx context.Context, stream string, target map[string]any, write bool) (*streams.Client, error) {
	var mu sync.Mutex
	var token string
	var expires time.Time
	grant := func(ctx context.Context) (string, string, error) {
		var g struct {
			Token          string  `json:"token"`
			ExpiresIn      float64 `json:"expiresIn"`
			GatewayBaseURL string  `json:"gatewayBaseUrl"`
			StreamID       string  `json:"streamId"`
		}
		err := s.Convex.Action(ctx, "promptShortcuts:getStreamToken", map[string]any{"workspaceId": s.Workspace, "target": target, "write": write}, &g)
		if err != nil {
			return "", "", err
		}
		if g.StreamID != stream || !strings.HasPrefix(g.GatewayBaseURL, "https://") || g.Token == "" {
			return "", "", errors.New("shortcuts: the grant is for another stream")
		}
		mu.Lock()
		token, expires = g.Token, time.Now().Add(time.Duration(g.ExpiresIn*float64(time.Second)))
		mu.Unlock()
		return g.Token, g.GatewayBaseURL, nil
	}
	_, gateway, err := grant(ctx)
	if err != nil {
		return nil, err
	}
	return &streams.Client{
		URL:  streams.StreamURL(gateway, stream),
		HTTP: s.HTTP,
		Token: func(ctx context.Context) (string, error) {
			mu.Lock()
			t, fresh := token, time.Until(expires) > 5*time.Second
			mu.Unlock()
			if fresh {
				return t, nil
			}
			t, _, err := grant(ctx)
			return t, err
		},
	}, nil
}

// readAll reads a stream: its snapshot, then every frame after it.
func readAll(ctx context.Context, c *streams.Client) (snapshot []byte, frames [][]byte, err error) {
	boot, err := c.Bootstrap(ctx)
	if err != nil {
		return nil, nil, err
	}
	if boot.Snapshot != nil {
		if snapshot, err = streams.Unpack(boot.Snapshot); err != nil {
			return nil, nil, err
		}
	}
	add := func(body []byte) error {
		fs, err := streams.DecodeFrames(body)
		frames = append(frames, fs...)
		return err
	}
	for _, u := range boot.Updates {
		if err := add(u); err != nil {
			return nil, nil, err
		}
	}
	offset, cursor, done := boot.NextOffset, boot.Cursor, boot.UpToDate
	for pages := 0; !done; pages++ {
		if pages > 50 {
			return nil, nil, errors.New("shortcuts: the stream doesn't end")
		}
		r, err := c.Read(ctx, offset, cursor, false)
		if err != nil {
			return nil, nil, err
		}
		if len(r.Payload) > 0 {
			if err := add(r.Payload); err != nil {
				return nil, nil, err
			}
		}
		if r.NextOffset == offset && !r.UpToDate {
			return nil, nil, errors.New("shortcuts: stalled stream")
		}
		offset, cursor, done = r.NextOffset, r.Cursor, r.UpToDate
	}
	return snapshot, frames, nil
}

// index opens an owner's index for a visibility, made when writing.
func (s *Store) index(ctx context.Context, owner, visibility string, write bool) (*flock.Flock, *streams.Client, error) {
	stream := indexStream(s.Workspace, owner, visibility)
	c, err := s.client(ctx, stream, map[string]any{"kind": "index", "ownerUserId": owner, "visibility": visibility}, write)
	if err != nil {
		return nil, nil, err
	}
	if write {
		if err := c.Create(ctx, "application/octet-stream", 0); err != nil {
			return nil, nil, err
		}
	}
	f, err := flock.New("lody-mygo-shortcuts-" + uuid.NewString())
	if err != nil {
		return nil, nil, err
	}
	snapshot, frames, err := readAll(ctx, c)
	if streams.IsNotFound(err) && !write {
		return f, c, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if snapshot != nil {
		if err := f.ImportFile(snapshot); err != nil {
			return nil, nil, err
		}
	}
	for _, frame := range frames {
		var bundle map[string]any
		if err := json.Unmarshal(frame, &bundle); err != nil {
			return nil, nil, err
		}
		if err := f.ImportJSON(bundle); err != nil {
			return nil, nil, err
		}
	}
	return f, c, nil
}

// entries lists the live entries of an index, tombstones winning.
func entries(f *flock.Flock) ([]Entry, error) {
	rows, err := f.Scan()
	if err != nil {
		return nil, err
	}
	deleted := map[string]bool{}
	for _, r := range rows {
		if r.Live && len(r.Key) == 2 && r.Key[0] == "deletedPromptShortcut" {
			if id, _ := r.Key[1].(string); id != "" {
				deleted[id] = true
			}
		}
	}
	var out []Entry
	for _, r := range rows {
		if !r.Live || len(r.Key) != 2 || r.Key[0] != "promptShortcut" {
			continue
		}
		id, _ := r.Key[1].(string)
		if deleted[id] {
			continue
		}
		b, _ := json.Marshal(r.Value)
		var rec struct {
			Kind  string `json:"kind"`
			Entry Entry  `json:"entry"`
		}
		if json.Unmarshal(b, &rec) != nil || rec.Kind != "active" || rec.Entry.ID != id {
			continue
		}
		out = append(out, rec.Entry)
	}
	return out, nil
}

// List lists the shortcuts the user may use, as the directory allows.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.directory(ctx)
	if err != nil {
		return nil, err
	}
	domains := map[string]dirRow{}
	for _, r := range rows {
		domains[r.OwnerUserID+":"+r.Visibility] = r
	}
	var out []Entry
	for _, d := range domains {
		f, _, err := s.index(ctx, d.OwnerUserID, d.Visibility, false)
		if err != nil {
			return nil, err
		}
		list, err := entries(f)
		f.Free()
		if err != nil {
			return nil, err
		}
		for _, e := range list {
			if e.WorkspaceID != s.Workspace || e.OwnerUserID != d.OwnerUserID || e.Visibility != d.Visibility {
				continue
			}
			for _, r := range rows {
				if r.ShortcutID == e.ID && r.BodyDocID == e.BodyDocID && r.Revision != nil && *r.Revision == e.Revision {
					out = append(out, e)
					break
				}
			}
		}
	}
	return out, nil
}

// Read reads a shortcut's prompt from its body.
func (s *Store) Read(ctx context.Context, e Entry) (*Shortcut, error) {
	stream := bodyStream(e.BodyDocID)
	c, err := s.client(ctx, stream, map[string]any{"kind": "body", "bodyDocId": e.BodyDocID}, false)
	if err != nil {
		return nil, err
	}
	snapshot, frames, err := readAll(ctx, c)
	if err != nil {
		return nil, err
	}
	doc := loro.NewLoroDoc()
	if snapshot != nil {
		if _, err := doc.Import(snapshot); err != nil {
			return nil, err
		}
	}
	for _, f := range frames {
		if _, err := doc.Import(f); err != nil {
			return nil, err
		}
	}
	contents, ok := loro.ValueAsAny(doc.GetMap(loro.AsContainerId("contents")).GetDeepValue())
	if !ok {
		return nil, errors.New("shortcuts: the body isn't a document")
	}
	b, err := json.Marshal(contents)
	if err != nil {
		return nil, err
	}
	var all map[string]Shortcut
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	sc, ok := all[e.Revision]
	if !ok {
		return nil, fmt.Errorf("shortcuts: revision %s isn't in its body", e.Revision)
	}
	return &sc, nil
}

// Save publishes a shortcut, new or an edit of base: a fresh body with a
// new revision, then its entry in the owner's index.
func (s *Store) Save(ctx context.Context, value Shortcut, base *Entry) (Entry, error) {
	now := time.Now().UnixMilli()
	value.V, value.WorkspaceID, value.OwnerUserID = 1, s.Workspace, s.User
	value.Revision, value.UpdatedAt = uuid.NewString(), now
	if value.Mentions == nil {
		value.Mentions = []any{}
	}
	if value.Scope == nil {
		value.Scope = map[string]any{}
	}
	if base != nil {
		value.ID, value.CreatedAt, value.Visibility = base.ID, base.CreatedAt, base.Visibility
	} else {
		value.ID, value.CreatedAt = uuid.NewString(), now
	}
	if err := value.Validate(); err != nil {
		return Entry{}, err
	}
	entry := value.index(uuid.NewString())
	encoded, _ := json.Marshal(entry)
	if len(encoded) > maxIndexBytes {
		return Entry{}, errors.New("shortcuts: the shortcut is too large")
	}
	// Check the slug and quota before staging, as Lody's catalog does.
	f, index, err := s.index(ctx, s.User, value.Visibility, true)
	if err != nil {
		return Entry{}, err
	}
	defer f.Free()
	current, err := entries(f)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range current {
		if e.ID != entry.ID && e.Slug == entry.Slug {
			return Entry{}, fmt.Errorf("shortcuts: /%s is taken", entry.Slug)
		}
	}
	if base == nil && len(current) >= maxEntries {
		return Entry{}, errors.New("shortcuts: the list is full")
	}
	var staged struct {
		BodyDocID string `json:"bodyDocId"`
		Status    string `json:"status"`
	}
	if err := s.Convex.Mutation(ctx, "promptShortcuts:stageDocument", map[string]any{
		"workspaceId": s.Workspace, "ownerUserId": s.User, "shortcutId": entry.ID, "bodyDocId": entry.BodyDocID, "visibility": entry.Visibility,
	}, &staged); err != nil {
		return Entry{}, err
	}
	if staged.BodyDocID != entry.BodyDocID {
		return Entry{}, errors.New("shortcuts: staged another document")
	}
	if staged.Status == "staged" {
		if err := s.publishBody(ctx, entry, value, base, len(encoded)); err != nil {
			// The staged document is left half done: the server settles it,
			// activating it if it got that far and cancelling it if not.
			var status string
			if s.Convex.Mutation(ctx, "promptShortcuts:settleDocument", map[string]any{
				"workspaceId": s.Workspace, "shortcutId": entry.ID, "bodyDocId": entry.BodyDocID, "visibility": entry.Visibility,
			}, &status) != nil || status != "active" {
				return Entry{}, err
			}
		}
	}
	before, err := f.Version()
	if err != nil {
		return Entry{}, err
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		return Entry{}, err
	}
	if err := f.Put([]any{"promptShortcut", entry.ID}, map[string]any{"kind": "active", "entry": row}); err != nil {
		return Entry{}, err
	}
	delta, err := f.ExportJSON(before)
	if err != nil {
		return Entry{}, err
	}
	b, err := json.Marshal(delta)
	if err != nil {
		return Entry{}, err
	}
	if _, err := index.Append(ctx, "application/octet-stream", streams.EncodeFrame(b)); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// publishBody writes a staged body document and activates it.
func (s *Store) publishBody(ctx context.Context, entry Entry, value Shortcut, base *Entry, indexBytes int) error {
	stream := bodyStream(entry.BodyDocID)
	body, err := s.client(ctx, stream, map[string]any{"kind": "body", "bodyDocId": entry.BodyDocID}, true)
	if err != nil {
		return err
	}
	if err := body.Create(ctx, "application/octet-stream", 0); err != nil {
		return err
	}
	snapshot, err := bodySnapshot(value)
	if err != nil {
		return err
	}
	if _, err := body.Append(ctx, "application/octet-stream", streams.EncodeFrame(snapshot)); err != nil {
		return err
	}
	var prevBody, prevRevision any
	if base != nil {
		prevBody, prevRevision = base.BodyDocID, base.Revision
	}
	return s.Convex.Mutation(ctx, "promptShortcuts:activateDocument", map[string]any{
		"workspaceId": s.Workspace, "bodyDocId": entry.BodyDocID, "previousBodyDocId": prevBody, "previousRevision": prevRevision,
		"revision": entry.Revision, "slug": entry.Slug, "indexBytes": indexBytes,
	}, nil)
}

// bodySnapshot is a fresh body document holding one revision.
func bodySnapshot(value Shortcut) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var content map[string]any
	if err := json.Unmarshal(b, &content); err != nil {
		return nil, err
	}
	doc := loro.NewLoroDoc()
	if err := doc.GetMap(loro.AsContainerId("contents")).InsertAny(value.Revision, content); err != nil {
		return nil, err
	}
	if err := doc.GetMap(loro.AsContainerId("revisions")).InsertAny(value.Revision, map[string]any{"parents": []any{}}); err != nil {
		return nil, err
	}
	doc.Commit()
	return doc.ExportSnapshot()
}

// Delete revokes a shortcut and tombstones it in both of the owner's
// indexes, as Lody does.
func (s *Store) Delete(ctx context.Context, e Entry) error {
	if err := s.Convex.Mutation(ctx, "promptShortcuts:revokeShortcut", map[string]any{
		"workspaceId": s.Workspace, "shortcutId": e.ID, "bodyDocId": e.BodyDocID, "visibility": e.Visibility,
	}, nil); err != nil {
		return err
	}
	for _, visibility := range []string{"private", "workspace"} {
		f, index, err := s.index(ctx, e.OwnerUserID, visibility, true)
		if err != nil {
			return err
		}
		before, err := f.Version()
		if err == nil {
			err = f.Put([]any{"deletedPromptShortcut", e.ID}, e.Revision)
		}
		var delta map[string]any
		if err == nil {
			delta, err = f.ExportJSON(before)
		}
		f.Free()
		if err != nil {
			return err
		}
		b, err := json.Marshal(delta)
		if err != nil {
			return err
		}
		if _, err := index.Append(ctx, "application/octet-stream", streams.EncodeFrame(b)); err != nil {
			return err
		}
	}
	return nil
}
