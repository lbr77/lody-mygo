package app

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's pending sends: a message whose attachments still have to upload,
// or one sent behind such a message in the same conversation, is held
// rather than written, and the composer is free at once. Each
// conversation's held messages go out in the order sent, one at a time,
// while the workspace is connected: a message's attachments upload one
// after another, then its user turn is written and handed to the machine.
// A message that fails holds the conversation's later ones behind it
// until it is sent again or canceled. Held messages are kept on disk, as
// lody-ios's outbox and Lody's durable inputs (outbox.go), so closing the
// workspace or quitting does not lose them: they go on when it opens
// again. A held message shows where it is going, in the conversation as a
// pending message or, routed to the queue because the agent was busy, as
// a pending row of Up next, and gives way to the real message once that
// is written.

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
	// fresh is a new conversation's first message, which the conversation
	// was made for: canceled before anything was written there, the
	// conversation goes too, as Lody never writes it.
	fresh bool
	// persisted is a message kept on disk as it stands, which leaving
	// does not lose.
	persisted bool
	// writing is a message whose write has begun: taken up again after a
	// restart, it is written only if the conversation hasn't it, never
	// twice.
	writing bool
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
	// scope is the file the workspace's held messages are kept in, once
	// taken up from it (restoreSends).
	scope string
}

// uploadAttachment uploads one attachment of a held message to Lody Cloud,
// reporting how far it has gone, and returns the block that stands for
// it; tests swap it.
var uploadAttachment = func(ctx context.Context, a *App, workspace, sessionID string, f pendingFile, progress func(sent, total int64)) (map[string]any, error) {
	return a.api.UploadAttachment(ctx, a.tokenNow(), workspace, sessionID, f.Path, progress)
}

// sendTurn writes a held message's turn and hands it to the machine, once
// its conversation, created with it, has synced; tests swap it. A turn
// the conversation has already, a held message's sent again after a
// restart, counts as written.
var sendTurn = func(ctx context.Context, r *session.Replica, t session.Turn) (string, error) {
	if err := r.WaitReady(ctx); err != nil {
		return session.NotSent, err
	}
	state, err := r.Send(ctx, t)
	if errors.Is(err, session.ErrTurnExists) {
		return session.Uploaded, nil
	}
	return state, err
}

