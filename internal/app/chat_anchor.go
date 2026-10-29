package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Lody holds a message just sent near the top of the conversation, 100px
// below where what came before it ends, so that its tail stays in view:
// the message glides there over 360ms, easing out as a cubic, and the
// reply streams in below it. For as long as what follows is too short to
// fill the view, room is made under it; once the reply fills the view, the
// conversation follows its end again, as it does at once for a message
// too tall to hold. Scrolling lets the message go, the room shrinking only
// as the reader leaves it.

// sentContext is how much of what came before a sent message stays above
// it.
const sentContext = 100

// chatPadBottom is the room under the conversation's last row.
const chatPadBottom = 24

// sentAnchor is the message just sent in a chat, and the room under the
// conversation.
type sentAnchor struct {
	session, id string
	held        bool // the message is held in place
	glided      bool // its glide there has started
	room        float32
	// As the last frame laid them out, in the window: the message's top
	// and height, and where the conversation ends; and whether the rows
	// that end it were built at all.
	top, h, end        float32
	seen, ended, built bool
}

// sendAnchor holds id, just sent in session, keeping the room already made.
func (s *sentAnchor) sendAnchor(session, id string) {
	room := s.room
	if s.session != session {
		room = 0
	}
	*s = sentAnchor{session: session, id: id, held: true, room: room}
}

// roomIn is the room under session's conversation.
func (s *sentAnchor) roomIn(session string) float32 {
	if s.session != session {
		return 0
	}
	return s.room
}

// holds reports whether session's conversation keeps from following its
// end: while a message is held, or room is left under it.
func (s *sentAnchor) holds(session string) bool {
	return s.session == session && (s.held || s.room > 0)
}

// noteRow keeps where the list laid out the row of entry id, the last
// entry's when last.
func (s *sentAnchor) noteRow(id string, b ui.Rect, last bool) {
	if s.held && id == s.id && b.H > 0 {
		s.top, s.h, s.seen = b.Y, b.H, true
	}
	if last {
		s.built = true
		if b.H > 0 {
			s.end, s.ended = b.Y+b.H, true
		}
	}
}

// noteEnd keeps where the list laid out its last row, the agent's status
// under the last entry. In its first frame at a new place, as an entry
// comes, it isn't laid out yet: the last entry's end stands in.
func (s *sentAnchor) noteEnd(b ui.Rect) {
	s.built = true
	if b.W > 0 {
		s.end, s.ended = b.Y+b.H, true
	}
}

// letGo stops holding the message, as scrolling does.
func (s *sentAnchor) letGo() { s.held = false }

// holdSent keeps the message just sent where Lody holds it, from where the
// list laid out its rows, and the room under the conversation to what it
// needs.
func (a *App) holdSent(list ui.Element) {
	s := &a.chatMotion.sent
	seen, ended, built := s.seen, s.ended, s.built
	s.seen, s.ended, s.built = false, false, false
	st := a.chatMotion.scroll
	lb := list.Bounds()
	if s.session != a.selected || st == nil || lb.H == 0 {
		return
	}
	bottom := lb.Y + lb.H - chatPadBottom
	follow := func() {
		s.held, s.room = false, 0
		a.chatList.ScrollToEnd()
		a.redraw()
	}
	if !s.held {
		if s.room > 0 {
			// Let go, the room shrinks to what keeps the reader in place;
			// once it is gone, the list follows its end again from there.
			room := float32(0)
			if ended {
				room = min(s.room, max(0, bottom-s.end))
			}
			if room != s.room {
				s.room = room
				a.redraw()
			}
		}
		return
	}
	if !seen {
		if s.glided {
			// Gone from the conversation.
			follow()
		}
		return
	}
	if !ended {
		if !built {
			// The reply runs past what the list builds.
			follow()
		}
		return
	}
	// What came before shows above it, as far as there is any.
	context := min(sentContext, max(0, st.Y+s.top-lb.Y-chatPadTop))
	target := lb.Y + chatPadTop + context
	if s.h > bottom-target {
		// Too tall to hold: it glides to the end, as a reply is followed.
		a.chatMotion.glideUntil = time.Now().Add(chatGlide.Duration)
		follow()
		return
	}
	room := bottom - target - (s.end - s.top)
	if room <= 0 {
		// The reply fills the view.
		follow()
		return
	}
	if room != s.room {
		s.room = room
		a.redraw()
	}
	switch delta := s.top - target; {
	case !s.glided && st.Y+delta > st.MaxY+0.5:
		// A glide stops where the content ends: it waits for the room to
		// be laid out.
		a.redraw()
	case !s.glided:
		st.ScrollTo(0, st.Y+delta, chatGlide)
		s.glided = true
	case !st.Scrolling() && abs32(delta) > 0.5:
		// What came before changed: the message stays where it is held.
		st.Y += delta
	}
}
