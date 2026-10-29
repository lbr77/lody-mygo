package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// maxReplica bounds what one catalog replica may download.
const maxReplica = 8 << 20

// State of the catalog connection.
type State struct {
	Live   bool   // every stream caught up and tailing
	Reason string // why the last sync failed, when it did
}

// Sync replicates a workspace's catalog and publishes merged snapshots.
// OnChange and OnState run on the sync's goroutines.
type Sync struct {
	Conn     *cloud.Conn
	OnChange func(Catalog)
	OnState  func(State)

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	meta      *replica
	machines  map[string]*replica
	partial   map[string]Catalog // by stream: "meta" or a machine id
	watchers  map[string]context.CancelFunc
	unhealthy map[string]string
	last      Catalog
}

type replica struct {
	mu     sync.Mutex
	flock  *flock.Flock
	client *streams.Client
}

// Start begins syncing. Stop ends it.
func (s *Sync) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.partial = map[string]Catalog{}
	s.machines = map[string]*replica{}
	s.watchers = map[string]context.CancelFunc{}
	s.unhealthy = map[string]string{}
	s.watch("meta")
	s.watch(workspaceMode)
}

func (s *Sync) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Sync) watch(mode string) {
	ctx, cancel := context.WithCancel(s.ctx)
	s.watchers[mode] = cancel
	go s.run(ctx, mode)
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (s *Sync) run(ctx context.Context, mode string) {
	failures := 0
	for ctx.Err() == nil {
		err := s.follow(ctx, mode, &failures)
		if ctx.Err() != nil {
			return
		}
		if mode != "meta" && streams.IsNotFound(err) {
			// A machine that never wrote its stream has an empty catalog.
			s.mu.Lock()
			delete(s.unhealthy, mode)
			s.partial[mode] = Catalog{}
			s.mu.Unlock()
			s.publish()
			sleep(ctx, 30*time.Second)
			continue
		}
		reason := "network_or_auth"
		if errors.Is(err, errLimit) {
			reason = "catalog_limit"
		}
		s.mu.Lock()
		if mode == "meta" {
			s.meta = nil
		} else {
			delete(s.machines, mode)
		}
		s.unhealthy[mode] = reason
		s.mu.Unlock()
		s.Conn.Invalidate()
		s.emitState()
		if reason == "catalog_limit" {
			return
		}
		backoff := min(30*time.Second, 2*time.Second<<min(failures, 4))
		failures++
		if sleep(ctx, backoff) != nil {
			return
		}
	}
}

var errLimit = errors.New("catalog: replica exceeds its limit")

// follow bootstraps a replica and tails it until an error.
func (s *Sync) follow(ctx context.Context, mode string, failures *int) error {
	name := "meta"
	switch mode {
	case "meta":
	case workspaceMode:
		name = "wf:workspace"
	default:
		name = "mf:" + mode
	}
	client, err := s.Conn.Stream(ctx, name)
	if err != nil {
		return err
	}
	boot, err := client.Bootstrap(ctx)
	if err != nil {
		return err
	}
	f, err := flock.New("lody-mygo-" + uuid.NewString())
	if err != nil {
		return err
	}
	size := 0
	if boot.Snapshot != nil {
		size += len(boot.Snapshot)
		if size > maxReplica {
			return errLimit
		}
		snapshot, err := streams.Unpack(boot.Snapshot)
		if err != nil {
			return err
		}
		if err := f.ImportFile(snapshot); err != nil {
			return err
		}
	}
	for _, u := range boot.Updates {
		size += len(u)
		if size > maxReplica {
			return errLimit
		}
		if err := applyUpdate(f, u); err != nil {
			return err
		}
	}
	rep := &replica{flock: f, client: client}
	offset, cursor, upToDate := boot.NextOffset, boot.Cursor, boot.UpToDate
	pages := 0
	for {
		if upToDate {
			if err := s.settle(mode, rep); err != nil {
				return err
			}
			*failures = 0
			pages = 0
		}
		r, err := client.Read(ctx, offset, cursor, upToDate)
		if err != nil {
			return err
		}
		if len(r.Payload) > 0 {
			size += len(r.Payload)
			if size > maxReplica {
				return errLimit
			}
			rep.mu.Lock()
			err := applyUpdate(f, r.Payload)
			rep.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if r.NextOffset == offset && !r.UpToDate {
			return errors.New("catalog: stalled cursor")
		}
		if pages++; pages > 100 && !r.UpToDate {
			return errLimit
		}
		offset, cursor, upToDate = r.NextOffset, r.Cursor, r.UpToDate
		if r.Closed {
			return errors.New("catalog: stream closed")
		}
		// Empty responses may come back at once; don't spin.
		if len(r.Payload) == 0 && upToDate {
			if err := sleep(ctx, time.Second); err != nil {
				return err
			}
		}
	}
}

func applyUpdate(f *flock.Flock, body []byte) error {
	frames, err := streams.DecodeFrames(body)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		var bundle map[string]any
		if err := json.Unmarshal(frame, &bundle); err != nil {
			return fmt.Errorf("catalog: bad update: %w", err)
		}
		if err := f.ImportJSON(bundle); err != nil {
			return err
		}
	}
	return nil
}

// settle records a caught-up replica and publishes.
func (s *Sync) settle(mode string, rep *replica) error {
	rep.mu.Lock()
	rows, err := rep.flock.Scan()
	rep.mu.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if mode == "meta" {
		s.meta = rep
	} else {
		s.machines[mode] = rep
	}
	s.partial[mode] = project(mode, rows)
	delete(s.unhealthy, mode)
	s.mu.Unlock()
	s.publish()
	return nil
}

func (s *Sync) publish() {
	s.mu.Lock()
	meta, ok := s.partial["meta"]
	if !ok || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	listed := map[string]bool{"meta": true, workspaceMode: true}
	for _, id := range meta.MachineIDs {
		listed[id] = true
		if _, watching := s.watchers[id]; !watching {
			s.watch(id)
		}
	}
	for id, cancel := range s.watchers {
		if !listed[id] {
			cancel()
			delete(s.watchers, id)
			delete(s.partial, id)
			delete(s.machines, id)
			delete(s.unhealthy, id)
		}
	}
	merged := merge(meta, s.partial)
	changed := !reflect.DeepEqual(merged, s.last)
	s.last = merged
	s.mu.Unlock()
	if changed && s.OnChange != nil {
		s.OnChange(merged)
	}
	s.emitState()
}

func (s *Sync) emitState() {
	if s.OnState == nil {
		return
	}
	s.mu.Lock()
	st := State{Live: s.meta != nil && len(s.unhealthy) == 0}
	for _, reason := range s.unhealthy {
		st.Reason = reason
	}
	s.mu.Unlock()
	s.OnState(st)
}

// ErrNotReady is returned by writes before the meta replica has synced.
var ErrNotReady = errors.New("catalog: workspace is still syncing")

// Write applies edits to the meta replica and appends them to the stream,
// as one Flock JSON delta.
func (s *Sync) Write(ctx context.Context, edit func(f *flock.Flock) error) error {
	s.mu.Lock()
	rep := s.meta
	s.mu.Unlock()
	if rep == nil {
		return ErrNotReady
	}
	return s.writeTo(ctx, rep, "meta", edit)
}

// writeTo applies edits to a replica and appends them to its stream.
func (s *Sync) writeTo(ctx context.Context, rep *replica, mode string, edit func(f *flock.Flock) error) error {
	rep.mu.Lock()
	before, err := rep.flock.Version()
	if err == nil {
		err = edit(rep.flock)
	}
	var delta map[string]any
	if err == nil {
		delta, err = rep.flock.ExportJSON(before)
	}
	rep.mu.Unlock()
	if err != nil {
		return err
	}
	body, err := json.Marshal(delta)
	if err != nil {
		return err
	}
	if _, err := rep.client.Append(ctx, "application/octet-stream", streams.EncodeFrame(body)); err != nil {
		return err
	}
	rep.mu.Lock()
	rows, err := rep.flock.Scan()
	rep.mu.Unlock()
	if err == nil {
		s.mu.Lock()
		s.partial[mode] = project(mode, rows)
		s.mu.Unlock()
		s.publish()
	}
	return nil
}

// Pin pins or unpins a session.
func (s *Sync) Pin(ctx context.Context, sessionID string, pinned bool) error {
	return s.SetField(ctx, sessionID, "isPinned", pinned)
}

// Archive archives or restores a session with the sessions it opened, as
// the desktop app does: the session flag is the archive, and the machine's
// cleanup command is best effort.
func (s *Sync) Archive(ctx context.Context, sessionID string, archived bool) error {
	s.mu.Lock()
	sessions := s.partial["meta"].Sessions
	s.mu.Unlock()
	ids := lifecycle(sessions, sessionID)
	byID := map[string]Session{}
	for _, sess := range sessions {
		byID[sess.ID] = sess
	}
	roots := map[string][]string{} // machine to session ids
	err := s.Write(ctx, func(f *flock.Flock) error {
		for _, id := range ids {
			if err := f.Put([]any{"m", room(id), "isArchived"}, archived); err != nil {
				return err
			}
			if archived {
				if err := f.Put([]any{"m", room(id), "status"}, map[string]any{"type": "idle"}); err != nil {
					return err
				}
			}
			sess := byID[id]
			if sess.ParentSessionID != "" || sess.MachineID == "" {
				continue
			}
			roots[sess.MachineID] = append(roots[sess.MachineID], id)
			if err := patchNeedToArchive(f, sess.MachineID, id, archived); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for machine, list := range roots {
		s.mu.Lock()
		rep := s.machines[machine]
		s.mu.Unlock()
		if rep == nil {
			continue
		}
		s.writeTo(ctx, rep, machine, func(f *flock.Flock) error {
			for _, id := range list {
				key := []any{"cmd", "archiveSession", id}
				if !archived {
					if err := f.Delete(key); err != nil {
						return err
					}
					continue
				}
				if err := f.Put(key, map[string]any{"v": 1.0, "requestedAt": float64(time.Now().UnixMilli())}); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return nil
}

// Delete removes a session, and the sessions it opened, for good. A
// session still running can't be deleted.
func (s *Sync) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	sessions := s.partial["meta"].Sessions
	s.mu.Unlock()
	ids := lifecycle(sessions, sessionID)
	for _, sess := range sessions {
		for _, id := range ids {
			if sess.ID == id && sess.Working() {
				return errors.New("catalog: the session is still running")
			}
		}
	}
	return s.Write(ctx, func(f *flock.Flock) error {
		for _, id := range ids {
			if err := f.Put([]any{"e", room(id)}, false); err != nil {
				return err
			}
		}
		return nil
	})
}

// lifecycle is the session and every session it opened, transitively.
func lifecycle(sessions []Session, id string) []string {
	children := map[string][]string{}
	for _, sess := range sessions {
		for _, owner := range []string{sess.ParentSessionID, sess.OpenedBySessionID, sess.OpenedByRootSessionID} {
			if owner != "" && owner != sess.ID {
				children[owner] = append(children[owner], sess.ID)
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for pending := []string{id}; len(pending) > 0; pending = pending[1:] {
		current := pending[0]
		if seen[current] {
			continue
		}
		seen[current] = true
		out = append(out, current)
		pending = append(pending, children[current]...)
	}
	return out
}

// patchNeedToArchive updates a machine's list of sessions to archive.
func patchNeedToArchive(f *flock.Flock, machineID, sessionID string, archived bool) error {
	machineRoom := "machine-" + machineID
	need := map[string]any{}
	if v, ok, err := f.Get([]any{"m", machineRoom, "needToArchiveSessions"}); err != nil {
		return err
	} else if ok {
		for k, x := range object(v) {
			need[k] = x
		}
	} else if whole, ok, err := f.Get([]any{"m", machineRoom}); err == nil && ok {
		for k, x := range object(object(whole)["needToArchiveSessions"]) {
			need[k] = x
		}
	}
	if archived {
		need[sessionID] = true
	} else {
		delete(need, sessionID)
	}
	return f.Put([]any{"m", machineRoom, "needToArchiveSessions"}, need)
}

func room(sessionID string) string { return "session-" + sessionID }

// MarkDispatch records a user turn on the session's metadata, which tells
// the machine to pick it up.
func (s *Sync) MarkDispatch(ctx context.Context, sessionID, turnID string, queued bool) error {
	return s.Write(ctx, func(f *flock.Flock) error {
		if queued {
			key := []any{"m", room(sessionID), "messageQueueUpdatedAt"}
			next := float64(time.Now().UnixMilli())
			if prev, ok, _ := f.Get(key); ok {
				if p, isNum := prev.(float64); isNum && p+1 > next {
					next = p + 1
				}
			}
			return f.Put(key, next)
		}
		if err := f.Put([]any{"m", room(sessionID), "latestUserMsgId"}, turnID); err != nil {
			return err
		}
		return f.Put([]any{"m", room(sessionID), "lastMissingHistoryUserMsgId"}, nil)
	})
}

// SetField sets one metadata field of a session: title, isPinned,
// lastReadAt and so on.
func (s *Sync) SetField(ctx context.Context, sessionID, field string, value any) error {
	return s.Write(ctx, func(f *flock.Flock) error {
		return f.Put([]any{"m", room(sessionID), field}, value)
	})
}

// Rename sets a session's title as a user edit.
func (s *Sync) Rename(ctx context.Context, sessionID, title string) error {
	return s.Write(ctx, func(f *flock.Flock) error {
		if err := f.Put([]any{"m", room(sessionID), "title"}, title); err != nil {
			return err
		}
		return f.Put([]any{"m", room(sessionID), "titleSource"}, "user")
	})
}

// MarkRead moves a session's read mark forward.
func (s *Sync) MarkRead(ctx context.Context, sess Session) error {
	at := max(float64(time.Now().UnixMilli()), sess.LastMessageAt, sess.LastReadAt+1)
	return s.SetField(ctx, sess.ID, "lastReadAt", at)
}

// NewSession is what CreateSession writes into the catalog.
type NewSession struct {
	ID           string
	MachineID    string
	UserID       string
	Title        string
	Agent        AgentConfig
	LocalProject string // local project id, for a session in a project
	CreatedAt    time.Time
}

// CreateSession creates the session's stream and its catalog entry.
func (s *Sync) CreateSession(ctx context.Context, n NewSession) error {
	stream, err := s.Conn.Stream(ctx, "s:"+n.ID)
	if err != nil {
		return err
	}
	if err := stream.Create(ctx, "application/octet-stream", 0); err != nil {
		return err
	}
	meta := map[string]any{
		"id":            n.ID,
		"machineId":     n.MachineID,
		"userId":        n.UserID,
		"title":         n.Title,
		"titleSource":   "draft",
		"status":        map[string]any{"type": "idle"},
		"isArchived":    false,
		"createdAt":     n.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"cliType":       n.Agent.CLIType,
		"agentType":     n.Agent.AgentType,
		"agentConfigId": n.Agent.ID,
	}
	if n.LocalProject != "" {
		meta["project"] = map[string]any{"kind": "local", "localProjectId": n.LocalProject}
	}
	return s.Write(ctx, func(f *flock.Flock) error {
		if err := f.Put([]any{"e", room(n.ID)}, true); err != nil {
			return err
		}
		return f.Put([]any{"m", room(n.ID)}, meta)
	})
}

// RenameMachine names a machine.
func (s *Sync) RenameMachine(ctx context.Context, machineID, name string) error {
	return s.Write(ctx, func(f *flock.Flock) error {
		return f.Put([]any{"m", "machine-" + machineID, "name"}, name)
	})
}

// RemoveMachine takes a machine out of the workspace; it registers again
// if its CLI connects.
func (s *Sync) RemoveMachine(ctx context.Context, machineID string) error {
	return s.Write(ctx, func(f *flock.Flock) error {
		return f.Put([]any{"e", "machine-" + machineID}, false)
	})
}
