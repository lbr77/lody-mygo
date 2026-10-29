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