// openSendReplica follows a conversation a held message is written to
// while it isn't open, as one taken up after a restart, until done is
// called; tests swap it.
var openSendReplica = func(a *App, sessionID string) (r *session.Replica, done func()) {
	if a.conn == nil {
		return nil, nil
	}
	r = session.OpenLocal(a.conn, a.local, sessionID, func(*session.View) {})
	return r, r.Close
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

// dispose lets go of every held message, stopping what is under way, as
// closing the workspace does; what is kept on disk stays, for when it
// opens again.
func (q *pendingSends) dispose() {
	for _, cancel := range q.cancels {
		cancel()
	}
	*q = pendingSends{seq: q.seq, epoch: q.epoch + 1}
}

// holdSend holds a message, last of its conversation's, keeps it on disk,
// and starts the conversation's sends if none is going out, as Lody's
// enqueue.
func (a *App) holdSend(s *pendingSend) {
	a.restoreSends()
	q := &a.pending
	q.seq++
	s.seq = q.seq
	q.list = append(q.list, s)
	a.saveSends()
	a.drainSends(s.sessionID)
}

// sendsConnected is whether held messages go now: the workspace's sync
// is live, as lody-ios's outbox waits for a connection.
func (a *App) sendsConnected() bool { return a.syncState.Live }

// drainSends sends a conversation's first held message, unless one is
// going out already or it failed, which holds the rest behind it, or the
// workspace is offline, when it waits.
func (a *App) drainSends(sessionID string) {
	q := &a.pending
	head := q.head(sessionID)
	if q.running[sessionID] || head == nil || head.err != "" || !a.sendsConnected() {
		return
	}
	a.attemptSend(head)
}

// writeJob is what a held message's write takes, read on the main thread
// once its attachments are ready: done lets go of a replica opened for
// it, once written.
type writeJob struct {
	r      *session.Replica
	done   func()
	turn   session.Turn
	expand func(ctx context.Context) (string, []map[string]any)
	err    error
}

// attemptSend sends a held message. Its attachments not yet uploaded go
// one at a time, each reporting as it goes and kept on disk once
// uploaded; after one fails the rest are still tried, so that sending
// again moves only what failed, and the first failure is the message's.
// Ready, it is marked as writing on disk and its turn written through its
// conversation's replica as it is then, or one opened for it.
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
	// patch changes attachment i, while the message is still held, and
	// with save keeps it so: one canceled is gone, and what its attempt
	// still reports with it, as is what a closed workspace's reports.
	patch := func(i int, save bool, fn func(*sendAttachment)) {
		a.update(func() {
			if s := q.find(id); s != nil && q.epoch == epoch && i < len(s.attachments) {
				fn(&s.attachments[i])
				if save {
					a.saveSends()
				}
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
					patch(u.i, false, func(at *sendAttachment) { at.progress = pct })
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
				patch(u.i, true, func(at *sendAttachment) { at.err, at.progress = reason, 0 })
				continue
			}
			patch(u.i, true, func(at *sendAttachment) { at.ready, at.err, at.progress = block, "", 100 })
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
			if t.MarkDispatch == nil {
				// Taken up after a restart, it dispatches as sent now.
				t.MarkDispatch = a.sync.MarkDispatch
			}
			expand := s.expand
			if expand == nil {
				expand = a.sendExpand(s.sessionID, s.text)
			}
			job := writeJob{r: a.replicaFor(s), turn: t, expand: expand}
			if job.r == nil {
				job.r, job.done = openSendReplica(a, s.sessionID)
			}
			if job.r == nil {
				job.err = errors.New("session: not open")
			} else if !s.writing {
				s.writing = true
				a.saveSends()
			}
			jobs <- job
		})
		var job writeJob
		select {
		case job = <-jobs:
		case <-ctx.Done():
		}
		if job.done != nil {
			defer job.done()
		}
		if job.r == nil || ctx.Err() != nil {
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
// conversation's next goes; not, it keeps why and holds the rest, or,
// the workspace gone offline meanwhile, waits for it to come back. One
// canceled meanwhile has gone already, and the next goes. What it comes
// to is kept on disk.
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
	case state == session.NotSent && !a.sendsConnected():
		s.writing = false
		s.wait()
		a.saveSends()
		return
	case state == session.NotSent && err != nil:
		s.writing, s.err = false, sendErrorText(err)
		a.saveSends()
		return
	case state == session.NotSent:
		// Stopped without a reason: it is tried again when sent again.
		s.writing, s.err = false, locales.PendingMessageUploadFailed()
		a.saveSends()
		return
	default:
		q.remove(id)
		a.saveSends()
		if err != nil {
			a.sendError = locales.SentUnconfirmed()
		}
	}
	a.drainSends(sessionID)
}

// wait readies a failed message to go again: what of it did not upload
// starts over.
func (s *pendingSend) wait() {
	s.err = ""
	for i := range s.attachments {
		if at := &s.attachments[i]; at.ready == nil {
			at.err, at.progress = "", 0
		}
	}
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
	s.wait()
	a.saveSends()
	a.drainSends(s.sessionID)
}

// cancelSend drops a held message, on disk too, stopping what of it is
// under way; its conversation's next goes once that has stopped, or at
// once.
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
	a.saveSends()
	if s.fresh && !q.hasSession(s.sessionID) && a.unwritten(s.sessionID) {
		a.dropFreshSession(s.sessionID)
		return
	}
	if !q.running[s.sessionID] {
		a.drainSends(s.sessionID)
	}
}

// unwritten reports whether nothing was written in a conversation yet.
func (a *App) unwritten(sessionID string) bool {
	if a.selected == sessionID && a.view != nil {
		return len(a.view.Entries) == 0 && len(a.view.Queue) == 0
	}
	s := a.catalog.Session(sessionID)
	return s == nil || s.LastMessageAt == 0
}

// dropFreshSession takes away a conversation made for a first message that
// was canceled, going back to New chat from it.
func (a *App) dropFreshSession(sessionID string) {
	if a.selected == sessionID {
		a.selected = ""
	}
	a.catalogAction("delete the session", func(ctx context.Context, sync *catalog.Sync) error {
		return sync.Delete(ctx, sessionID)
	})
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
