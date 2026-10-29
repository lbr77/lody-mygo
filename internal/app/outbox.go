package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/lbr77/lody-mygo/internal/session"
)

// The outbox keeps held messages on disk, where lody-ios's outbox and
// Lody's durable inputs agree: one file per account and workspace under
// the app's data, rewritten with every change; each message as sent,
// under the id its turn is written with, with what of it was uploaded,
// why it was not sent, and whether its write had begun. Taken up again
// when the workspace opens, after a restart or going offline, held
// messages go on in the order sent: an attachment uploaded is not
// uploaded again, one whose file is gone fails on its card, and a message
// whose write had begun is never written twice: let go once its
// conversation shows the turn, else written again under the same id,
// which the replica refuses to repeat. Leaving asks only while a held
// message is not kept yet (unload.go).

// outboxRecord is a held message as the outbox keeps it.
type outboxRecord struct {
	ID          string             `json:"id"`
	SessionID   string             `json:"sessionId"`
	Text        string             `json:"text"` // as typed: mentions expand when it is written
	At          time.Time          `json:"at"`
	Turn        outboxTurn         `json:"turn"`
	Attachments []outboxAttachment `json:"attachments,omitempty"`
	Queue       bool               `json:"queue,omitempty"`
	Fresh       bool               `json:"fresh,omitempty"`
	Error       string             `json:"error,omitempty"`
	Writing     bool               `json:"writing,omitempty"`
}

// outboxTurn is how a held message runs, as it was chosen when sent.
type outboxTurn struct {
	UserID    string `json:"userId"`
	MachineID string `json:"machineId"`
	AgentType string `json:"agentType"`
	CLIType   string `json:"cliType"`
	Resume    string `json:"resume,omitempty"`
	ModelID   string `json:"modelId,omitempty"`
	ModeID    string `json:"modeId,omitempty"`
	Queue     bool   `json:"queue,omitempty"`
	Guide     bool   `json:"guide,omitempty"`
	// MCPServerIDs keeps null apart from none: null keeps the previous
	// turn's servers.
	MCPServerIDs       []string       `json:"mcpServerIds"`
	ConfigOptionValues map[string]any `json:"configOptionValues,omitempty"`
}

// outboxAttachment is a held message's attachment: its file, and once
// uploaded the block standing for it.
type outboxAttachment struct {
	Path  string         `json:"path"`
	Name  string         `json:"name"`
	Image bool           `json:"image,omitempty"`
	Size  int64          `json:"size"`
	Ready map[string]any `json:"ready,omitempty"`
	Error string         `json:"error,omitempty"`
}

func (s *pendingSend) record() outboxRecord {
	t := s.turn
	rec := outboxRecord{ID: s.id, SessionID: s.sessionID, Text: s.text, At: s.at, Queue: s.queue, Fresh: s.fresh, Error: s.err, Writing: s.writing,
		Turn: outboxTurn{UserID: t.UserID, MachineID: t.MachineID, AgentType: t.AgentType, CLIType: t.CLIType, Resume: t.Resume,
			ModelID: t.ModelID, ModeID: t.ModeID, Queue: t.Queue, Guide: t.Guide, MCPServerIDs: t.MCPServerIDs, ConfigOptionValues: t.ConfigOptionValues}}
	for _, at := range s.attachments {
		rec.Attachments = append(rec.Attachments, outboxAttachment{Path: at.file.Path, Name: at.file.Name, Image: at.file.Image, Size: at.file.Size,
			Ready: at.ready, Error: at.err})
	}
	return rec
}

// send is the held message a kept record stands for, kept already. What
// it expands its mentions with and dispatches through are the
// workspace's as it is written.
func (rec outboxRecord) send() *pendingSend {
	t := rec.Turn
	s := &pendingSend{id: rec.ID, sessionID: rec.SessionID, text: rec.Text, at: rec.At, queue: rec.Queue, fresh: rec.Fresh, err: rec.Error,
		writing: rec.Writing, persisted: true,
		turn: session.Turn{Text: rec.Text, UserID: t.UserID, MachineID: t.MachineID, AgentType: t.AgentType, CLIType: t.CLIType, Resume: t.Resume,
			ModelID: t.ModelID, ModeID: t.ModeID, Queue: t.Queue, Guide: t.Guide, MCPServerIDs: t.MCPServerIDs, ConfigOptionValues: t.ConfigOptionValues}}
	for _, at := range rec.Attachments {
		s.attachments = append(s.attachments, sendAttachment{file: pendingFile{Path: at.Path, Name: at.Name, Image: at.Image, Size: at.Size},
			ready: at.Ready, err: at.Error})
	}
	return s
}

