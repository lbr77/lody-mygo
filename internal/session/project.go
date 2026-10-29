// Package session replicates one Lody session, a Loro document on the
// stream "s:<session id>", and projects its history for display.
package session

import (
	"fmt"
	"strings"
	"time"

	loro "github.com/aholstenson/loro-go"
)

// Item is one part of an entry: text, thinking, a tool call, a plan...
type Item struct {
	ID   string
	Type string
	Text string // text, thought; markdown of proposed_plan; notice message

	// tool_call
	Kind, Title, Status, Path string
	Added, Removed            int
	HasDetail                 bool
	Permission                *Permission
	Content                   []any // tool output blocks: diff, terminal, content
	RawInput, RawOutput       any

	// ThoughtFor is how long a thought streamed, when this client saw it
	// stream; zero otherwise.
	ThoughtFor time.Duration

	Plan     []PlanEntry // plan
	Subagent *Subagent   // subagent_task
	FileName string      // file, image

	// image and file
	ImageID, FileID  string
	StorageSessionID string // the session the attachment was uploaded to
	MimeType         string
	SizeBytes        int64
	Images           []Item // image_group
}

type Permission struct {
	RequestID string
	Pending   bool
	Question  bool
	Options   []Option
}

type Option struct {
	ID, Name, Kind string
}

type PlanEntry struct {
	Content, Status string
}

type Subagent struct {
	Description, Status, Summary, Error, Actor string
	Items                                      []Item
}

// Entry is one turn of the conversation.
type Entry struct {
	ID         string
	Role       string // user or assistant
	Status     string
	Finished   bool
	Queued     bool
	Timestamp  string
	UserTurnID string
	Model      string
	Items      []Item
	// A steer is a message given to a running turn: SteerMode is native or
	// interrupt, SteerState confirming, accepted or unknown.
	Steer      bool
	SteerMode  string
	SteerState string
	FileDiffs  []FileDiff
	StartedAt  float64
	EndedAt    float64
}

type FileDiff struct {
	Path     string
	Add, Del int
}

