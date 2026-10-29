package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	loro "github.com/aholstenson/loro-go"
	"github.com/google/uuid"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localterm"
	"github.com/lbr77/lody-mygo/internal/streams"
)

const maxSession = 32 << 20

// Replica follows one session stream. OnChange runs on the replica's
// goroutine, at most every 100ms while history streams in.
type Replica struct {
	ID       string
	Conn     *cloud.Conn
	OnChange func(*View)
	// Local, when set, syncs the session with this machine's daemon over
	// its local data plane rather than with the cloud.
	Local *localplane.Conn
	local *localplane.Room // the room joined while following locally

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	doc     *loro.LoroDoc
	client  *streams.Client
	ready   bool
	status  string
	reason  string
	timer   *time.Timer
	sending bool

	firstReady chan struct{}
	readyOnce  sync.Once
	flushMu    sync.Mutex

	thoughts map[string]*span  // thoughts seen streaming, by entry/item
	steers   map[string]string // steer receipts, by user turn
}

// span is when a thought streamed: from its first text to its last.
type span struct {
	start, end time.Time
	length     int
}

// projectLocked projects the document with what only this client knows:
// thought timings and steer receipts. Callers hold r.mu.
func (r *Replica) projectLocked() *View {
	v := project(r.doc, r.status, r.reason)
	r.timeThoughts(v, time.Now())
	links, _ := deepValue(r.doc.GetMap(loro.AsContainerId("lodySteerLinks")).GetDeepValue()).(map[string]any)
	r.markSteers(v, links)
	return v
}

// timeThoughts records how long thoughts stream while the replica is live,
// and sets ThoughtFor on those it saw. Callers hold r.mu.
func (r *Replica) timeThoughts(v *View, now time.Time) {
	if r.thoughts == nil {
		r.thoughts = map[string]*span{}
	}
	for ei := range v.Entries {
		e := &v.Entries[ei]
		if e.Role != "assistant" {
			continue
		}
		for ii := range e.Items {
			it := &e.Items[ii]
			if it.Type != "thought" {
				continue
			}
			key := e.ID + "/" + it.ID
			sp := r.thoughts[key]
			if sp == nil {
				if e.Finished || !r.ready {
					continue // already there when we came: its timing is unknown
				}
				sp = &span{start: now, end: now, length: len(it.Text)}
				r.thoughts[key] = sp
			}
			if !e.Finished && len(it.Text) != sp.length {
				sp.length, sp.end = len(it.Text), now
			}
			it.ThoughtFor = sp.end.Sub(sp.start)
		}
	}
}

// Open starts following the session.
func Open(conn *cloud.Conn, id string, onChange func(*View)) *Replica {
	return OpenLocal(conn, nil, id, onChange)
}

// OpenLocal starts following the session through this machine's daemon
// when local is set, through the cloud otherwise.
func OpenLocal(conn *cloud.Conn, local *localplane.Conn, id string, onChange func(*View)) *Replica {
	r := &Replica{ID: id, Conn: conn, Local: local, OnChange: onChange, doc: loro.NewLoroDoc(), status: "syncing", firstReady: make(chan struct{})}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	go r.run()
	return r
}

