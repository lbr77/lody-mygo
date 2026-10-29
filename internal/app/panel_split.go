package app

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// The conversation and the panel beside it share the card as Lody's
// resizable panels do: the panel takes 40% at first, at least 280 DIPs
// and never more than the conversation, behind a hairline that a drag
// moves. Under the pointer the handle draws a 2px line in the ring's ink
// over the hairline, once the pointer has rested 150ms, its color fading
// over 150ms, and stronger while dragged; it fades with the panel as it
// opens and closes. Dragged narrower than half its least, the panel folds
// away.

const (
	panelMin  = 280 // the panel's least width
	panelGrip = 12  // how far either side of its edge the handle reaches
)

// ringInk is Lody's sidebar ring.
func ringInk(p *palette) ui.Color {
	if p.Dark {
		return ui.Hex("#479EF5")
	}
	return ui.Hex("#5C8DEF")
}

func (a *App) sessionSplit(c *ui.Context, p *palette, k float32) {
	if k <= 0 {
		ui.Column(c).Fill().Children(func() { a.sessionMain(c, p) })
		return
	}
	if a.panelPercent == 0 {
		a.panelPercent = 60
	}
	split := ui.Row(c.Key("split")).Fill().AlignItems(ui.Stretch)
	sb := split.Bounds()
	total := sb.W
	if total <= 0 {
		w, _ := c.Size()
		total = w - 8
		if !a.sidebarHidden {
			total -= a.sidebarW()
		}
	}
	if total > 0 {
		a.panelPercent = min(a.panelPercent, max(50, 100-panelMin/total*100))
	}
	panelW := total * (100 - a.panelPercent) / 100 * k
	split.Children(func() {
		ui.Column(c.Key("main")).Grow(1).MinWidth(0).Children(func() { a.sessionMain(c, p) })
		panel := ui.Column(c.Key("panel")).Width(panelW).Shrink(0).MinWidth(0).ClipX().BorderWidth(0, 0, 0, 1).BorderColor(p.Border)
		panel.Children(func() {
			// Closing, the panel's content is hidden at once, as Lody's
			// visibility: hidden, while its room shrinks away.
			if !a.panelOpen {
				return
			}
			inner := ui.Column(c).Fill().MinWidth(0)
			if k < 1 {
				inner.Translate(panelW/k*(1-k), 0)
			}
			inner.Children(func() { a.inspector(c, p) })
		})
		a.panelHandle(c, p, sb, total, panelW, k)
	})
}

// panelHandle is the panel's edge, which a drag moves.
func (a *App) panelHandle(c *ui.Context, p *palette, sb ui.Rect, total, panelW, k float32) {
	h := ui.Box(c.Key("panel-handle")).Absolute().Top(0).Bottom(0).Left(total - panelW - panelGrip).Width(2 * panelGrip).
		Cursor(ui.CursorResizeColumn).Label(locales.PanelResize()).Opacity(k)
	if !a.panelOpen {
		h.PassThrough()
		return
	}
	if dx, _, ok := h.Dragged(); ok && dx != 0 && sb.W > 0 {
		// The edge follows the pointer, past where the panel stops: from
		// where the drag took it, as a frame may build more than once and
		// must move it once.
		if a.panelDrag == nil {
			edge := sb.X + sb.W - panelW
			if b := h.Bounds(); b.W > 0 {
				edge = b.X + panelGrip
			}
			a.panelDrag = &edge
		}
		*a.panelDrag += dx
		w := sb.X + sb.W - *a.panelDrag
		if w < panelMin/2 {
			a.panelOpen, a.panelDrag = false, nil
		} else {
			w = min(max(w, panelMin), total/2)
			a.panelPercent = 100 - w/total*100
		}
	}
	if _, _, ended := h.DragEnded(); ended {
		a.panelDrag = nil
		a.savePrefs()
	}
	ink, anim := ringInk(p).Alpha(0), ui.Animation{Duration: 150 * time.Millisecond, Ease: twEase}
	switch {
	case h.Dragging():
		ink = ringInk(p).Alpha(0.7)
	case h.Hovered():
		ink, anim.Delay = ringInk(p).Alpha(0.5), 150*time.Millisecond
	}
	ink = h.AnimateColor("line", ink, anim)
	h.Children(func() {
		ui.Box(c).Absolute().Left(panelGrip).Top(0).Bottom(0).Width(2).Background(ink)
	})
}