// Text joins an entry's text items.
func (e *Entry) Text() string {
	var parts []string
	for _, it := range e.Items {
		if it.Type == "text" && it.Text != "" {
			parts = append(parts, it.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// View is a session projected for display.
type View struct {
	Status       string // syncing, live or offline
	Reason       string
	Entries      []Entry
	Working      bool
	AwaitingUser bool
	ModelID      string // of the last user turn
	ModeID       string // the permission mode of the last user turn
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	return 0
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func countDiff(content any) (path string, added, removed int) {
	for _, b := range list(content) {
		block := obj(b)
		if block == nil || block["type"] != "diff" {
			continue
		}
		if path == "" {
			path = str(block["path"])
		}
		before := strings.Split(fmt.Sprint(orEmpty(block["oldText"])), "\n")
		after := strings.Split(fmt.Sprint(orEmpty(block["newText"])), "\n")
		shared := map[string]bool{}
		for _, l := range before {
			shared[l] = true
		}
		for _, l := range after {
			if !shared[l] {
				added++
			}
		}
		target := map[string]bool{}
		for _, l := range after {
			target[l] = true
		}
		for _, l := range before {
			if !target[l] {
				removed++
			}
		}
	}
	return
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func summarizeItem(raw map[string]any, identity string) Item {
	typ := str(raw["type"])
	if typ == "" {
		typ = "unknown"
	}
	it := Item{ID: identity, Type: typ}
	switch typ {
	case "text", "thought":
		it.Text = str(raw["text"])
	case "system_notice":
		it.Title = str(raw["name"])
		it.Text = str(obj(raw["meta"])["message"])
	case "proposed_plan":
		it.Text = str(raw["markdown"])
		it.Status = str(raw["status"])
	case "file":
		it.FileName = str(raw["fileName"])
		it.FileID = str(raw["fileId"])
		it.StorageSessionID = str(raw["storageSessionId"])
		it.MimeType = str(raw["mimeType"])
		it.SizeBytes = int64(num(raw["sizeBytes"]))
	case "image":
		it.FileName = str(raw["fileName"])
		it.ImageID = str(raw["imageId"])
		it.StorageSessionID = str(raw["storageSessionId"])
		it.MimeType = str(raw["mimeType"])
	case "image_group":
		for i, img := range list(raw["images"]) {
			if m := obj(img); m != nil && str(m["imageId"]) != "" {
				it.Images = append(it.Images, summarizeItem(map[string]any{
					"type": "image", "imageId": m["imageId"], "fileName": m["fileName"], "storageSessionId": m["storageSessionId"],
				}, fmt.Sprintf("%s/%d", identity, i)))
			}
		}
	case "tool_call":
		if id := str(raw["toolCallId"]); id != "" {
			it.ID = id
		}
		it.Kind = str(raw["kind"])
		if it.Kind == "" {
			it.Kind = "other"
		}
		it.Title = str(raw["title"])
		if it.Title == "" {
			it.Title = str(raw["toolName"])
		}
		it.Status = str(raw["status"])
		if it.Status == "" {
			it.Status = "pending"
		}
		it.Path, it.Added, it.Removed = countDiff(raw["content"])
		it.Content, it.RawInput, it.RawOutput = list(raw["content"]), raw["rawInput"], raw["rawOutput"]
		it.HasDetail = len(it.Content) > 0 || it.RawInput != nil || it.RawOutput != nil
		if req := obj(raw["permissionRequest"]); req != nil {
			p := &Permission{
				RequestID: fmt.Sprint(orEmpty(req["requestId"])),
				Pending:   req["outcome"] == nil,
				Question:  req["kind"] == "ask_user_question" || raw["kind"] == "ask_user_question" || req["_meta"] != nil && obj(req["_meta"])["questions"] != nil,
			}
			for _, o := range list(req["options"]) {
				opt := obj(o)
				p.Options = append(p.Options, Option{ID: str(opt["optionId"]), Name: str(opt["name"]), Kind: str(opt["kind"])})
			}
			it.Permission = p
		}
	case "plan":
		for _, e := range list(raw["entries"]) {
			entry := obj(e)
			status := str(entry["status"])
			if status == "" {
				status = "pending"
			}
			it.Plan = append(it.Plan, PlanEntry{Content: str(entry["content"]), Status: status})
		}
	case "subagent_task":
		sa := &Subagent{
			Description: str(raw["description"]),
			Status:      str(raw["status"]),
			Summary:     str(raw["summary"]),
			Error:       str(raw["error"]),
			Actor:       str(raw["actor"]),
		}
		for i, r := range list(obj(raw["run"])["items"]) {
			child := obj(r)
			switch str(child["type"]) {
			case "text", "thought", "tool_call", "plan":
				sa.Items = append(sa.Items, summarizeItem(child, fmt.Sprintf("%s/run:%d", identity, i)))
			}
		}
		it.Subagent = sa
	}
	return it
}

// identityAt is the id Lody gives an element of a list: its Loro op id.
func identityAt(l *loro.LoroList, i int, fallback string) string {
	if l == nil {
		return fallback
	}
	id := l.GetIdAt(uint32(i))
	if id == nil {
		return fallback
	}
	return fmt.Sprintf("%d:%d", id.Peer, id.Counter)
}

// normalize turns Loro's int64 values into float64, as JSON numbers.
func normalize(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = normalize(x[k])
		}
	}
	return v
}

func deepValue(v loro.LoroValue) any {
	out, _ := loro.ValueAsAny(v)
	return normalize(out)
}

// project builds the view of a document.
func project(doc *loro.LoroDoc, status, reason string) *View {
	history := doc.GetList(loro.AsContainerId("history"))
	raw := list(deepValue(history.GetDeepValue()))
	view := &View{Status: status, Reason: reason}

	entries := make([]Entry, 0, len(raw))
	for i, r := range raw {
		e := obj(r)
		if e == nil {
			continue
		}
		var items *loro.LoroList
		if m, ok := history.GetLoroMap(uint32(i)); ok && m != nil {
			items, _ = m.GetLoroList("items")
		}
		id := str(e["id"])
		if id == "" {
			id = identityAt(history, i, fmt.Sprintf("idx:%d", i))
		}
		role := str(e["role"])
		if role == "" {
			role = "assistant"
		}
		entryStatus := str(e["status"])
		if entryStatus == "" {
			entryStatus = "pending"
			if e["read"] == true {
				entryStatus = "seen"
			}
		}
		entry := Entry{
			ID:         id,
			Role:       role,
			Status:     entryStatus,
			Finished:   e["finished"] == true,
			Timestamp:  str(e["timestamp"]),
			UserTurnID: str(e["userTurnId"]),
			StartedAt:  num(e["startedAt"]),
			EndedAt:    num(e["endedAt"]),
		}
		if m := obj(e["modelInfo"]); m != nil {
			entry.Model = strings.TrimSpace(str(m["name"]))
			if entry.Model == "" {
				entry.Model = strings.TrimSpace(str(m["modelId"]))
			}
		}
		for j, it := range list(e["items"]) {
			if m := obj(it); m != nil {
				entry.Items = append(entry.Items, summarizeItem(m, identityAt(items, j, fmt.Sprintf("idx:%d:%d", i, j))))
			}
		}
		for _, d := range list(e["fileDiff"]) {
			diff := obj(d)
			if path := str(diff["filePath"]); path != "" {
				entry.FileDiffs = append(entry.FileDiffs, FileDiff{Path: path, Add: int(num(diff["add"])), Del: int(num(diff["del"]))})
			}
		}
		if role == "user" {
			if cfg := obj(e["inputConfig"]); cfg != nil {
				view.ModelID = str(cfg["modelId"])
				view.ModeID = str(cfg["modeId"])
				entry.Steer = str(cfg["_lodyDeliveryKind"]) == "steer"
			}
		}
		entries = append(entries, entry)
	}

	// A reply placed before its input by a late history sync goes back
	// after it, by its explicit link; arrival order is never used.
	users := map[string]bool{}
	for _, e := range entries {
		if e.Role == "user" {
			users[e.ID] = true
		}
	}
	replies := map[string][]Entry{}
	for _, e := range entries {
		if e.Role == "assistant" && users[e.UserTurnID] {
			replies[e.UserTurnID] = append(replies[e.UserTurnID], e)
		}
	}
	for _, e := range entries {
		if e.Role == "assistant" && users[e.UserTurnID] {
			continue
		}
		view.Entries = append(view.Entries, e)
		if e.Role == "user" {
			view.Entries = append(view.Entries, replies[e.ID]...)
		}
	}

	queue := list(deepValue(doc.GetMovableList(loro.AsContainerId("mq")).GetDeepValue()))
	for i, q := range queue {
		item := obj(q)
		turnID := str(item["userTurnId"])
		if turnID == "" || users[turnID] {
			continue
		}
		cfg := obj(item["acpSessionConfig"])
		entry := Entry{ID: turnID, Role: "user", Status: "queued", Queued: true, Timestamp: str(item["timestamp"])}
		if str(cfg["_lodySteerMode"]) == "interrupt" {
			entry.Status, entry.Steer, entry.SteerMode = "pending_apply", true, "interrupt"
		}
		blocks := list(cfg["inputBlocks"])
		if len(blocks) == 0 {
			blocks = []any{map[string]any{"type": "text", "text": str(item["task"])}}
		}
		for j, b := range blocks {
			if m := obj(b); m != nil {
				entry.Items = append(entry.Items, summarizeItem(m, fmt.Sprintf("queue:%d:%d", i, j)))
			}
		}
		view.Entries = append(view.Entries, entry)
	}

	view.Working = isWorking(raw, len(queue))
	sess := obj(deepValue(doc.GetMap(loro.AsContainerId("session")).GetDeepValue()))
	view.AwaitingUser = sess["awaitingUserSince"] != nil
	return view
}

// isWorking follows the iOS runtime: queued work, an unfinished reply, or a
// submitted turn without its first reply yet.
func isWorking(history []any, queued int) bool {
	if queued > 0 {
		return true
	}
	replied, latestUser := false, true
	for i := len(history) - 1; i >= 0; i-- {
		e := obj(history[i])
		switch str(e["role"]) {
		case "assistant":
			if e["finished"] != true {
				return true
			}
			replied = true
		case "user":
			if latestUser && !replied {
				switch str(e["status"]) {
				case "pending", "seen", "processing", "pending_apply":
					return true
				}
			}
			latestUser = false
		}
	}
	return false
}
