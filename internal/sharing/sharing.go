// Package sharing publishes a static copy of conversations, as Lody's
// session sharing does: the histories are captured and stripped of what
// is not conversation content, packed with their images into objects
// under a manifest, uploaded to a deployment with an upload credential,
// sealed, and then published by the authenticated app. Readers open the
// copy at share.lody.ai with a bearer secret kept in the link's fragment.
//
// It ports Lody's packages/shared/src/session-share-*.ts (AGPL-3.0-only)
// as lody-ios vendors them, with uncompressed histories as iOS sends.
package sharing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

// The limits a package keeps.
const (
	MaxConversations   = 32
	MaxAttachments     = 64
	MaxImageBytes      = 20_000_000
	MaxManifestBytes   = 1024 * 1024
	MaxHistoryBytes    = 32 * 1024 * 1024
	MaxObjectBytes     = 100 * 1024 * 1024
	MaxDeploymentBytes = 256 * 1024 * 1024
	maxJSONDepth       = 64
)

// Where shares are read, and where their objects go.
var (
	Origin       = "https://share.lody.ai"
	UploadOrigin = "https://api.lody.ai"
)

var (
	resourceID   = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
	secretRe     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	accessFrag   = regexp.MustCompile(`#access=v1\.[a-f0-9]{64}`)
	deliveryKey  = regexp.MustCompile(`^[a-f0-9]{588}$`)
	errHistory   = errors.New("invalid share history")
	errJSON      = errors.New("invalid share JSON")
	errTooLarge  = errors.New("share object exceeds size limit")
	errPackage   = errors.New("share package exceeds size limit")
	errAttachKey = errors.New("invalid share attachment")
)

// ValidID is whether s may name a share, deployment, object or session.
func ValidID(s string) bool { return resourceID.MatchString(s) }

// encode writes JSON as JavaScript's JSON.stringify does for these values,
// without escaping HTML.
func encode(v any, limit int) ([]byte, error) {
	if err := assertJSON(v, 0); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if len(out) > limit {
		return nil, errTooLarge
	}
	return out, nil
}

