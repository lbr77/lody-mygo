package session

import (
	"context"
	"errors"
	"time"

	loro "github.com/aholstenson/loro-go"
)

// The message queue: the turns waiting behind the running one, the
// session's "mq" movable list. Lody's writer removes, edits and reorders
// its items in place (removeSessionMessage, updateSessionMessage,
// reorderSessionMessages); each change here is such a write, uploaded at
// once. The caller then bumps the session's messageQueueUpdatedAt, as
// Lody's bumpMessageQueueWatermark does, which wakes the machine's
// dispatcher. Items are named by their user turn.

// errNotQueued is a change to a message that left the queue.
var errNotQueued = errors.New("session: the message is no longer queued")

// queueIndex is where the item of user turn id is in the queue, or -1.
func queueIndex(mq *loro.LoroMovableList, id string) int {
	for i, q := range list(deepValue(mq.GetDeepValue())) {
		if str(obj(q)["userTurnId"]) == id {
			return i
		}
	}
	return -1
}

// editQueue applies fn to the queued item of user turn id, at index in
// the queue, then commits and uploads the change.
func (r *Replica) editQueue(ctx context.Context, id string, fn func(mq *loro.LoroMovableList, index int, item *loro.LoroMap) error) error {
	r.mu.Lock()
	if !r.ready {
		r.mu.Unlock()
		return errNotReady
	}
	mq := r.doc.GetMovableList(loro.AsContainerId("mq"))
	index := queueIndex(mq, id)
	if index < 0 {
		r.mu.Unlock()
		return errNotQueued
	}
	item, ok := mq.GetLoroMap(uint32(index))
	if !ok || item == nil {
		r.mu.Unlock()
		return errors.New("session: the queued message is malformed")
	}
	before := r.doc.OplogVv()
	err := fn(mq, index, item)
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

// RemoveQueued takes a message out of the queue, unsent.
func (r *Replica) RemoveQueued(ctx context.Context, id string) error {
	return r.editQueue(ctx, id, func(mq *loro.LoroMovableList, index int, _ *loro.LoroMap) error {
		return mq.Delete(uint32(index), 1)
	})
}

// MoveQueued moves a queued message to where the message over is, the
// others between them making room, as Lody's reorder (arrayMove) does
// when a row is dropped on another.
func (r *Replica) MoveQueued(ctx context.Context, id, over string) error {
	if id == over {
		return nil
	}
	return r.editQueue(ctx, id, func(mq *loro.LoroMovableList, from int, _ *loro.LoroMap) error {
		to := queueIndex(mq, over)
		if to < 0 {
			return errNotQueued
		}
		return mq.Mov(uint32(from), uint32(to))
	})
}

// EditingQueued marks a queued message as being edited, or no longer.
// While the first is, the machine holds the queue rather than send it
// half-edited, for as long as the lease from editingStartedAt lasts.
func (r *Replica) EditingQueued(ctx context.Context, id string, editing bool) error {
	return r.editQueue(ctx, id, func(_ *loro.LoroMovableList, _ int, item *loro.LoroMap) error {
		if !editing {
			if err := item.InsertAny("isEditing", false); err != nil {
				return err
			}
			if _, ok := item.GetAny("editingStartedAt"); ok {
				return item.Delete("editingStartedAt")
			}
			return nil
		}
		if err := item.InsertAny("isEditing", true); err != nil {
			return err
		}
		return item.InsertAny("editingStartedAt", float64(time.Now().UnixMilli()))
	})
}

// EditQueued replaces a queued message's text and ends its editing, as
// Lody's buildEditedMessageQueueItem: the text leads its images and
// files, and a message left with none says imageOnly in the queue.
func (r *Replica) EditQueued(ctx context.Context, id, text, imageOnly string) error {
	return r.editQueue(ctx, id, func(_ *loro.LoroMovableList, _ int, item *loro.LoroMap) error {
		was := obj(deepValue(item.GetDeepValue()))
		task := text
		if task == "" {
			task = imageOnly
		}
		for k, v := range map[string]any{"task": task, "isEditing": false} {
			if err := item.InsertAny(k, v); err != nil {
				return err
			}
		}
		if _, ok := item.GetAny("editingStartedAt"); ok {
			if err := item.Delete("editingStartedAt"); err != nil {
				return err
			}
		}
		cfg, ok := item.GetLoroMap("acpSessionConfig")
		if !ok || cfg == nil {
			return nil
		}
		// Its blocks as they were, or its prompt (else its task) as one.
		current := obj(was["acpSessionConfig"])
		prompt, has := current["prompt"].(string)
		if !has {
			prompt = str(was["task"])
		}
		blocks := list(current["inputBlocks"])
		if len(blocks) == 0 && prompt != "" {
			blocks = []any{map[string]any{"type": "text", "text": prompt}}
		}
		var next []any
		if text != "" {
			next = append(next, map[string]any{"type": "text", "text": text})
		}
		for _, b := range blocks {
			if str(obj(b)["type"]) != "text" {
				next = append(next, b)
			}
		}
		if err := cfg.InsertAny("prompt", text); err != nil {
			return err
		}
		if len(next) == 0 {
			if _, ok := cfg.GetAny("inputBlocks"); ok {
				return cfg.Delete("inputBlocks")
			}
			return nil
		}
		return cfg.InsertAny("inputBlocks", next)
	})
}