// WaitReady waits until the replica first caught up with its stream.
func (r *Replica) WaitReady(ctx context.Context) error {
	select {
	case <-r.firstReady:
		return nil
	case <-r.ctx.Done():
		return errNotReady
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops following.
func (r *Replica) Close() {
	r.cancel()
	r.mu.Lock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
}

// View projects the current state.
func (r *Replica) View() *View {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.projectLocked()
}

func (r *Replica) setStatus(status, reason string) {
	r.mu.Lock()
	r.status, r.reason = status, reason
	r.mu.Unlock()
	r.emit(true)
}

// emit schedules OnChange, coalescing bursts of updates.
func (r *Replica) emit(now bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil || r.OnChange == nil {
		return
	}
	if now {
		if r.timer != nil {
			r.timer.Stop()
			r.timer = nil
		}
		go r.flush()
		return
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(100*time.Millisecond, r.flush)
	}
}

// flush projects and delivers the view. Deliveries are serialized so an
// older view never lands after a newer one.
func (r *Replica) flush() {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()
	r.mu.Lock()
	r.timer = nil
	if r.ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	v := r.projectLocked()
	r.mu.Unlock()
	r.OnChange(v)
}

func (r *Replica) run() {
	backoff := time.Second
	for r.ctx.Err() == nil {
		var err error
		if r.Local != nil {
			err = r.followLocal()
		} else {
			err = r.follow()
		}
		if r.ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		r.ready = false
		r.mu.Unlock()
		reason := "sync_failed"
		if err != nil {
			reason = err.Error()
		}
		r.setStatus("syncing", reason)
		if streams.IsUnauthorized(err) && r.Conn != nil {
			r.Conn.Invalidate()
		}
		if errors.Is(err, errLimit) {
			r.setStatus("offline", "session_limit")
			return
		}
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

var errLimit = errors.New("session: history exceeds the limit")

func (r *Replica) importBody(body []byte) error {
	frames, err := streams.DecodeFrames(body)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, frame := range frames {
		if _, err := r.doc.Import(frame); err != nil {
			return err
		}
	}
	return nil
}

func (r *Replica) follow() error {
	ctx := r.ctx
	client, err := r.Conn.Stream(ctx, "s:"+r.ID)
	if err != nil {
		return err
	}
	boot, err := client.Bootstrap(ctx)
	if err != nil {
		return err
	}
	// A fresh document per bootstrap: the snapshot covers what the old
	// one had, and imports are idempotent anyway.
	doc := loro.NewLoroDoc()
	size := 0
	if boot.Snapshot != nil {
		size += len(boot.Snapshot)
		snapshot, err := streams.Unpack(boot.Snapshot)
		if err != nil {
			return err
		}
		if _, err := doc.Import(snapshot); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.doc = doc
	r.client = client
	r.mu.Unlock()
	for _, u := range boot.Updates {
		if size += len(u); size > maxSession {
			return errLimit
		}
		if err := r.importBody(u); err != nil {
			return err
		}
	}
	offset, cursor, upToDate := boot.NextOffset, boot.Cursor, boot.UpToDate
	pages := 0
	for {
		if upToDate {
			r.mu.Lock()
			wasReady := r.ready
			r.ready = true
			r.status, r.reason = "live", ""
			r.mu.Unlock()
			r.readyOnce.Do(func() { close(r.firstReady) })
			r.emit(!wasReady)
			pages = 0
		}
		res, err := client.Read(ctx, offset, cursor, upToDate)
		if err != nil {
			return err
		}
		if len(res.Payload) > 0 {
			if size += len(res.Payload); size > maxSession {
				return errLimit
			}
			if err := r.importBody(res.Payload); err != nil {
				return err
			}
			r.emit(false)
		}
		if res.NextOffset == offset && !res.UpToDate {
			return errors.New("stalled_cursor")
		}
		if pages++; pages > 100 && !res.UpToDate {
			return errLimit
		}
		offset, cursor, upToDate = res.NextOffset, res.Cursor, res.UpToDate
		if res.Closed {
			return errors.New("stream_closed")
		}
		if len(res.Payload) == 0 && upToDate {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

// machineRPC asks the session's machine: over this computer's control
// socket when following locally, else through the cloud.
func (r *Replica) machineRPC(ctx context.Context, machine, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if r.Local != nil {
		return localterm.MachineRPC(ctx, r.Local.Workspace, method, params, timeout)
	}
	return r.Conn.MachineRPC(ctx, machine, method, params, timeout)
}

// followLocal follows the session through the daemon's data plane: it
// joins the session's document with what it has, takes what it missed,
// and imports what the daemon pushes until the connection ends.
func (r *Replica) followLocal() error {
	doc := loro.NewLoroDoc()
	room := localplane.DocRoom("session-" + r.ID)
	pushes := make(chan localplane.Payload, 256)
	// A fresh document has nothing, which the daemon takes as no version.
	joined, err := r.Local.Join(r.ctx, room, "", func(p localplane.Payload) {
		select {
		case pushes <- p:
		case <-r.ctx.Done():
		}
	}, nil)
	if err != nil {
		return err
	}
	defer r.Local.Leave(room)
	if joined.Payload != nil {
		data, err := joined.Payload.Bytes()
		if err != nil {
			return err
		}
		if _, err := doc.Import(data); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.doc, r.local = doc, &room
	wasReady := r.ready
	r.ready, r.status, r.reason = true, "live", ""
	r.mu.Unlock()
	r.readyOnce.Do(func() { close(r.firstReady) })
	r.emit(!wasReady)
	defer func() {
		r.mu.Lock()
		r.local = nil
		r.mu.Unlock()
	}()
	for {
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-r.Local.Done():
			return r.Local.Err()
		case p := <-pushes:
			data, err := p.Bytes()
			if err != nil {
				continue
			}
			r.mu.Lock()
			_, err = r.doc.Import(data)
			r.mu.Unlock()
			if err != nil {
				return err
			}
			r.emit(false)
		}
	}
}

// upload appends the document's changes since before.
func (r *Replica) upload(ctx context.Context, before *loro.VersionVector) error {
	r.mu.Lock()
	update, err := r.doc.Export(loro.UpdatesMode(before))
	client, room, have := r.client, r.local, ""
	if room != nil {
		have = base64.StdEncoding.EncodeToString(r.doc.OplogVv().Encode())
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if room != nil {
		// Through the daemon, which takes it on to the cloud when it can.
		return r.Local.Update(*room, have, localplane.DocUpdate(update))
	}
	_, err = client.Append(ctx, "application/octet-stream", streams.EncodeFrame(update))
	return err
}

// Send outcomes.
const (
	NotSent  = "not_sent" // nothing was written
	Unknown  = "unknown"  // the write may or may not have landed
	Uploaded = "uploaded" // durable; the machine picks it up from the stream
	Queued   = "queued"   // behind the running turn
	Steering = "steering" // written to steer the running turn; awaiting its receipt
	Accepted = "accepted" // the machine acknowledged it
)

// Turn is a message to send.
type Turn struct {
	Text      string
	UserID    string
	MachineID string
	AgentType string
	CLIType   string
	Resume    string
	ModelID   string // empty keeps the previous turn's model
	ModeID    string // the permission mode; empty keeps the previous one
	Queue     bool   // the session is busy: queue behind it
	// Guide steers the running turn with this message instead of queuing
	// it, for agents that acknowledge steering.
	Guide bool
	// Attachments are uploaded blocks ({type: "image", imageId, ...} or
	// {type: "file", fileId, ...}) sent after the text.
	Attachments []map[string]any
	// MCPServerIDs are the MCP servers the turn loads; nil keeps the
	// previous turn's.
	MCPServerIDs []string
	// TextSpans mark what mentions became in Text, which transcripts show
	// as chips: Lody's message spans.
	TextSpans []map[string]any
	// ConfigOptionValues are the agent's settings chosen for the turn, as
	// its reasoning effort and Fast: laid over the previous turn's.
	ConfigOptionValues map[string]any

	// MarkDispatch records the turn in the catalog, which wakes the machine.
	MarkDispatch func(ctx context.Context, sessionID, turnID string, queued bool) error
}

var errNotReady = errors.New("session: not synced yet")

// Send writes a user turn and hands it to the machine. It returns how far it
// got; a turn past Unknown is never resent automatically.
func (r *Replica) Send(ctx context.Context, t Turn) (string, error) {
	text := strings.TrimSpace(t.Text)
	if text == "" && len(t.Attachments) == 0 {
		return NotSent, errors.New("session: empty message")
	}
	if len(t.Attachments) > 16 {
		return NotSent, errors.New("session: too many attachments")
	}
	if utf8.RuneCountInString(text) > 32000 {
		return NotSent, errors.New("session: message is too long")
	}
	if t.UserID == "" || t.MachineID == "" || t.AgentType == "" || t.CLIType == "" {
		return NotSent, errors.New("session: missing machine or agent")
	}
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return NotSent, errNotReady
	}
	if r.sending {
		r.mu.Unlock()
		return NotSent, errors.New("session: a message is still sending")
	}
	r.sending = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.sending = false
		r.mu.Unlock()
	}()

	turnID := uuid.NewString()
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	r.mu.Lock()
	history := list(deepValue(r.doc.GetList(loro.AsContainerId("history")).GetDeepValue()))
	queueLen := r.doc.GetMovableList(loro.AsContainerId("mq")).Len()
	config := inputConfig(history, t, text)
	// A guide steers the unfinished reply; without one it goes the usual
	// way, never orphaned.
	target := ""
	if t.Guide {
		target = unfinishedReply(history)
	}
	queued := target == "" && (t.Queue || queueLen > 0 || busy(history))
	before := r.doc.OplogVv()
	var err error
	switch {
	case target != "":
		if err = r.doc.GetMap(loro.AsContainerId("lodySteerLinks")).InsertAny(turnID, map[string]any{"targetId": target, "mode": "native"}); err == nil {
			steered := map[string]any{"_lodyDeliveryKind": "steer"}
			for k, v := range config {
				steered[k] = v
			}
			err = r.appendUserTurn(turnID, t.UserID, timestamp, steered, "pending_apply")
		}
		r.setSteer(turnID, "confirming")
	case queued:
		err = r.pushQueued(turnID, text, t.UserID, timestamp, config)
	default:
		err = r.appendUserTurn(turnID, t.UserID, timestamp, config, "pending")
	}
	r.mu.Unlock()
	if err != nil {
		return NotSent, err
	}
	r.emit(true)
	if err := r.upload(ctx, before); err != nil {
		return Unknown, err
	}
	if target != "" {
		// History is durable; the receipt comes back on its own.
		go r.steer(t.MachineID, map[string]any{
			"sessionId":      r.ID,
			"expectedTurnId": target,
			"userTurnId":     turnID,
			"userId":         t.UserID,
			"timestamp":      timestamp,
			"inputConfig":    config,
		})
		return Steering, nil
	}
	if queued {
		if err := t.MarkDispatch(ctx, r.ID, turnID, true); err != nil {
			return Uploaded, err
		}
		return Queued, nil
	}
	if err := t.MarkDispatch(ctx, r.ID, turnID, false); err != nil {
		return Uploaded, err
	}
	reply, err := r.machineRPC(ctx, t.MachineID, "session/dispatch-turn", map[string]any{
		"sessionId":   r.ID,
		"userTurnId":  turnID,
		"userId":      t.UserID,
		"timestamp":   timestamp,
		"inputConfig": config,
	}, 15*time.Second)
	if err != nil {
		// The turn is durable; the machine also picks it up from the stream.
		return Uploaded, nil
	}
	var res struct {
		Accepted bool `json:"accepted"`
	}
	if json.Unmarshal(reply, &res) == nil && res.Accepted {
		return Accepted, nil
	}
	return Uploaded, nil
}

// busy reports whether a new turn has to wait: an unfinished reply, or a
// pending turn nothing answered yet.
func busy(history []any) bool {
	for _, h := range history {
		e := obj(h)
		if str(e["role"]) == "assistant" && e["finished"] != true {
			return true
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		e := obj(history[i])
		if str(e["role"]) == "user" {
			if str(e["status"]) != "pending" {
				return false
			}
			for _, later := range history[i+1:] {
				if str(obj(later)["role"]) == "assistant" {
					return false
				}
			}
			return true
		}
	}
	return false
}

// inputConfig builds the turn's configuration, carrying the previous turn's
// settings forward as the iOS runtime does.
func inputConfig(history []any, t Turn, text string) map[string]any {
	var previous map[string]any
	for i := len(history) - 1; i >= 0; i-- {
		if e := obj(history[i]); str(e["role"]) == "user" {
			previous = obj(e["inputConfig"])
			break
		}
	}
	if previous == nil {
		previous = map[string]any{}
	}
	var blocks []any
	if text != "" {
		block := map[string]any{"type": "text", "text": text}
		if len(t.TextSpans) > 0 {
			spans := make([]any, len(t.TextSpans))
			for i, s := range t.TextSpans {
				spans[i] = s
			}
			block["spans"] = spans
		}
		blocks = append(blocks, block)
	}
	for _, a := range t.Attachments {
		blocks = append(blocks, a)
	}
	config := map[string]any{
		"cliType":          t.CLIType,
		"agentType":        t.AgentType,
		"prompt":           text,
		"inputBlocks":      blocks,
		"mcpServerIds":     []any{},
		"taskToolsEnabled": false,
	}
	if ids := list(previous["mcpServerIds"]); ids != nil {
		config["mcpServerIds"] = ids
	}
	if t.MCPServerIDs != nil {
		ids := make([]any, len(t.MCPServerIDs))
		for i, id := range t.MCPServerIDs {
			ids[i] = id
		}
		config["mcpServerIds"] = ids
	}
	if v, ok := previous["taskToolsEnabled"].(bool); ok {
		config["taskToolsEnabled"] = v
	}
	mode := t.ModeID
	if mode == "" {
		mode = str(previous["modeId"])
	}
	if mode != "" {
		config["modeId"] = mode
	}
	model := t.ModelID
	if model == "" {
		model = str(previous["modelId"])
	}
	if model != "" {
		config["modelId"] = model
	}
	opts := map[string]any{}
	for k, v := range obj(previous["configOptionValues"]) {
		opts[k] = v
	}
	for k, v := range t.ConfigOptionValues {
		opts[k] = v
	}
	if len(opts) > 0 {
		config["configOptionValues"] = opts
	}
	if t.Resume != "" {
		config["resume"] = t.Resume
	}
	if role := previous["agentRoleId"]; role != nil {
		changed := str(previous["cliType"]) != t.CLIType || str(previous["agentType"]) != t.AgentType ||
			str(previous["modelId"]) != model || str(previous["modeId"]) != mode
		if changed {
			config["agentRoleId"] = nil
		} else {
			config["agentRoleId"] = role
			if rev := previous["agentRoleRevision"]; rev != nil {
				config["agentRoleRevision"] = rev
			}
		}
	}
	return config
}

// appendUserTurn pushes a user entry onto history. Callers hold r.mu.
func (r *Replica) appendUserTurn(id, userID, timestamp string, config map[string]any, status string) error {
	history := r.doc.GetList(loro.AsContainerId("history"))
	entry, err := history.InsertLoroMap(history.Len())
	if err != nil {
		return err
	}
	for k, v := range map[string]any{
		"id": id, "role": "user", "userId": userID, "timestamp": timestamp,
		"status": status, "read": false, "finished": true, "fileDiff": []any{},
	} {
		if err := entry.InsertAny(k, v); err != nil {
			return err
		}
	}
	items, err := entry.GetOrCreateLoroList("items")
	if err != nil {
		return err
	}
	for _, b := range list(config["inputBlocks"]) {
		item, err := items.InsertLoroMap(items.Len())
		if err != nil {
			return err
		}
		for k, v := range obj(b) {
			if k == "text" {
				t, err := item.GetOrCreateLoroText("text")
				if err != nil {
					return err
				}
				if err := t.Insert(0, fmt.Sprint(v)); err != nil {
					return err
				}
				continue
			}
			if err := item.InsertAny(k, v); err != nil {
				return err
			}
		}
	}
	input, err := entry.GetOrCreateLoroMap("inputConfig")
	if err != nil {
		return err
	}
	for k, v := range config {
		if err := input.InsertAny(k, v); err != nil {
			return err
		}
	}
	r.doc.Commit()
	return nil
}

// pushQueued adds a turn to the message queue. Callers hold r.mu.
func (r *Replica) pushQueued(id, text, userID, timestamp string, config map[string]any) error {
	mq := r.doc.GetMovableList(loro.AsContainerId("mq"))
	item, err := mq.InsertLoroMap(mq.Len())
	if err != nil {
		return err
	}
	for k, v := range map[string]any{"task": text, "userId": userID, "userTurnId": id, "timestamp": timestamp} {
		if err := item.InsertAny(k, v); err != nil {
			return err
		}
	}
	cfg, err := item.GetOrCreateLoroMap("acpSessionConfig")
	if err != nil {
		return err
	}
	for k, v := range config {
		if err := cfg.InsertAny(k, v); err != nil {
			return err
		}
	}
	if err := cfg.InsertAny("chainDepth", float64(0)); err != nil {
		return err
	}
	r.doc.Commit()
	return nil
}

// Stop cancels the running turn.
func (r *Replica) Stop(ctx context.Context, machineID string) error {
	r.mu.Lock()
	history := list(deepValue(r.doc.GetList(loro.AsContainerId("history")).GetDeepValue()))
	r.mu.Unlock()
	turnID := ""
	for i := len(history) - 1; i >= 0; i-- {
		if e := obj(history[i]); str(e["role"]) == "assistant" && e["finished"] != true {
			turnID = str(e["id"])
			break
		}
	}
	if turnID == "" {
		return errors.New("session: nothing is running")
	}
	reply, err := r.machineRPC(ctx, machineID, "session/cancel", map[string]any{"sessionId": r.ID, "turnId": turnID}, 35*time.Second)
	if err != nil {
		return err
	}
	var res struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	json.Unmarshal(reply, &res)
	if !res.Success {
		if res.Error == "" {
			res.Error = "stop_failed"
		}
		return errors.New(res.Error)
	}
	return nil
}

// Answer selects an option of a pending permission request.
func (r *Replica) Answer(ctx context.Context, entryID, itemID, requestID, optionID string) error {
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return errNotReady
	}
	item := r.locateItem(entryID, itemID)
	if item == nil {
		r.mu.Unlock()
		return errors.New("session: the request is gone")
	}
	outcome := map[string]any{"outcome": "selected", "optionId": optionID}
	before := r.doc.OplogVv()
	var err error
	if req, ok := item.GetLoroMap("permissionRequest"); ok && req != nil {
		current := obj(deepValue(req.GetDeepValue()))
		if fmt.Sprint(orEmpty(current["requestId"])) != requestID || current["outcome"] != nil {
			r.mu.Unlock()
			return errors.New("session: the request was already answered")
		}
		err = req.InsertAny("outcome", outcome)
	} else {
		v, _ := item.GetAny("permissionRequest")
		current := obj(normalize(v))
		if current == nil || fmt.Sprint(orEmpty(current["requestId"])) != requestID || current["outcome"] != nil {
			r.mu.Unlock()
			return errors.New("session: the request was already answered")
		}
		next := map[string]any{}
		for k, v := range current {
			next[k] = v
		}
		next["outcome"] = outcome
		err = item.InsertAny("permissionRequest", next)
	}
	if err == nil {
		r.doc.Commit()
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.emit(true)
	return r.upload(ctx, before)
}

// SetNoticeMeta merges fields into a system notice's metadata, as a
// proposal's outcome is written back to it.
func (r *Replica) SetNoticeMeta(ctx context.Context, entryID, itemID string, fields map[string]any) error {
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return errNotReady
	}
	item := r.locateItem(entryID, itemID)
	if item == nil {
		r.mu.Unlock()
		return errors.New("session: the notice is gone")
	}
	before := r.doc.OplogVv()
	var err error
	if meta, ok := item.GetLoroMap("meta"); ok && meta != nil {
		for k, v := range fields {
			if err = meta.InsertAny(k, v); err != nil {
				break
			}
		}
	} else {
		v, _ := item.GetAny("meta")
		next := map[string]any{}
		for k, v := range obj(normalize(v)) {
			next[k] = v
		}
		for k, v := range fields {
			next[k] = v
		}
		err = item.InsertAny("meta", next)
	}
	if err == nil {
		r.doc.Commit()
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.emit(true)
	return r.upload(ctx, before)
}

// locateItem finds an item map by entry and item id. Callers hold r.mu.
func (r *Replica) locateItem(entryID, itemID string) *loro.LoroMap {
	history := r.doc.GetList(loro.AsContainerId("history"))
	for i := 0; i < int(history.Len()); i++ {
		entry, ok := history.GetLoroMap(uint32(i))
		if !ok || entry == nil {
			continue
		}
		id, _ := entry.GetString("id")
		if id == "" {
			id = identityAt(history, i, "")
		}
		if id != entryID {
			continue
		}
		items, ok := entry.GetLoroList("items")
		if !ok || items == nil {
			return nil
		}
		for j := 0; j < int(items.Len()); j++ {
			item, ok := items.GetLoroMap(uint32(j))
			if !ok || item == nil {
				continue
			}
			if tc, _ := item.GetString("toolCallId"); tc == itemID || identityAt(items, j, "") == itemID {
				return item
			}
		}
	}
	return nil
}

// ReadHistory reads a session's history as the server holds it, for a
// share to copy: a fresh replica that catches up and goes, so no unsent
// local edit is ever exported.
func ReadHistory(ctx context.Context, conn *cloud.Conn, id string) ([]any, error) {
	r := Open(conn, id, nil)
	defer r.Close()
	if err := r.WaitReady(ctx); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return list(deepValue(r.doc.GetList(loro.AsContainerId("history")).GetDeepValue())), nil
}
