package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
)

// What a client asks a session's machine besides a turn, as Lody's chat
// does: edit the last message and send it again, fork from a reply, end
// the agent's process, steer a goal, and ask whether it really runs.

// opError is a machine's refusal, its code and words.
type opError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OpError is what a session operation failed with.
type OpError struct{ Code, Message string }

func (e *OpError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// EditableUser is the user message an edit may replace: the last one,
// delivered as an ordinary turn, in a session whose agent restarts from
// the reply before it (a first message has none).
func EditableUser(entries []Entry, forkable bool) *Entry {
	last := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "user" && !entries[i].Queued {
			last = i
			break
		}
	}
	if last < 0 {
		return nil
	}
	u := &entries[last]
	if u.Status == "pending_apply" || u.Status == "delivery_unknown" || u.Steer {
		return nil
	}
	for i := last - 1; i >= 0; i-- {
		e := entries[i]
		if e.Role == "user" {
			return nil
		}
		if e.Role != "assistant" {
			continue
		}
		if !e.Finished || e.AcpTurnID == "" || !forkable {
			return nil
		}
		return u
	}
	return u
}

// inputBlocksOf is the message's input as it was sent; one written
// without it is rebuilt from what the conversation shows: its text, then
// its pictures and files.
func inputBlocksOf(u Entry) []any {
	if u.Config != nil {
		if blocks := list(u.Config["inputBlocks"]); blocks != nil {
			return blocks
		}
	}
	var out []any
	for _, it := range u.Items {
		switch it.Type {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": it.Text})
		case "image", "file":
			out = append(out, itemBlock(it))
		case "image_group":
			for _, img := range it.Images {
				out = append(out, itemBlock(img))
			}
		}
	}
	return out
}

// itemBlock is the input block a picture or file shown in the
// conversation stands for, with the fields Lody's inputBlock keeps.
func itemBlock(it Item) map[string]any {
	b := map[string]any{"type": it.Type}
	set := func(k string, v any, ok bool) {
		if ok {
			b[k] = v
		}
	}
	if it.Type == "image" {
		set("imageId", it.ImageID, it.ImageID != "")
	} else {
		set("fileId", it.FileID, it.FileID != "")
		set("textPreview", it.TextPreview, true)
		set("transport", it.Transport, it.Transport != "")
		set("machineId", it.FileMachine, it.FileMachine != "")
		set("uploadedAt", it.UploadedAt, it.UploadedAt != 0)
	}
	set("fileName", it.FileName, it.FileName != "")
	set("mimeType", it.MimeType, it.MimeType != "")
	set("sizeBytes", float64(it.SizeBytes), it.SizeBytes > 0)
	set("storageSessionId", it.StorageSessionID, it.StorageSessionID != "")
	return b
}

func isAttachment(b map[string]any) bool {
	t := str(b["type"])
	return t == "image" || t == "file"
}

// EditAttachment is one of the last message's pictures and files as an
// edit sees it, as Lody's editAttachments: the id the edit keeps it by,
// the block its input carries, and the item the conversation shows.
type EditAttachment struct {
	ID    string
	Block map[string]any
	Item  Item
}

// EditAttachments are the message's pictures and files in the order sent,
// each "original:<n>".
func EditAttachments(u Entry) []EditAttachment {
	shown := map[string]Item{}
	for _, it := range u.Items {
		for _, x := range append([]Item{it}, it.Images...) {
			switch {
			case x.Type == "image" && x.ImageID != "":
				shown["image:"+x.ImageID] = x
			case x.Type == "file" && x.FileID != "":
				shown["file:"+x.FileID] = x
			}
		}
	}
	var out []EditAttachment
	for _, b := range inputBlocksOf(u) {
		m := obj(b)
		if !isAttachment(m) {
			continue
		}
		it, ok := shown[str(m["type"])+":"+str(m["imageId"])+str(m["fileId"])]
		if !ok {
			it = Item{Type: str(m["type"]), ImageID: str(m["imageId"]), FileID: str(m["fileId"]), FileName: str(m["fileName"]),
				MimeType: str(m["mimeType"]), StorageSessionID: str(m["storageSessionId"])}
		}
		if it.SizeBytes == 0 {
			it.SizeBytes = int64(num(m["sizeBytes"]))
		}
		out = append(out, EditAttachment{ID: fmt.Sprintf("original:%d", len(out)), Block: m, Item: it})
	}
	return out
}

