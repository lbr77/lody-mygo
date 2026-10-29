package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	loro "github.com/aholstenson/loro-go"
)

// Steering: a message given to the running turn rather than queued after
// it. The message is first made durable as a user entry "pending_apply"
// linked to the turn it steers; the machine's "session/steer" receipt then
// marks it "processing". A lost receipt never replays the write.

// unfinishedReply is the id of the reply still being written, if any.
func unfinishedReply(history []any) string {
	for i := len(history) - 1; i >= 0; i-- {
		if e := obj(history[i]); str(e["role"]) == "assistant" && e["finished"] != true {
			return str(e["id"])
		}
	}
	return ""
}

// setSteer records a steer's delivery: confirming, accepted or unknown.
// Callers hold r.mu.
func (r *Replica) setSteer(turnID, state string) {
	if r.steers == nil {
		r.steers = map[string]string{}
	}
	r.steers[turnID] = state
}

// steer asks the machine to apply a steer and records its receipt.
func (r *Replica) steer(machineID string, params map[string]any) string {
	turnID, _ := params["userTurnId"].(string)
	ctx, cancel := context.WithTimeout(r.ctx, 35*time.Second)
	defer cancel()
	reply, err := r.Conn.MachineRPC(ctx, machineID, "session/steer", params, 30*time.Second)
	var res struct {
		Applied     bool   `json:"applied"`
		Disposition string `json:"disposition"`
	}
	if err == nil {
		json.Unmarshal(reply, &res)
	}
	r.mu.Lock()
	if err != nil || !res.Applied {
		r.setSteer(turnID, "unknown")
		r.mu.Unlock()
		r.emit(true)
		return "not_applied"
	}
	r.setSteer(turnID, "accepted")
	before := r.doc.OplogVv()
	history := r.doc.GetList(loro.AsContainerId("history"))
	changed := false
	for i := 0; i < int(history.Len()); i++ {
		entry, ok := history.GetLoroMap(uint32(i))
		if !ok || entry == nil {
			continue
		}
		id, _ := entry.GetString("id")
		status, _ := entry.GetString("status")
		if id == turnID && status == "pending_apply" {
			entry.InsertAny("status", "processing")
			entry.InsertAny("read", true)
			changed = true
		}
	}
	if changed {
		r.doc.Commit()
	}
	r.mu.Unlock()
	r.emit(true)
	if changed {
		// Delivery is confirmed even if this display-status write fails.
		r.upload(ctx, before)
	}
	return "applied"
}

// SteerQueued gives a queued message to the running turn. Natively, the
// message moves out of the queue into history and the agent takes it
// mid-turn; with interrupt, the running turn is cancelled and the queue,
// with this message first, resumes.
func (r *Replica) SteerQueued(ctx context.Context, machineID, messageID string, interrupt bool) error {
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return errNotReady
	}
	if r.sending {
		r.mu.Unlock()
		return errors.New("session: a message is still sending")
	}
	history := list(deepValue(r.doc.GetList(loro.AsContainerId("history")).GetDeepValue()))
	target := unfinishedReply(history)
	if target == "" {
		r.mu.Unlock()
		return errors.New("session: nothing is running")
	}
	mq := r.doc.GetMovableList(loro.AsContainerId("mq"))
	queue := list(deepValue(mq.GetDeepValue()))
	index := -1
	for i, q := range queue {
		if str(obj(q)["userTurnId"]) == messageID {
			index = i
		}
	}
	if index < 0 {
		r.mu.Unlock()
		return errors.New("session: the message is no longer queued")
	}
	if interrupt && index != 0 {
		r.mu.Unlock()
		return errors.New("session: only the first queued message can interrupt")
	}
	item := obj(queue[index])
	config := obj(item["acpSessionConfig"])
	userID, timestamp := str(item["userId"]), str(item["timestamp"])
	if config == nil || userID == "" || timestamp == "" {
		r.mu.Unlock()
		return errors.New("session: the queued message is incomplete")
	}
	mode := "native"
	if interrupt {
		mode = "interrupt"
	}
	r.sending = true
	defer func() {
		r.mu.Lock()
		r.sending = false
		r.mu.Unlock()
	}()
	r.setSteer(messageID, "confirming")
	before := r.doc.OplogVv()
	err := r.doc.GetMap(loro.AsContainerId("lodySteerLinks")).InsertAny(messageID, map[string]any{"targetId": target, "mode": mode})
	if err == nil && interrupt {
		// Mark the queued item; the machine's queue resumes with it once
		// the running turn is cancelled.
		queued, ok := mq.GetLoroMap(uint32(index))
		cfg, ok2 := (*loro.LoroMap)(nil), false
		if ok && queued != nil {
			cfg, ok2 = queued.GetLoroMap("acpSessionConfig")
		}
		if !ok2 || cfg == nil {
			err = errors.New("session: the queued message is malformed")
		} else {
			cfg.InsertAny("_lodyDeliveryKind", "steer")
			cfg.InsertAny("_lodySteerTarget", target)
			cfg.InsertAny("_lodySteerMode", "interrupt")
		}
	} else if err == nil {
		steered := map[string]any{"_lodyDeliveryKind": "steer"}
		for k, v := range config {
			if k != "chainDepth" {
				steered[k] = v
			}
		}
		if err = r.appendUserTurn(messageID, userID, timestamp, steered, "pending_apply"); err == nil {
			err = mq.Delete(uint32(index), 1)
		}
	}
	if err == nil {
		r.doc.Commit()
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	r.emit(true)
	if err := r.upload(ctx, before); err != nil {
		r.mu.Lock()
		r.setSteer(messageID, "unknown")
		r.mu.Unlock()
		return errors.New("session: the steer may not have been saved")
	}
	if interrupt {
		reply, err := r.Conn.MachineRPC(ctx, machineID, "session/cancel", map[string]any{"sessionId": r.ID, "turnId": target}, 35*time.Second)
		var res struct {
			Success bool `json:"success"`
		}
		if err == nil {
			json.Unmarshal(reply, &res)
		}
		if err != nil || !res.Success {
			return errors.New("session: the machine didn't confirm the interrupt")
		}
		return nil
	}
	params := map[string]any{
		"sessionId":      r.ID,
		"expectedTurnId": target,
		"userTurnId":     messageID,
		"userId":         userID,
		"timestamp":      timestamp,
		"inputConfig":    config,
	}
	if r.steer(machineID, params) != "applied" {
		return errors.New("session: the agent didn't take the steer")
	}
	return nil
}

// markSteers sets each user entry's steer state from the receipts and the
// session's steer links. Callers hold r.mu.
func (r *Replica) markSteers(v *View, links map[string]any) {
	for i := range v.Entries {
		e := &v.Entries[i]
		if e.Role != "user" {
			continue
		}
		if link := obj(links[e.ID]); link != nil {
			e.Steer = true
			e.SteerMode = str(link["mode"])
		}
		if state := r.steers[e.ID]; state != "" {
			e.Steer = true
			e.SteerState = state
		}
	}
}