func assertJSON(v any, depth int) error {
	if depth > maxJSONDepth {
		return errors.New("share JSON exceeds depth limit")
	}
	switch x := v.(type) {
	case nil, string, bool:
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return errJSON
		}
	case int, int64:
	case []any:
		for _, c := range x {
			if err := assertJSON(c, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, c := range x {
			if err := assertJSON(c, depth+1); err != nil {
				return err
			}
		}
	default:
		// The manifest's own structs.
		if _, err := json.Marshal(x); err != nil {
			return errJSON
		}
	}
	return nil
}

func obj(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func shallow(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// validateShape checks the stored history's frame: entries with unique
// ids and roles, and items with types. Unknown fields survive.
func validateShape(v any) ([]map[string]any, error) {
	if err := assertJSON(v, 0); err != nil {
		return nil, err
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errHistory
	}
	ids := map[string]bool{}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		entry, ok := obj(e)
		if !ok {
			return nil, errHistory
		}
		id, _ := entry["id"].(string)
		_, role := entry["role"].(string)
		if id == "" || ids[id] || !role {
			return nil, errHistory
		}
		if items, present := entry["items"]; present {
			arr, ok := items.([]any)
			if !ok {
				return nil, errHistory
			}
			for _, it := range arr {
				m, ok := obj(it)
				if !ok {
					return nil, errHistory
				}
				if _, ok := m["type"].(string); !ok {
					return nil, errHistory
				}
			}
		}
		ids[id] = true
		out = append(out, entry)
	}
	return out, nil
}

// project keeps what a share shows of a history. Capturing (omit) drops
// task proposals, terminals and their output, permission requests, and
// the entries' delivery bookkeeping; reading refuses task proposals.
func project(v any, omit bool) ([]map[string]any, error) {
	history, err := validateShape(v)
	if err != nil {
		return nil, err
	}
	var blocks func(items []any) ([]any, error)
	blocks = func(items []any) ([]any, error) {
		out := make([]any, 0, len(items))
		for _, it := range items {
			item, ok := obj(it)
			if !ok {
				out = append(out, it)
				continue
			}
			typ, _ := item["type"].(string)
			if typ == "system_notice" && item["name"] == "task_proposal" {
				if !omit {
					return nil, errors.New("task proposals are not allowed in share history")
				}
				continue
			}
			result := shallow(item)
			if omit {
				if typ == "terminal" || typ == "terminal_output" {
					continue
				}
				if typ == "tool_call" {
					delete(result, "permissionRequest")
				}
			}
			for _, key := range []string{"items", "content", "inputBlocks"} {
				if arr, ok := item[key].([]any); ok {
					b, err := blocks(arr)
					if err != nil {
						return nil, err
					}
					result[key] = b
				}
			}
			if typ == "tool_call" {
				if content, ok := result["content"].([]any); ok {
					for _, blk := range content {
						if m, ok := obj(blk); ok && m["type"] == "terminal_command" {
							// The first command is the fallback for an omitted title.
							if cmd, ok := m["command"].(string); ok && omit && result["title"] == cmd {
								delete(result, "title")
							}
							break
						}
					}
				}
			}
			out = append(out, result)
		}
		return out, nil
	}
	out := make([]map[string]any, 0, len(history))
	for _, entry := range history {
		result := shallow(entry)
		if omit {
			for _, k := range []string{"read", "userId", "acpTurnId", "userTurnId", "fileDiff", "inputConfig"} {
				delete(result, k)
			}
		}
		if items, ok := entry["items"].([]any); ok {
			b, err := blocks(items)
			if err != nil {
				return nil, err
			}
			result["items"] = b
		}
		if config, ok := obj(entry["inputConfig"]); ok {
			if inputs, ok := config["inputBlocks"].([]any); ok {
				b, err := blocks(inputs)
				if err != nil {
					return nil, err
				}
				next := map[string]any{}
				if !omit {
					next = shallow(config)
				}
				next["inputBlocks"] = b
				result["inputConfig"] = next
			}
		}
		out = append(out, result)
	}
	return out, nil
}

// CaptureHistory is the copy of a stored history a share publishes, with
// any share credential it quotes redacted. The live history is untouched.
func CaptureHistory(v any) ([]any, error) {
	projected, err := project(v, true)
	if err != nil {
		return nil, err
	}
	b, err := encode(projected, math.MaxInt)
	if err != nil {
		return nil, err
	}
	b = accessFrag.ReplaceAll(b, []byte("#access=omitted"))
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Manifest describes a deployment: its conversations, attachments and
// the objects holding them.
type Manifest struct {
	FormatVersion        int            `json:"formatVersion"`
	HistoryFormatVersion int            `json:"historyFormatVersion"`
	CapturedAt           string         `json:"capturedAt"`
	RootConversationID   string         `json:"rootConversationId"`
	Conversations        []Conversation `json:"conversations"`
	Attachments          []Attachment   `json:"attachments"`
	Objects              []Object       `json:"objects"`
}

type Conversation struct {
	ID                     string `json:"id"`
	Title                  string `json:"title"`
	HistoryObjectID        string `json:"historyObjectId"`
	ParentConversationID   string `json:"parentConversationId,omitempty"`
	OpenedByConversationID string `json:"openedByConversationId,omitempty"`
	ChildSessionPlacement  string `json:"childSessionPlacement,omitempty"`
}

type Attachment struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	FileName string `json:"fileName"`
	ObjectID string `json:"objectId"`
}

type Object struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	SizeBytes int    `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// SourceID maps a session to its conversation in a share.
type SourceID struct {
	SourceID       string `json:"sourceId"`
	ConversationID string `json:"conversationId"`
}

// Source is a conversation to share: its stored history and where it
// hangs in the tree.
type Source struct {
	SourceID         string
	Title            string
	History          any
	ParentSourceID   string
	OpenedBySourceID string
	SidePanel        bool
}

// AttachmentSource is a typed image or file reference to copy.
type AttachmentSource struct {
	ConversationSourceID string
	Kind                 string // image or file
	Reference            map[string]any
}

// Prepared is a frozen package, ready to upload and to retry.
type Prepared struct {
	Manifest      Manifest
	ManifestBytes []byte
	ManifestHash  string
	Objects       map[string][]byte
	SourceIDs     []SourceID
}

// Options say what to package.
type Options struct {
	RootSourceID   string
	Sources        []Source
	Previous       []SourceID // the published mapping, kept for stable ids
	CapturedAt     time.Time
	OmissionText   string // what stands in for a file attachment
	ReadAttachment func(ctx context.Context, a AttachmentSource) (data []byte, mediaType string, err error)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// clipTitle keeps a title within the 1024 bytes a manifest allows.
func clipTitle(s string) string {
	if len(s) <= 1024 {
		return s
	}
	s = s[:1024]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Prepare captures the sources and packs them, reading each image once.
func Prepare(ctx context.Context, o Options) (*Prepared, error) {
	if len(o.Sources) == 0 || len(o.Sources) > MaxConversations {
		return nil, errors.New("invalid share selection")
	}
	ids := map[string]string{}
	reserved := map[string]bool{}
	for _, e := range o.Previous {
		if _, dup := ids[e.SourceID]; dup || reserved[e.ConversationID] || !ValidID(e.ConversationID) {
			return nil, errors.New("invalid share identity")
		}
		ids[e.SourceID], reserved[e.ConversationID] = e.ConversationID, true
	}
	type captured struct {
		Source
		id      string
		history []any
	}
	next, total := 1, 0
	selected := map[string]bool{}
	var caps []captured
	for _, s := range o.Sources {
		if selected[s.SourceID] {
			return nil, errors.New("duplicate share selection")
		}
		selected[s.SourceID] = true
		id := ids[s.SourceID]
		if id == "" {
			for reserved[fmt.Sprintf("c%d", next)] {
				next++
			}
			id = fmt.Sprintf("c%d", next)
			next++
			ids[s.SourceID], reserved[id] = id, true
		}
		history, err := CaptureHistory(s.History)
		if err != nil {
			return nil, err
		}
		b, err := encode(history, MaxHistoryBytes)
		if err != nil {
			return nil, err
		}
		if total += len(b); total > MaxDeploymentBytes {
			return nil, errPackage
		}
		if _, err := validateShape(history); err != nil {
			return nil, err
		}
		caps = append(caps, captured{s, id, history})
	}
	if !selected[o.RootSourceID] {
		return nil, errors.New("share root not selected")
	}
	p := &Prepared{Objects: map[string][]byte{}}
	var inventory []Object
	attachments := []Attachment{}
	totalBytes := 0
	add := func(id string, b []byte, mediaType string) (Object, error) {
		if err := ctx.Err(); err != nil {
			return Object{}, err
		}
		if len(b) > MaxObjectBytes {
			return Object{}, errTooLarge
		}
		if totalBytes += len(b); totalBytes > MaxDeploymentBytes {
			return Object{}, errPackage
		}
		owned := bytes.Clone(b)
		d := Object{ID: id, MediaType: mediaType, SizeBytes: len(owned), SHA256: digest(owned)}
		inventory = append(inventory, d)
		p.Objects[id] = owned
		return d, nil
	}
	type copyJob struct {
		id         string
		source     AttachmentSource
		references []map[string]any
	}
	copies := map[string]*copyJob{}
	var order []*copyJob
	copyAttachment := func(ref map[string]any, kind, sourceID string) error {
		field := "imageId"
		if kind == "file" {
			field = "fileId"
		}
		objID, ok := ref[field].(string)
		if !ok {
			return errAttachKey
		}
		storage := ref["storageSessionId"]
		if storage == nil {
			storage = sourceID
		}
		keyBytes, _ := json.Marshal([]any{kind, storage, ref["machineId"], objID})
		key := string(keyBytes)
		job := copies[key]
		if job == nil {
			if len(copies) >= MaxAttachments {
				return errors.New("too many share attachments")
			}
			id := fmt.Sprintf("a%d", len(copies)+1)
			job = &copyJob{id: id, source: AttachmentSource{ConversationSourceID: sourceID, Kind: kind, Reference: ref}}
			copies[key] = job
			order = append(order, job)
			name, _ := ref["fileName"].(string)
			if name == "" {
				name = id
			}
			attachments = append(attachments, Attachment{ID: id, Kind: kind, FileName: name, ObjectID: id})
		}
		job.references = append(job.references, ref)
		return nil
	}
	omission := firstNonEmpty(o.OmissionText, "File attachment not included in this share")
	var visit func(v any, sourceID string) error
	visit = func(v any, sourceID string) error {
		if arr, ok := v.([]any); ok {
			for _, c := range arr {
				if err := visit(c, sourceID); err != nil {
					return err
				}
			}
			return nil
		}
		m, ok := obj(v)
		if !ok {
			return nil
		}
		switch m["type"] {
		case "file":
			// File attachments are not shared: the block becomes a note.
			for k := range m {
				delete(m, k)
			}
			m["type"], m["text"] = "text", omission
			return nil
		case "image":
			if _, ok := m["imageId"].(string); ok {
				if err := copyAttachment(m, "image", sourceID); err != nil {
					return err
				}
			}
		case "image_group":
			if images, ok := m["images"].([]any); ok {
				for _, im := range images {
					ref, ok := obj(im)
					if !ok {
						return errAttachKey
					}
					if err := copyAttachment(ref, "image", sourceID); err != nil {
						return err
					}
				}
			}
		}
		for _, key := range []string{"items", "content", "inputBlocks"} {
			if child, ok := m[key]; ok {
				if err := visit(child, sourceID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, s := range caps {
		for _, e := range s.history {
			entry, _ := obj(e)
			if items, ok := entry["items"]; ok {
				if err := visit(items, s.SourceID); err != nil {
					return nil, err
				}
			}
			if config, ok := obj(entry["inputConfig"]); ok {
				if inputs, ok := config["inputBlocks"]; ok {
					if err := visit(inputs, s.SourceID); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for _, job := range order {
		if o.ReadAttachment == nil {
			return nil, errors.New("share attachment unavailable")
		}
		data, mediaType, err := o.ReadAttachment(ctx, job.source)
		if err != nil {
			return nil, err
		}
		if job.source.Kind == "image" && len(data) > MaxImageBytes {
			return nil, errors.New("share image exceeds size limit")
		}
		d, err := add(job.id, data, mediaType)
		if err != nil {
			return nil, err
		}
		field := "imageId"
		if job.source.Kind == "file" {
			field = "fileId"
		}
		for _, ref := range job.references {
			if sum, ok := ref["sha256"].(string); ok && sum != d.SHA256 {
				return nil, errors.New("share attachment checksum mismatch")
			}
			ref[field], ref["sizeBytes"], ref["mimeType"] = job.id, float64(d.SizeBytes), mediaType
			delete(ref, "storageSessionId")
			delete(ref, "machineId")
			delete(ref, "sourcePath")
			if job.source.Kind == "file" {
				ref["transport"] = "r2"
			}
		}
	}
	var conversations []Conversation
	for i, s := range caps {
		objID := fmt.Sprintf("h%d", i+1)
		raw, err := encode(s.history, MaxHistoryBytes)
		if err != nil {
			return nil, err
		}
		if _, err := add(objID, raw, "application/json"); err != nil {
			return nil, err
		}
		c := Conversation{ID: s.id, Title: clipTitle(s.Title), HistoryObjectID: objID}
		if s.ParentSourceID != "" && selected[s.ParentSourceID] {
			c.ParentConversationID = ids[s.ParentSourceID]
		}
		if s.OpenedBySourceID != "" && selected[s.OpenedBySourceID] {
			c.OpenedByConversationID = ids[s.OpenedBySourceID]
		}
		if c.ParentConversationID != "" && s.SidePanel {
			c.ChildSessionPlacement = "side-panel"
		}
		conversations = append(conversations, c)
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].ID < inventory[j].ID })
	p.Manifest = Manifest{
		FormatVersion: 1, HistoryFormatVersion: 1,
		CapturedAt:         o.CapturedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		RootConversationID: ids[o.RootSourceID],
		Conversations:      conversations, Attachments: attachments, Objects: inventory,
	}
	b, err := encode(p.Manifest, MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	p.ManifestBytes, p.ManifestHash = b, digest(b)
	for _, s := range caps {
		p.SourceIDs = append(p.SourceIDs, SourceID{s.SourceID, s.id})
	}
	return p, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// NewSecret is a fresh credential: 32 random bytes in lowercase hex.
func NewSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// IsSecret is whether s has a credential's form.
func IsSecret(s string) bool { return secretRe.MatchString(s) }

// HashSecret is what the server keeps of a credential.
func HashSecret(secret string) (string, error) {
	if !IsSecret(secret) {
		return "", errors.New("invalid share credential")
	}
	return digest([]byte("lody-session-share-access-v1:" + secret)), nil
}

// URL is a share's link, its credential in the fragment.
func URL(shareID, secret string) (string, error) {
	if !ValidID(shareID) || !IsSecret(secret) {
		return "", errors.New("invalid share link")
	}
	return Origin + "/s/" + shareID + "#access=v1." + secret, nil
}

// Upload sends a package's objects, one at a time, then seals the
// deployment with its manifest. It never publishes.
func Upload(ctx context.Context, client *http.Client, deploymentID, uploadSecret string, p *Prepared, progress func(done, total int)) error {
	if !ValidID(deploymentID) || !IsSecret(uploadSecret) {
		return errors.New("invalid share deployment")
	}
	if len(p.Manifest.Attachments) > MaxAttachments {
		return errors.New("too many share attachments")
	}
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	send := func(method, path, contentType string, body []byte) error {
		req, err := http.NewRequestWithContext(ctx, method, UploadOrigin+"/api/share-deployments/"+deploymentID+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+uploadSecret)
		req.Header.Set("Content-Type", contentType)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("share upload failed (%d)", res.StatusCode)
		}
		return nil
	}
	total, done := 0, 0
	for _, o := range p.Manifest.Objects {
		total += o.SizeBytes
	}
	for _, o := range p.Manifest.Objects {
		b := p.Objects[o.ID]
		if b == nil || len(b) != o.SizeBytes || digest(b) != o.SHA256 {
			return errors.New("incomplete share package")
		}
		if err := send(http.MethodPut, "/objects/"+o.ID, "application/octet-stream", b); err != nil {
			return err
		}
		done += o.SizeBytes
		if progress != nil {
			progress(done, total)
		}
	}
	return send(http.MethodPost, "/seal", "application/json", p.ManifestBytes)
}

// Envelope carries a reader secret to the CLI that asked for a share,
// encrypted to its key.
type Envelope struct {
	Version    int    `json:"version"`
	Origin     string `json:"origin"`
	Ciphertext string `json:"ciphertext"`
}

// EncryptDelivery seals secret for the requester's RSA key, labelled with
// the request and the origin so the envelope can't be moved.
func EncryptDelivery(publicKey, requestID, origin, secret string) (*Envelope, error) {
	if !IsSecret(secret) {
		return nil, errors.New("invalid share credential")
	}
	if !deliveryKey.MatchString(publicKey) {
		return nil, errors.New("invalid delivery key")
	}
	der, _ := hex.DecodeString(publicKey)
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("invalid delivery key")
	}
	label, err := encode([]any{"lody-share-delivery-v1", requestID, origin}, math.MaxInt)
	if err != nil {
		return nil, err
	}
	ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, []byte(secret), label)
	if err != nil {
		return nil, err
	}
	return &Envelope{Version: 1, Origin: origin, Ciphertext: hex.EncodeToString(ct)}, nil
}
