package app

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's pending sends: a message whose attachments still have to upload,
// or one sent behind such a message in the same conversation, is held in
// memory rather than written, and the composer is free at once. Each
// conversation's held messages go out in the order sent, one at a time:
// a message's attachments upload one after another, then its user turn is
// written and handed to the machine. A message that fails holds the
// conversation's later ones behind it until it is sent again or canceled.
// Nothing keeps them: closing the workspace loses them. A held message
// shows where it is going, in the conversation as a pending message or,
// routed to the queue because the agent was busy, as a pending row of Up
// next, and gives way to the real message once that is written.

// sendAttachment is one of a held message's attachments, as Lody's
// SessionAttachmentDraft: its file; once uploaded, the block that stands
// for it; or why it could not be; and how far it has gone, 0 to 100.
type sendAttachment struct {
	file     pendingFile
	ready    map[string]any
	err      string
	progress float32
}

// pendingSend is a held message, as Lody's PendingSessionSend.
type pendingSend struct {
	id        string // its user turn's, which the message has once written
	sessionID string
	text      string // as it was typed
	at        time.Time
	turn      session.Turn
	// expand gives its text with its mentions expanded, as it is written.
	expand      func(ctx context.Context) (string, []map[string]any)
	attachments []sendAttachment
	// queue is a message routed to the queue because the agent was busy:
	// it shows in Up next rather than the conversation, and is written
	// there.
	queue   bool
	err     string // why it was not sent, which holds the ones behind it
	seq     int
	replica *session.Replica // its conversation's, as it was sent
}

// pendingSends are the workspace's held messages in the order sent, as
// Lody's createPendingSessionSends: state of the main thread, with the
// uploads and writes running off it.
type pendingSends struct {
	list []*pendingSend
	seq  int
	// epoch is the workspace's: what a closed one left running settles
	// into nothing.
	epoch   int
	running map[string]bool               // conversations whose first held message is going out
	cancels map[string]context.CancelFunc // what is under way, by message
}

// uploadAttachment uploads one attachment of a held message to Lody Cloud,
// reporting how far it has gone, and returns the block that stands for
// it; tests swap it.
var uploadAttachment = func(ctx context.Context, a *App, workspace, sessionID string, f pendingFile, progress func(sent, total int64)) (map[string]any, error) {
	return a.api.UploadAttachment(ctx, a.tokenNow(), workspace, sessionID, f.Path, progress)
}

// sendTurn writes a held message's turn and hands it to the machine, once
// its conversation, created with it, has synced; tests swap it.
var sendTurn = func(ctx context.Context, r *session.Replica, t session.Turn) (string, error) {
	if err := r.WaitReady(ctx); err != nil {
		return session.NotSent, err
	}
	return r.Send(ctx, t)
}

func (q *pendingSends) find(id string) *pendingSend {
	for _, s := range q.list {
		if s.id == id {
			return s
		}
	}
	return nil
}

// head is a conversation's first held message, the one that goes next.
func (q *pendingSends) head(sessionID string) *pendingSend {
	for _, s := range q.list {
		if s.sessionID == sessionID {
			return s
		}
	}
	return nil
}

// hasSession reports whether a conversation holds a message, which a new
// one waits behind.
func (q *pendingSends) hasSession(sessionID string) bool { return q.head(sessionID) != nil }

func (q *pendingSends) remove(id string) {
	out := q.list[:0]
	for _, s := range q.list {
		if s.id != id {
			out = append(out, s)
		}
	}
	clear(q.list[len(out):])
	q.list = out
}

// dispose drops every held message, stopping what is under way, as
// closing the workspace does.
func (q *pendingSends) dispose() {
	for _, cancel := range q.cancels {
		cancel()
	}
	*q = pendingSends{seq: q.seq, epoch: q.epoch + 1}
}

// holdSend holds a message, last of its conversation's, and starts the
// conversation's sends if none is going out, as Lody's enqueue.
func (a *App) holdSend(s *pendingSend) {
	q := &a.pending
	q.seq++
	s.seq = q.seq
	q.list = append(q.list, s)
	a.drainSends(s.sessionID)
}

// drainSends sends a conversation's first held message, unless one is
// going out already or it failed, which holds the rest behind it.
func (a *App) drainSends(sessionID string) {
	q := &a.pending
	head := q.head(sessionID)
	if q.running[sessionID] || head == nil || head.err != "" {
		return
	}
	a.attemptSend(head)
}

