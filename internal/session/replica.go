package session

import (
	"context"
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
	"github.com/lbr77/lody-mygo/internal/streams"
)

const maxSession = 32 << 20

// Replica follows one session stream. OnChange runs on the replica's
// goroutine, at most every 100ms while history streams in.
type Replica struct {
	ID       string
	Conn     *cloud.Conn
	OnChange func(*View)

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
}

// Open starts following the session.
func Open(conn *cloud.Conn, id string, onChange func(*View)) *Replica {
	r := &Replica{ID: id, Conn: conn, OnChange: onChange, doc: loro.NewLoroDoc(), status: "syncing", firstReady: make(chan struct{})}
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
	return project(r.doc, r.status, r.reason)
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
	v := project(r.doc, r.status, r.reason)
	r.mu.Unlock()
	r.OnChange(v)
}

func (r *Replica) run() {
	backoff := time.Second
	for r.ctx.Err() == nil {
		err := r.follow()
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
		if streams.IsUnauthorized(err) {
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

// upload appends the document's changes since before.
func (r *Replica) upload(ctx context.Context, before *loro.VersionVector) error {
	r.mu.Lock()
	update, err := r.doc.Export(loro.UpdatesMode(before))
	client := r.client
	r.mu.Unlock()
	if err != nil {
		return err
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
	Queue     bool   // the session is busy: queue behind it

	// MarkDispatch records the turn in the catalog, which wakes the machine.
	MarkDispatch func(ctx context.Context, sessionID, turnID string, queued bool) error
}

var errNotReady = errors.New("session: not synced yet")

// Send writes a user turn and hands it to the machine. It returns how far it
// got; a turn past Unknown is never resent automatically.
func (r *Replica) Send(ctx context.Context, t Turn) (string, error) {
	text := strings.TrimSpace(t.Text)
	if text == "" {
		return NotSent, errors.New("session: empty message")
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
	queued := t.Queue || queueLen > 0 || busy(history)
	before := r.doc.OplogVv()
	var err error
	if queued {
		err = r.pushQueued(turnID, text, t.UserID, timestamp, config)
	} else {
		err = r.appendUserTurn(turnID, t.UserID, timestamp, config)
	}
	r.mu.Unlock()
	if err != nil {
		return NotSent, err
	}
	r.emit(true)
	if err := r.upload(ctx, before); err != nil {
		return Unknown, err
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
	reply, err := r.Conn.MachineRPC(ctx, t.MachineID, "session/dispatch-turn", map[string]any{
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
	config := map[string]any{
		"cliType":          t.CLIType,
		"agentType":        t.AgentType,
		"prompt":           text,
		"inputBlocks":      []any{map[string]any{"type": "text", "text": text}},
		"mcpServerIds":     []any{},
		"taskToolsEnabled": false,
	}
	if ids := list(previous["mcpServerIds"]); ids != nil {
		config["mcpServerIds"] = ids
	}
	if v, ok := previous["taskToolsEnabled"].(bool); ok {
		config["taskToolsEnabled"] = v
	}
	if v := str(previous["modeId"]); v != "" {
		config["modeId"] = v
	}
	model := t.ModelID
	if model == "" {
		model = str(previous["modelId"])
	}
	if model != "" {
		config["modelId"] = model
	}
	if opts := obj(previous["configOptionValues"]); len(opts) > 0 {
		config["configOptionValues"] = opts
	}
	if t.Resume != "" {
		config["resume"] = t.Resume
	}
	if role := previous["agentRoleId"]; role != nil {
		changed := str(previous["cliType"]) != t.CLIType || str(previous["agentType"]) != t.AgentType || str(previous["modelId"]) != model
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
func (r *Replica) appendUserTurn(id, userID, timestamp string, config map[string]any) error {
	history := r.doc.GetList(loro.AsContainerId("history"))
	entry, err := history.InsertLoroMap(history.Len())
	if err != nil {
		return err
	}
	for k, v := range map[string]any{
		"id": id, "role": "user", "userId": userID, "timestamp": timestamp,
		"status": "pending", "read": false, "finished": true, "fileDiff": []any{},
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
	reply, err := r.Conn.MachineRPC(ctx, machineID, "session/cancel", map[string]any{"sessionId": r.ID, "turnId": turnID}, 35*time.Second)
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