// outboxFile is the file the open workspace's held messages are kept in,
// by account and workspace; "" while there is neither.
func (a *App) outboxFile() string {
	if a.account == nil || a.account.User.ID == "" || a.conn == nil || a.conn.ID == "" {
		return ""
	}
	return "outbox-" + filepath.Base(a.account.User.ID) + "-" + filepath.Base(a.conn.ID) + ".json"
}

// restoreSends takes up the held messages kept for the open workspace,
// once for each time it opens, behind any held already. A picture's
// thumbnail is read again from its file.
func (a *App) restoreSends() {
	q := &a.pending
	file := a.outboxFile()
	if file == "" || q.scope == file {
		return
	}
	q.scope = file
	var kept []outboxRecord
	readJSON(file, &kept)
	for _, rec := range kept {
		if rec.ID == "" || rec.SessionID == "" || q.find(rec.ID) != nil {
			continue
		}
		s := rec.send()
		for _, at := range s.attachments {
			if at.file.Image {
				a.loadLocalThumb(at.file.Path)
			}
		}
		q.list = append(q.list, s)
	}
}

// saveSends keeps the open workspace's held messages on disk as they
// stand; once that worked, each is persisted.
func (a *App) saveSends() {
	a.restoreSends()
	q := &a.pending
	if q.scope == "" || q.scope != a.outboxFile() {
		return
	}
	records := make([]outboxRecord, 0, len(q.list))
	for _, s := range q.list {
		records = append(records, s.record())
	}
	if writeOutbox(q.scope, records) != nil {
		return
	}
	for _, s := range q.list {
		s.persisted = true
	}
}

// writeOutbox replaces the outbox file, whole or not at all; with nothing
// to keep, there is none.
func writeOutbox(name string, records []outboxRecord) error {
	dir := dataDir()
	path := filepath.Join(dir, name)
	if len(records) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// tickSends is the outbox's dispatcher, run as the window draws, as
// lody-ios's runs as its inputs change: it takes up what was kept, lets
// go of a message whose write had begun once its conversation shows the
// turn, and, connected, sends each conversation's next.
func (a *App) tickSends() {
	a.restoreSends()
	q := &a.pending
	if len(q.list) == 0 {
		return
	}
	landed := false
	for _, s := range append([]*pendingSend(nil), q.list...) {
		if s.writing && !q.running[s.sessionID] && a.turnShown(s) {
			q.remove(s.id)
			landed = true
		}
	}
	if landed {
		a.saveSends()
	}
	if !a.sendsConnected() {
		return
	}
	seen := map[string]bool{}
	for _, s := range append([]*pendingSend(nil), q.list...) {
		if !seen[s.sessionID] {
			seen[s.sessionID] = true
			a.drainSends(s.sessionID)
		}
	}
}

// turnShown reports whether a held message's turn is in its conversation
// already: the catalog's latest message from the user, or, open, a turn
// of it or its queue.
func (a *App) turnShown(s *pendingSend) bool {
	if sess := a.catalog.Session(s.sessionID); sess != nil && sess.LatestUserMsgID == s.id {
		return true
	}
	return a.selected == s.sessionID && landed(a.view, s.id)
}

// sendExpand is how a held message taken up again expands its mentions,
// with what the workspace knows as it is written.
func (a *App) sendExpand(sessionID, text string) func(ctx context.Context) (string, []map[string]any) {
	var t mentionTarget
	if s := a.catalog.Session(sessionID); s != nil {
		t = mentionTarget{s.MachineID, s.ProjectID, s.ID, s.AgentConfigID}
	}
	conn, user, store := a.conn, a.userID(), a.shortcutStore()
	skills, sessions := a.mentionItems(t)
	return func(ctx context.Context) (string, []map[string]any) {
		return expandMentions(ctx, conn, user, t, text, skills, sessions, store)
	}
}