// writeJob is what a held message's write takes, read on the main thread
// once its attachments are ready.
type writeJob struct {
	r      *session.Replica
	turn   session.Turn
	expand func(ctx context.Context) (string, []map[string]any)
	err    error
}

// attemptSend sends a held message. Its attachments not yet uploaded go
// one at a time, each reporting as it goes; after one fails the rest are
// still tried, so that sending again moves only what failed, and the
// first failure is the message's. Ready, its turn is written through its
// conversation's replica as it is then.
func (a *App) attemptSend(s *pendingSend) {
	q := &a.pending
	if q.running == nil {
		q.running, q.cancels = map[string]bool{}, map[string]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	id, sid, epoch := s.id, s.sessionID, q.epoch
	q.running[sid], q.cancels[id] = true, cancel
	workspace := ""
	if a.conn != nil {
		workspace = a.conn.ID
	}
	type upload struct {
		i int
		f pendingFile
	}
	var todo []upload
	for i, at := range s.attachments {
		if at.ready == nil {
			todo = append(todo, upload{i, at.file})
		}
	}
	// patch changes attachment i, while the message is still held: one
	// canceled is gone, and what its attempt still reports with it.
	patch := func(i int, fn func(*sendAttachment)) {
		a.update(func() {
			if s := q.find(id); s != nil && i < len(s.attachments) {
				fn(&s.attachments[i])
			}
		})
	}
	settle := func(state string, err error) {
		a.update(func() { a.sendSettled(epoch, id, sid, state, err) })
	}
	go func() {
		defer cancel()
		var failure error
		for _, u := range todo {
			if ctx.Err() != nil {
				break
			}
			block, err := uploadAttachment(ctx, a, workspace, sid, u.f, func(sent, total int64) {
				if total > 0 {
					pct := float32(sent) * 100 / float32(total)
					patch(u.i, func(at *sendAttachment) { at.progress = pct })
				}
			})
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				if failure == nil {
					failure = err
				}
				reason := err.Error()
				patch(u.i, func(at *sendAttachment) { at.err, at.progress = reason, 0 })
				continue
			}
			patch(u.i, func(at *sendAttachment) { at.ready, at.err, at.progress = block, "", 100 })
		}
		if ctx.Err() != nil || failure != nil {
			settle(session.NotSent, failure)
			return
		}
		jobs := make(chan writeJob, 1)
		a.update(func() {
			s := q.find(id)
			if s == nil || ctx.Err() != nil {
				jobs <- writeJob{}
				return
			}
			t := s.turn
			t.ID, t.Attachments = s.id, nil
			for _, at := range s.attachments {
				t.Attachments = append(t.Attachments, at.ready)
			}
			r := a.replicaFor(s)
			job := writeJob{r: r, turn: t, expand: s.expand}
			if r == nil {
				job.err = errors.New("session: not open")
			}
			jobs <- job
		})
		var job writeJob
		select {
		case job = <-jobs:
		case <-ctx.Done():
		}
		if job.r == nil {
			settle(session.NotSent, job.err)
			return
		}
		if job.expand != nil {
			job.turn.Text, job.turn.TextSpans = job.expand(ctx)
		}
		sendCtx, cancelSend := context.WithTimeout(ctx, 60*time.Second)
		state, err := sendTurn(sendCtx, job.r, job.turn)
		cancelSend()
		settle(state, err)
	}()
}

// sendSettled ends a held message's attempt: written, it leaves, and its
// conversation's next goes; not, it keeps why and holds the rest. One
// canceled meanwhile has gone already, and the next goes.
func (a *App) sendSettled(epoch int, id, sessionID, state string, err error) {
	q := &a.pending
	if epoch != q.epoch {
		return
	}
	delete(q.running, sessionID)
	delete(q.cancels, id)
	s := q.find(id)
	switch {
	case s == nil:
	case state == session.NotSent && err != nil:
		s.err = sendErrorText(err)
		return
	case state == session.NotSent:
		// Stopped without a reason: it is tried again when sent again.
		s.err = locales.PendingMessageUploadFailed()
		return
	default:
		q.remove(id)
		if err != nil {
			a.sendError = locales.SentUnconfirmed()
		}
	}
	a.drainSends(sessionID)
}

