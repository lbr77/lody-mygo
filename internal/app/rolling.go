package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// A figure that changes rolls its digits as Lody's NumberFlow: only the
// characters that changed move, the new coming up from below as the value
// grows (down from above as it shrinks) over 900ms on NumberFlow's curve,
// fading in over the first 450ms, the old leaving the other way. It never
// rolls as it first shows.

const (
	rollMove = 900 * time.Millisecond
	rollFade = 450 * time.Millisecond
)

// roll is a rolling figure's last change.
type roll struct {
	prev, cur string
	value     float64
	down      bool
	at        time.Time
}

// rollingNumber draws text, the figure value formatted, as style sets each
// of its characters, rolling what changed since it last showed.
func (a *App) rollingNumber(c *ui.Context, key, text string, value float64, style func(t ui.Element) ui.Element) ui.Element {
	if a.rolls == nil {
		a.rolls = map[string]*roll{}
	}
	now := c.Now()
	r := a.rolls[key]
	switch {
	case r == nil:
		r = &roll{prev: text, cur: text, value: value}
		a.rolls[key] = r
	case text != r.cur:
		r.prev, r.cur, r.down, r.at = r.cur, text, value < r.value, now
		r.value = value
	}
	elapsed := now.Sub(r.at)
	move, fade := float32(1), float32(1)
	if !r.at.IsZero() && elapsed < rollMove {
		move = numberFlowEase(float32(elapsed) / float32(rollMove))
		fade = numberFlowEase(min(1, float32(elapsed)/float32(rollFade)))
		c.AnimationFrame()
	}
	old, cur := []rune(r.prev), []rune(r.cur)
	if move >= 1 {
		old = cur
	}
	// Right-aligned, as the figures' places are.
	n := max(len(old), len(cur))
	pad := func(s []rune) []rune {
		out := make([]rune, n)
		for i := range out {
			out[i] = ' '
		}
		copy(out[n-len(s):], s)
		return out
	}
	old, cur = pad(old), pad(cur)
	row := ui.Row(c.Key(key)).AlignItems(ui.End).Label(text)
	row.Children(func() {
		for i := range n {
			o, nw := old[i], cur[i]
			if o == nw {
				if nw != ' ' {
					style(ui.Text(c.Key(i), string(nw))).SingleLine().Shrink(0)
				}
				continue
			}
			cell := ui.Box(c.Key(i)).Shrink(0).ClipY()
			h := cell.Bounds().H
			dir := float32(1)
			if r.down {
				dir = -1
			}
			cell.Children(func() {
				if nw != ' ' {
					t := style(ui.Text(c.Key("new"), string(nw))).SingleLine().Shrink(0).Opacity(fade)
					t.Translate(0, dir*h*(1-move))
				} else {
					// The place it leaves keeps the old character's room while
					// it goes.
					style(ui.Text(c.Key("room"), string(o))).SingleLine().Shrink(0).Opacity(0)
				}
				if o != ' ' {
					style(ui.Text(c.Key("old"), string(o))).SingleLine().Absolute().Left(0).Top(0).Opacity(1-fade).Translate(0, -dir*h*move)
				}
			})
		}
	})
	return row
}