// Edit is what replaces the last user message, as Lody's editSession
// sends it: its text, the ids of the attachments it keeps
// (EditAttachments), and the blocks of the files added, uploaded.
type Edit struct {
	Text     string
	Retained []string
	Added    []map[string]any
}

// errEditAttachments is an edit keeping an attachment the message never
// had, or one twice.
var errEditAttachments = errors.New("session: invalid attachment")

// editBlocks are the message's input as the edit leaves it: the text in
// place of its own, the attachments kept where they were and those added
// after them, and anything else it carried, as references, as it was. It
// holds Lody's limits: 16 attachments, 8 of each kind.
func editBlocks(u Entry, e Edit) ([]any, error) {
	text := strings.TrimSpace(e.Text)
	known := map[string]bool{}
	for _, at := range EditAttachments(u) {
		known[at.ID] = true
	}
	keep := map[string]bool{}
	for _, id := range e.Retained {
		if !known[id] || keep[id] {
			return nil, errEditAttachments
		}
		keep[id] = true
	}
	var out []any
	textBlock := map[string]any{"type": "text", "text": text}
	// after is where what is added goes: behind the last attachment, else
	// behind the text.
	replaced, after, n := false, -1, 0
	for _, b := range inputBlocksOf(u) {
		block := obj(b)
		switch {
		case str(block["type"]) == "text":
			if !replaced && text != "" {
				out = append(out, textBlock)
				after = len(out)
			}
			replaced = true
		case isAttachment(block):
			if keep[fmt.Sprintf("original:%d", n)] {
				out = append(out, b)
			}
			n++
			after = len(out)
		default:
			out = append(out, b)
		}
	}
	if !replaced && text != "" {
		out = append(out, textBlock)
		after = len(out)
	}
	if after < 0 {
		after = len(out)
	}
	blocks := append([]any(nil), out[:after]...)
	for _, b := range e.Added {
		blocks = append(blocks, b)
	}
	blocks = append(blocks, out[after:]...)
	images, files := 0, 0
	for _, b := range blocks {
		switch str(obj(b)["type"]) {
		case "image":
			images++
		case "file":
			files++
		}
	}
	if images+files > cloud.MaxAttachments || images > cloud.MaxImages || files > cloud.MaxFiles {
		return nil, errors.New("session: too many attachments")
	}
	if text == "" && images+files == 0 {
		return nil, errors.New("session: empty message")
	}
	return blocks, nil
}

// EditAndResend replaces the last user message with the edit and runs
// the turn again from the reply before it.
func (r *Replica) EditAndResend(ctx context.Context, machineID, userID, cliType, agentType string, u Entry, e Edit) error {
	return editAndResend(ctx, r.machineRPC, r.ID, machineID, userID, cliType, agentType, u, e)
}

