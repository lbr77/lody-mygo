package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// chatMotion is what the conversation's motion keeps between frames.
type chatMotion struct {
	// lastUser is the open chat's last message from the user, as
	// "session/entry", and glideUntil how long the conversation glides to
	// its end: a message just sent glides up over 360ms, as Lody's send,
	// while a reply streaming in is followed at once.
	lastUser   string
	glideUntil time.Time
	// copiedTurn is the reply just copied, and copiedAt when.
	copiedTurn string
	copiedAt   time.Time
	// scroll is where the open chat's list is scrolled, for the find bar
	// to glide a result to the top; scrollFor is the chat it is of.
	scroll    *ui.ScrollState
	scrollFor string
	// sent is the message just sent, held near the top as the reply comes.
	sent sentAnchor
	// nudge is how far the next frame scrolls the conversation: an offset
	// set once the list is built is written over as it is laid out, so it
	// is set before the list builds (applyNudge).
	nudge float32
	// inflight is how far this frame has scrolled the conversation so far:
	// a frame builds up to three times before it is laid out, so what its
	// builds read of the layout is the last frame's, and so far off.
	inflight   float32
	inflightAt time.Time
	// viewH is how tall the conversation shows, and rows how many rows its
	// list has, as the last build had them.
	viewH float32
	rows  int
}

// applyNudge scrolls session's conversation by the nudge asked for, in
// the frame of now.
func (m *chatMotion) applyNudge(session string, now time.Time) {
	if !now.Equal(m.inflightAt) {
		m.inflight, m.inflightAt = 0, now
	}
	if st := m.scroll; st != nil && m.scrollFor == session && m.nudge != 0 {
		y := max(0, min(st.MaxY, st.Y+m.nudge))
		m.inflight += y - st.Y
		st.Y = y
	}
	m.nudge = 0
}

// chatScroll is the ScrollState the open chat's list tracks: a fresh one
// for a list built anew, so that it starts where the list puts itself
// (its end) rather than where another left off.
func (m *chatMotion) chatScroll(session string, born bool) *ui.ScrollState {
	if m.scroll == nil || m.scrollFor != session || born {
		m.scroll, m.scrollFor = &ui.ScrollState{}, session
	}
	return m.scroll
}