// sendErrorText says why a message was not sent.
func sendErrorText(err error) string {
	if strings.Contains(err.Error(), "free_session_turn_limit_reached") {
		return locales.SendFreeTurnLimit()
	}
	return err.Error()
}

// retrySend sends a failed message again, with what of it did not upload
// starting over, as Lody's retry.
func (a *App) retrySend(id string) {
	s := a.pending.find(id)
	if s == nil {
		return
	}
	s.err = ""
	for i := range s.attachments {
		if at := &s.attachments[i]; at.ready == nil {
			at.err, at.progress = "", 0
		}
	}
	a.drainSends(s.sessionID)
}

// cancelSend drops a held message, stopping what of it is under way; its
// conversation's next goes once that has stopped, or at once.
func (a *App) cancelSend(id string) {
	q := &a.pending
	s := q.find(id)
	if s == nil {
		return
	}
	if cancel := q.cancels[id]; cancel != nil {
		cancel()
	}
	q.remove(id)
	if !q.running[s.sessionID] {
		a.drainSends(s.sessionID)
	}
}

// cancelSessionSends drops the held messages of conversations archived or
// deleted, as Lody's cancelSessions.
func (a *App) cancelSessionSends(gone func(sessionID string) bool) {
	for _, s := range append([]*pendingSend(nil), a.pending.list...) {
		if gone(s.sessionID) {
			a.cancelSend(s.id)
		}
	}
}

// replicaFor is the replica a held message is written through: its
// conversation's open one, else the one it was sent from.
func (a *App) replicaFor(s *pendingSend) *session.Replica {
	if a.replica != nil && a.replica.ID == s.sessionID {
		return a.replica
	}
	return s.replica
}

// landed reports whether the turn id shows already, in the conversation
// or its queue, which a held message gives way to.
func landed(v *session.View, id string) bool {
	if v == nil {
		return false
	}
	for _, e := range v.Entries {
		if e.ID == id {
			return true
		}
	}
	for _, e := range v.Queue {
		if e.ID == id {
			return true
		}
	}
	return false
}

// heldSends are a conversation's held messages that show in it, or with
// queue in its queue, as Lody's useSessionPendingMessages and
// selectPendingQueueSends: the ones whose turn shows already are left
// out.
func (a *App) heldSends(sessionID string, v *session.View, queue bool) []*pendingSend {
	var out []*pendingSend
	for _, s := range a.pending.list {
		if s.sessionID == sessionID && s.queue == queue && !landed(v, s.id) {
			out = append(out, s)
		}
	}
	return out
}

// bytes are a held message's attachment bytes, in all and sent: an
// attachment uploaded counts in full.
func (s *pendingSend) bytes() (total, sent float64) {
	for _, at := range s.attachments {
		size := float64(at.file.Size)
		total += size
		if at.ready != nil {
			sent += size
		} else {
			sent += size * float64(min(100, max(0, at.progress))) / 100
		}
	}
	return total, sent
}

// sendProgress is how far bytes have gone, as Lody's progressOf: short of
// done until written, and, with no bytes to measure, at the start of the
// ring; and whether there were bytes.
func sendProgress(total, sent float64) (int, bool) {
	if total <= 0 {
		return 5, false
	}
	return int(min(99, math.Round(sent/total*100))), true
}

// sendStatus is what the sidebar shows of a conversation's held messages,
// as Lody's deriveSessionSendStatuses: whether one failed, and how far
// they have gone together.
type sendStatus struct {
	held, failed bool
	progress     int
	known        bool // there are bytes to measure
	sent, total  int64
}

func (a *App) sendStatus(sessionID string) sendStatus {
	var st sendStatus
	var total, sent float64
	for _, s := range a.pending.list {
		if s.sessionID != sessionID {
			continue
		}
		st.held = true
		st.failed = st.failed || s.err != ""
		t, d := s.bytes()
		total, sent = total+t, sent+d
	}
	st.progress, st.known = sendProgress(total, sent)
	st.sent, st.total = int64(math.Round(sent)), int64(total)
	return st
}

// label is the send mark's spoken name: the bytes shown only while there
// are some to measure, as Lody's useSessionSendLabel.
func (st sendStatus) label() string {
	switch {
	case st.failed:
		return locales.SendStatusFailed()
	case !st.known:
		return locales.SendStatusSendingWithoutSize()
	}
	return locales.SendStatusSending(fileSize(st.sent), fileSize(st.total))
}
