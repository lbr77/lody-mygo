package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
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

// editBlocks are the message's input with its text replaced: its
// attachments stay.
func editBlocks(u Entry, text string) []any {
	var original []any
	if u.Config != nil {
		original = list(u.Config["inputBlocks"])
	}
	if original == nil {
		for _, it := range u.Items {
			switch it.Type {
			case "text":
				original = append(original, map[string]any{"type": "text", "text": it.Text})
			}
		}
	}
	var out []any
	replaced := false
	for _, b := range original {
		block := obj(b)
		if str(block["type"]) == "text" {
			if !replaced {
				out = append(out, map[string]any{"type": "text", "text": text})
				replaced = true
			}
			continue
		}
		out = append(out, b)
	}
	if !replaced {
		out = append(out, map[string]any{"type": "text", "text": text})
	}
	return out
}

// EditAndResend replaces the last user message with text and runs the
// turn again from the reply before it.
func (r *Replica) EditAndResend(ctx context.Context, machineID, userID, cliType, agentType string, u Entry, text string) error {
	return editAndResend(ctx, r.machineRPC, r.ID, machineID, userID, cliType, agentType, u, text)
}

func editAndResend(ctx context.Context, call rpc, sessionID, machineID, userID, cliType, agentType string, u Entry, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("session: empty message")
	}
	config := map[string]any{}
	for k, v := range u.Config {
		config[k] = v
	}
	blocks := editBlocks(u, text)
	config["inputBlocks"], config["prompt"] = blocks, text
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
