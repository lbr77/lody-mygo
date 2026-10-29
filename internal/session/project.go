// Package session replicates one Lody session, a Loro document on the
// stream "s:<session id>", and projects its history for display.
package session

import (
	"fmt"
	"sort"
	"strings"
	"time"

	loro "github.com/aholstenson/loro-go"
)

// Item is one part of an entry: text, thinking, a tool call, a plan...
type Item struct {
	ID   string
	Type string
	Text string // text, thought; markdown of proposed_plan; notice message
	// Notice is a system notice's metadata; Title is its name.
	Notice map[string]any
	// Spans are a user text's mentions as sent, which show as chips, and
	// Latest whether a proposed plan is the latest.
	Spans  []TextSpan
	Latest bool

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

	// file
	TextPreview bool    // it previews as text
	Transport   string  // r2, in the relay store, or local, still on its machine
	FileMachine string  // the machine a local file waits on
	UploadedAt  float64 // when it was uploaded, in Unix milliseconds
	Goal        *Goal   // goal
}

type Permission struct {
	RequestID string
	Pending   bool
	Question  bool
	Options   []Option
	// Questions are what an AskUserQuestion request asks, and Answers
	// what its outcome answered; nil for other requests.
	Questions *AskQuestions
	Answers   AskAnswers
}

type Option struct {
	ID, Name, Kind string
}

type PlanEntry struct {
	Content, Status string
}

// Subagent is a task the agent started: a subagent, or a background
// command or watcher.
type Subagent struct {
	TaskID, ParentTaskID                       string
	Description, Status, Summary, Error, Actor string
	TaskType, SubagentType                     string
	State                                      string // pending, running, completed, failed, cancelled or unknown
	Background, SkipTranscript                 bool
	Started, Ended                             float64 // epoch seconds
	LastTool, Progress                         string
	Items                                      []Item
}

// Running reports whether the task is still worth waiting on.
func (s *Subagent) Running() bool { return s.State == "running" || s.State == "pending" }

// Bash reports whether the task runs a shell command.
func (s *Subagent) Bash() bool { return s.Actor == "local_bash" || s.TaskType == "local_bash" }

// Author is who wrote a message the user didn't: another session's agent.
type Author struct {
	Agent     bool
	Name      string
	AgentType string
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
	UserID     string  // who wrote a user message
	Author     *Author // set when an agent wrote it
	Items      []Item
	// A steer is a message given to a running turn: SteerMode is native or
	// interrupt, SteerState confirming, accepted or unknown.
	Steer      bool
	SteerMode  string
	SteerState string
	FileDiffs  []FileDiff
	StartedAt  float64
	EndedAt    float64
	// AcpTurnID is the agent's own id of a reply's turn, which a fork
	// starts from.
	AcpTurnID string
	// Config is a user message's input, as it was sent.
	Config map[string]any
	// ScheduleRun marks a user message a schedule's run posted.
	ScheduleRun *ScheduleRun
}

// ScheduleRun is the schedule run a user message is.
type ScheduleRun struct {
	ScheduleID, TurnID, Title string
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
	ModelID      string         // of the last user turn
	ModeID       string         // the permission mode of the last user turn
	ConfigValues map[string]any // the agent settings of the last user turn
	Preview      *Preview
	Goal         *Goal // the latest goal the agent reported
}

// Goal is an objective the agent pursues across turns, as Codex's goals.
type Goal struct {
	ThreadID    string
	Objective   string
	Status      string // active, paused, budgetLimited, blocked, usageLimited, complete or cleared
	TokenBudget float64
	TokensUsed  float64
	Seconds     float64 // time spent on it
}

// Preview is a web server the session runs that it can show, as the
// session's agent reports it, and the tunnel that shows it, if any.
type Preview struct {
	Target     PreviewTarget
	Candidate  string // reported, validating, available or invalid
	Connection string // creating, active, closed or failed
	PublicURL  string // the tunnel's, while it is active
}

// PreviewTarget is a server on the machine's loopback.
type PreviewTarget struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Path     string `json:"path,omitempty"`
}