func editAndResend(ctx context.Context, call rpc, sessionID, machineID, userID, cliType, agentType string, u Entry, e Edit) error {
	blocks, err := editBlocks(u, e)
	if err != nil {
		return err
	}
	config := map[string]any{}
	for k, v := range u.Config {
		config[k] = v
	}
	config["inputBlocks"], config["prompt"] = blocks, strings.TrimSpace(e.Text)
	config["cliType"], config["agentType"] = cliType, agentType
	raw, err := call(ctx, machineID, "session/edit-and-resend", map[string]any{
		"sessionId":             sessionID,
		"expectedUserTurnId":    u.ID,
		"replacementUserTurnId": uuid.NewString(),
		"requestedByUserId":     userID,
		"timestamp":             now(),
		"inputConfig":           config,
	}, 120*time.Second)
	if err != nil {
		return err
	}
	var res struct {
		Success bool     `json:"success"`
		Error   *opError `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if !res.Success {
		if res.Error != nil {
			return &OpError{res.Error.Code, res.Error.Message}
		}
		return &OpError{Code: "EDIT_FAILED"}
	}
	return nil
}

// ForkResult is how a fork went.
type ForkResult struct {
	Target   string // the new session
	Confirm  bool   // the source worktree has changes a worktree fork leaves behind
	Partial  bool   // some history couldn't come along
	Warnings []string
}

// Fork starts a new session from a reply: in the same directory, or a new
// worktree from the last commit. target is the session to make, new when
// empty; dirtyOK confirms leaving uncommitted changes behind.
func (r *Replica) Fork(ctx context.Context, machineID, userID, turnID string, worktree bool, target string, dirtyOK bool) (ForkResult, error) {
	return fork(ctx, r.machineRPC, r.ID, machineID, userID, turnID, worktree, target, dirtyOK)
}

func fork(ctx context.Context, call rpc, sessionID, machineID, userID, turnID string, worktree bool, target string, dirtyOK bool) (ForkResult, error) {
	if target == "" {
		target = uuid.NewString()
	}
	params := map[string]any{"sourceSessionId": sessionID, "sourceTurnId": turnID, "targetSessionId": target, "requestedByUserId": userID}
	timeout := 120 * time.Second
	if worktree {
		ctxt := map[string]any{"kind": "new-worktree"}
		if dirtyOK {
			ctxt["acknowledgeDirtySource"] = true
		}
		params["targetContext"] = ctxt
		timeout = 15 * time.Second
	}
	var res struct {
		Success     bool      `json:"success"`
		Disposition string    `json:"disposition"`
		Partial     bool      `json:"partial"`
		Warnings    []opError `json:"warnings"`
		Error       *opError  `json:"error"`
	}
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := call(ctx, machineID, "session/fork", params, timeout)
		if err != nil {
			return ForkResult{}, err
		}
		res.Error = nil
		if err := json.Unmarshal(raw, &res); err != nil {
			return ForkResult{}, err
		}
		// Lody tries a worktree fork again once after an internal error.
		if !(worktree && !res.Success && res.Error != nil && res.Error.Code == "INTERNAL_ERROR") {
			break
		}
	}
	out := ForkResult{Target: target, Partial: res.Partial}
	for _, w := range res.Warnings {
		out.Warnings = append(out.Warnings, w.Message)
	}
	if res.Disposition == "confirmation-required" {
		out.Confirm = true
		return out, nil
	}
	if !res.Success {
		if res.Error != nil {
			return out, &OpError{res.Error.Code, res.Error.Message}
		}
		return out, &OpError{Code: "FORK_FAILED"}
	}
	return out, nil
}

// Terminate ends the agent process a session runs on its machine.
func (r *Replica) Terminate(ctx context.Context, machineID string) error {
	return Terminate(ctx, r.machineRPC, machineID, r.ID)
}

// rpc is a machine RPC, as cloud.Conn makes one.
type rpc func(ctx context.Context, machineID, method string, params any, timeout time.Duration) (json.RawMessage, error)

// Terminate ends a session's agent process on its machine, as the
// machines page does for one it monitors.
func Terminate(ctx context.Context, call rpc, machineID, sessionID string) error {
	raw, err := call(ctx, machineID, "session/terminate", map[string]any{"sessionId": sessionID}, 30*time.Second)
	if err != nil {
		return err
	}
	var res struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if !res.Success {
		return &OpError{Code: "TERMINATE_FAILED", Message: res.Error}
	}
	return nil
}

// Goal steers a session's goal: set (with an objective), pause, resume or
// clear. It returns how the machine took it.
func (r *Replica) Goal(ctx context.Context, machineID, userID, action, objective string) (string, error) {
	return goal(ctx, r.machineRPC, r.ID, machineID, userID, action, objective)
}

func goal(ctx context.Context, call rpc, sessionID, machineID, userID, action, objective string) (string, error) {
	params := map[string]any{"sessionId": sessionID, "action": action, "userId": userID}
	if o := strings.TrimSpace(objective); o != "" {
		params["objective"] = o
	}
	raw, err := call(ctx, machineID, "session/goal", params, 30*time.Second)
	if err != nil {
		return "", err
	}
	var res struct {
		Accepted    bool   `json:"accepted"`
		Disposition string `json:"disposition"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if !res.Accepted {
		return res.Disposition, &OpError{Code: res.Disposition, Message: firstOf(res.Error, "Goal command was "+firstOf(res.Disposition, "not delivered"))}
	}
	return res.Disposition, nil
}

// LiveStatus asks the machine what the session's agent is doing: idle,
// initializing, running, waiting or unknown.
func (r *Replica) LiveStatus(ctx context.Context, machineID string) (string, error) {
	return liveStatus(ctx, r.machineRPC, r.ID, machineID)
}

func liveStatus(ctx context.Context, call rpc, sessionID, machineID string) (string, error) {
	raw, err := call(ctx, machineID, "session/live-status", map[string]any{"sessionId": sessionID}, 10*time.Second)
	if err != nil {
		return "unknown", err
	}
	var res struct {
		Success bool   `json:"success"`
		State   string `json:"state"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "unknown", err
	}
	if !res.Success {
		return "unknown", &OpError{Code: "LIVE_STATUS_FAILED", Message: res.Error}
	}
	return res.State, nil
}
