package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Context chips, as lody-ios puts them at the start of the quick replies'
// row: the server the session's agent runs, to open in the browser panel
// or share, and the subtasks still running. They stay while the user
// types.

// previewResource is the server a chip names: the candidate the agent
// reported, else the connection made or being made, on the machine's
// loopback only.
func previewResource(pv *session.Preview) (target session.PreviewTarget, active, ok bool) {
	if pv == nil {
		return target, false, false
	}
	switch pv.Candidate {
	case "reported", "validating", "available":
	default:
		if pv.Connection != "creating" && pv.Connection != "active" {
			return target, false, false
		}
	}
	target = pv.Target
	loopback := target.Host == "localhost" || target.Host == "127.0.0.1" || target.Host == "::1"
	if target.Protocol != "http" && target.Protocol != "https" || !loopback || target.Port < 1 || target.Port > 65535 {
		return target, false, false
	}
	return target, pv.Connection == "active" || pv.Connection == "creating", true
}

// runningSubtasks counts the subtasks still pending or running, and the
// entry holding the last of them.
func runningSubtasks(v *session.View) (n, entry int) {
	entry = -1
	for i, e := range v.Entries {
		for _, t := range collectTasks(e.Items) {
			if t.State == "pending" || t.State == "running" || t.Status == "in_progress" || t.Status == "pending" {
				n++
				entry = i
			}
		}
	}
	return n, entry
}

// contextChips builds the chips, reporting whether there were any.
func (a *App) contextChips(c *ui.Context, p *palette, sess *catalog.Session, v *session.View) bool {
	if sess == nil || v == nil {
		return false
	}
	target, active, preview := previewResource(v.Preview)
	tasks, entry := runningSubtasks(v)
	if !preview && tasks == 0 {
		return false
	}
	chip := func(key string, icon *ui.SVG, label, help string, ink ui.Color) ui.Element {
		b := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, 12).Radius(16).Gap(6).AlignItems(ui.Center).Border(1, p.Border).
			Background(p.Composer).Cursor(ui.CursorPointer).Label(help).Transition(colorFade)
		tip(c, p, b, help)
		pressable(b, 0.96)
		if b.Hovered() {
			b.Background(p.Hover)
		}
		b.Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(ink).Shrink(0)
			ui.Text(c, label).FontSize(sizeSub).TextColor(ink).SingleLine().MaxWidth(220)
		})
		return b
	}
	if preview {
		label := fmt.Sprintf("%s:%d", strings.Trim(target.Host, "[]"), target.Port)
		ps := a.previewFor(sess.ID)
		if ps.starting {
			label = locales.ChipConnecting()
		}
		s := sess
		b := chip("preview", iconGlobe, label, locales.ChipOpen(label), p.Primary)
		b.OnClick(func() {
			in := a.inspectFor(a.selected)
			in.openPanel(tabPreview)
			a.panelOpen = true
		})
		lodyContextMenu(c, p, b, a.rowMenu("chip/preview/"+sess.ID), 28, func(m *lodyMenu) {
			if m.item("copy-link", iconLink, locales.ChipCopyLink(), menuItem{}) {
				if url := firstNonEmpty(ps.url, v.Preview.PublicURL); url != "" {
					writeClipboard(url)
					a.toast("success", locales.ChipCopied())
				} else {
					// Nothing shared yet: share it, in the browser panel.
					ps.address = fmt.Sprintf("%d", target.Port)
					a.startPreview(s, ps)
					in := a.inspectFor(a.selected)
					in.openPanel(tabPreview)
					a.panelOpen = true
				}
			}
			if (active || ps.url != "") && m.item("stop-sharing", iconCircleStop, locales.ChipStopSharing(), menuItem{}) {
				a.stopPreview(s, ps)
				a.toast("success", locales.ChipStopped())
			}
		})
	}
	if tasks > 0 {
		label := locales.ChipSubtasks(tasks)
		chip("tasks", iconLayers, label, label, p.Text).OnClick(func() { a.chatList.ScrollIntoView(entry) })
	}
	return true
}