// Label is the server's host and port.
func (t PreviewTarget) Label() string { return fmt.Sprintf("%s:%d", t.Host, t.Port) }

func previewTarget(v any) (PreviewTarget, bool) {
	m := obj(v)
	t := PreviewTarget{Protocol: str(m["protocol"]), Host: strings.TrimSpace(str(m["host"])), Port: int(num(m["port"])), Path: str(m["path"])}
	switch strings.ToLower(t.Host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
	default:
		return t, false
	}
	if (t.Protocol != "http" && t.Protocol != "https") || t.Port < 1 || t.Port > 65535 || float64(t.Port) != num(m["port"]) {
		return t, false
	}
	if t.Path != "" && !strings.HasPrefix(t.Path, "/") {
		return t, false
	}
	return t, true
}

// previewOf reads the session doc's preview state as lody-ios does: the
// candidate's target while one is reported, else the tunnel's.
func previewOf(state map[string]any) *Preview {
	candidate, connection := obj(state["candidate"]), obj(state["connection"])
	p := &Preview{Candidate: str(candidate["status"]), Connection: str(connection["status"])}
	ok := false
	switch p.Candidate {
	case "reported", "validating", "available":
		p.Target, ok = previewTarget(candidate["target"])
	}
	if !ok {
		switch p.Connection {
		case "creating", "active":
			p.Target, ok = previewTarget(connection["target"])
		}
	}
	if !ok {
		return nil
	}
	if p.Connection == "active" {
		p.PublicURL = str(connection["publicUrl"])
	}
	return p
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
		it.Spans = textSpans(it.Text, raw["spans"])
	case "system_notice":
		it.Title = str(raw["name"])
		it.Text = str(obj(raw["meta"])["message"])
		it.Notice = obj(raw["meta"])
	case "proposed_plan":
		it.Text = str(raw["markdown"])
		it.Status = str(raw["status"])
		it.Latest = raw["isLatest"] != false
	case "goal":
		it.Text, it.Status = str(raw["objective"]), str(raw["status"])
		it.Goal = &Goal{ThreadID: str(raw["threadId"]), Objective: str(raw["objective"]), Status: str(raw["status"]),
			TokenBudget: num(raw["tokenBudget"]), TokensUsed: num(raw["tokensUsed"]), Seconds: num(raw["timeUsedSeconds"])}
	case "file":
		it.FileName = str(raw["fileName"])
		it.FileID = str(raw["fileId"])
		it.StorageSessionID = str(raw["storageSessionId"])
		it.MimeType = str(raw["mimeType"])
		it.SizeBytes = int64(num(raw["sizeBytes"]))
		it.TextPreview = raw["textPreview"] == true
		it.Transport = str(raw["transport"])
		it.FileMachine = str(raw["machineId"])
		it.UploadedAt = num(raw["uploadedAt"])
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
			qs := ParseAskQuestions(obj(req["_meta"]))
			p := &Permission{
				RequestID: fmt.Sprint(orEmpty(req["requestId"])),
				Pending:   req["outcome"] == nil,
				Question:  qs != nil || req["kind"] == "ask_user_question" || raw["kind"] == "ask_user_question",
				Questions: qs,
				Answers:   ExtractAskAnswers(qs, obj(req["outcome"])),
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
		run := obj(raw["run"])
		sa := &Subagent{
			TaskID:         str(raw["taskId"]),
			ParentTaskID:   str(raw["parentTaskId"]),
			Description:    str(raw["description"]),
			Status:         str(raw["status"]),
			Summary:        str(raw["summary"]),
			Error:          str(raw["error"]),
			Actor:          str(raw["actor"]),
			TaskType:       str(raw["taskType"]),
			SubagentType:   str(raw["subagentType"]),
			Background:     raw["isBackgrounded"] == true,
			SkipTranscript: raw["skipTranscript"] == true,
			Started:        num(raw["startedAtEpochSeconds"]),
			Ended:          num(raw["endedAtEpochSeconds"]),
			LastTool:       firstOf(str(obj(run["progress"])["lastToolName"]), str(raw["lastToolName"])),
			Progress:       str(obj(run["progress"])["summary"]),
		}
		if sa.TaskID == "" {
			sa.TaskID = identity
		}
		// A normalized run's snapshot says how it stands; a legacy task has
		// only its status.
		sa.State = str(obj(run["snapshot"])["state"])
		if sa.State == "" {
			sa.State = sa.Status
			if sa.State == "in_progress" {
				sa.State = "running"
			}
		}
		if sa.State == "" {
			sa.State = "pending"
		}
		for i, r := range list(run["items"]) {
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

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// authorOf reads a message's author: only an agent's is of interest.
func authorOf(v any) *Author {
	a := obj(v)
	if a == nil || str(a["kind"]) != "agent" {
		return nil
	}
	name := str(obj(a["role"])["name"])
	if name == "" {
		name = firstOf(str(a["name"]), str(a["agentType"]), "Agent")
	}
	return &Author{Agent: true, Name: name, AgentType: str(a["agentType"])}
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
			UserID:     str(e["userId"]),
			Author:     authorOf(e["author"]),
			StartedAt:  num(e["startedAt"]),
			EndedAt:    num(e["endedAt"]),
			AcpTurnID:  str(e["acpTurnId"]),
		}
		if run := obj(e["scheduleRun"]); run != nil && str(run["scheduleId"]) != "" {
			entry.ScheduleRun = &ScheduleRun{ScheduleID: str(run["scheduleId"]), TurnID: str(run["turnId"]), Title: str(run["title"])}
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
				entry.Config = cfg
				view.ModelID = str(cfg["modelId"])
				view.ModeID = str(cfg["modeId"])
				view.ConfigValues = obj(cfg["configOptionValues"])
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
	for i := len(view.Entries) - 1; i >= 0 && view.Goal == nil; i-- {
		items := view.Entries[i].Items
		for j := len(items) - 1; j >= 0; j-- {
			if items[j].Goal != nil && items[j].Goal.ThreadID != "" {
				view.Goal = items[j].Goal
				break
			}
		}
	}
	sess := obj(deepValue(doc.GetMap(loro.AsContainerId("session")).GetDeepValue()))
	view.AwaitingUser = sess["awaitingUserSince"] != nil
	view.Preview = previewOf(obj(deepValue(doc.GetMap(loro.AsContainerId("preview")).GetDeepValue())))
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

// TextSpan is a mention in a sent text: the region of Text it became,
// in bytes, what kind it is and the label the user picked, as Lody's
// message spans.
type TextSpan struct {
	Start, End int
	Kind       string
	Label      string
	Target     string
	Mark       string
}

var spanKinds = map[string]bool{"file": true, "dir": true, "issue": true, "pr": true, "skill": true, "session": true, "command": true, "agent_role": true, "pasted_text": true}

// textSpans reads a text's spans, their UTF-16 offsets turned to bytes,
// keeping those in range, in order and apart, as Lody's
// sanitizeMessageTextSpans does.
func textSpans(text string, raw any) []TextSpan {
	list, _ := raw.([]any)
	if len(list) == 0 {
		return nil
	}
	// Byte offset of each UTF-16 unit boundary.
	var bytesAt []int
	for i, r := range text {
		bytesAt = append(bytesAt, i)
		if r >= 0x10000 {
			bytesAt = append(bytesAt, -1) // inside a surrogate pair
		}
	}
	bytesAt = append(bytesAt, len(text))
	var all []TextSpan
	for _, v := range list {
		m := obj(v)
		start, end := int(num(m["start"])), int(num(m["end"]))
		kind, label := str(m["kind"]), str(m["label"])
		if !spanKinds[kind] || label == "" || start < 0 || end <= start || end > len(bytesAt)-1 || bytesAt[start] < 0 || bytesAt[end] < 0 {
			continue
		}
		all = append(all, TextSpan{Start: bytesAt[start], End: bytesAt[end], Kind: kind, Label: label, Target: str(m["target"]), Mark: str(m["mark"])})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Start < all[j].Start })
	var out []TextSpan
	for _, s := range all {
		if n := len(out); n > 0 && s.Start < out[n-1].End {
			continue
		}
		out = append(out, s)
	}
	return out
}
