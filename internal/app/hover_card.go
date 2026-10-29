package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// Resting the pointer on a sidebar chat shows Lody's info card beside it:
// where it works, its branch, who started it on which machine, and its
// pull request and changes. The first card waits 650ms and rises in;
// while one was shown in the last 3s the next comes at once, in place, as
// the pointer runs down the list. One card shows at a time, and it stays
// 180ms after the pointer leaves, for the pointer to reach it.

const (
	cardWarmup = 650 * time.Millisecond
	cardWarm   = 3 * time.Second
	cardGrace  = 180 * time.Millisecond
)

// hoverCard is the sidebar's info card.
type hoverCard struct {
	candidate string    // the chat under the pointer, waiting out the warmup
	since     time.Time // when it came under the pointer
	shown     string    // the chat whose card shows
	inRun     bool      // shown warm: it appears in place
	left      time.Time // when the pointer left the chat and its card
	lastShown time.Time // when a card was last shown, for the warm window
	pressed   bool      // a press on a row hides cards until the pointer moves on
	copied    time.Time // the branch was copied
	// The row whose card shows, as this frame built it, the card drawn
	// once after the list, and whether the pointer was on the row or the
	// card in the last frame.
	row        ui.Element
	sess       *catalog.Session
	rowHovered bool
	cardHover  bool
}

// sessionHover runs the card of a sidebar row: hovered is whether the
// pointer is on the row, pressed whether it pressed it.
func (a *App) sessionHover(c *ui.Context, p *palette, row ui.Element, s *catalog.Session, hovered, pressed bool) {
	hc := &a.card
	now := time.Now()
	if pressed {
		if hc.shown == s.ID {
			hc.lastShown = now
		}
		// It stays the candidate, pressed, until the pointer moves on.
		hc.shown, hc.candidate, hc.pressed = "", s.ID, true
		return
	}
	if hovered && hc.candidate != s.ID && hc.shown != s.ID {
		hc.candidate, hc.since, hc.pressed = s.ID, now, false
	}
	if hovered && hc.candidate == s.ID && hc.shown != s.ID && !hc.pressed {
		warm := hc.shown != "" || now.Sub(hc.lastShown) < cardWarm
		switch {
		case warm:
			hc.shown, hc.inRun = s.ID, true
		case now.Sub(hc.since) >= cardWarmup:
			hc.shown, hc.inRun = s.ID, false
		default:
			c.After(cardWarmup - now.Sub(hc.since))
		}
		hc.left = time.Time{}
	}
	if !hovered && hc.candidate == s.ID && hc.shown != s.ID {
		hc.candidate = ""
	}
	if hc.shown != s.ID {
		return
	}
	hc.row, hc.sess, hc.rowHovered = row, s, hovered
}

// sessionCardOverlay draws the card of the row that has it, once, after
// the list: another row taking it over in the frame replaces it in place.
// It stays 180ms after the pointer leaves the row and the card.
func (a *App) sessionCardOverlay(c *ui.Context, p *palette) {
	hc := &a.card
	row, s, hovered := hc.row, hc.sess, hc.rowHovered
	hc.row, hc.sess, hc.rowHovered = ui.Element{}, nil, false
	if s == nil || hc.shown != s.ID {
		return
	}
	now := time.Now()
	var card ui.Element
	ui.Overlay(c, func() {
		card = a.sessionCard(c, p, row, s)
	})
	if hovered || card.Hovered() {
		hc.left = time.Time{}
		return
	}
	if hc.left.IsZero() {
		hc.left = now
	}
	if now.Sub(hc.left) >= cardGrace {
		hc.shown, hc.lastShown, hc.candidate = "", now, ""
		return
	}
	c.After(cardGrace - now.Sub(hc.left))
}

// sessionCard is the card itself, 288 wide, 6px to the right of its row.
func (a *App) sessionCard(c *ui.Context, p *palette, row ui.Element, s *catalog.Session) ui.Element {
	hc := &a.card
	// One card under one key: another taking over replaces it at once, in
	// place; a card closing fades as it came.
	card := ui.Column(c.Key("info-card")).Width(288).Padding(12).Gap(12).Radius(12).Border(0.5, p.CardRim).Background(p.Popover).
		Shadow(0, 12, 32, -8, ui.RGBA(0, 0, 0, 0.18)).AttachTo(row, ui.AnchorTopEnd, ui.AnchorTopStart).Margin(0, 0, 0, 6)
	if !hc.inRun {
		// Side-aware: it starts 4px further right and closes that step.
		card.Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Enter: &ui.Motion{X: 4}, Exit: &ui.Motion{X: 4}})
	}
	now := time.Now()
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, firstNonEmpty(s.Title, locales.UntitledSession())).FontSize(sizeSub).FontWeight(600).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
			if s.LastMessageAt > 0 {
				ui.Text(c, compactAge(time.UnixMilli(int64(s.LastMessageAt)), now)).FontSize(sizeFoot).TextColor(p.Secondary).Shrink(0)
			}
		})
		fact := func(key string, icon *ui.SVG, label string, value func()) {
			ui.Row(c.Key(key)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary).Label(label).Tooltip(label).Shrink(0)
				value()
			})
		}
		text := func(v string) { ui.Text(c, v).FontSize(sizeFoot).TextColor(p.Text).SingleLine().MinWidth(0) }
		facts := 0
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			repo := s.RepoFullName
			if strings.HasPrefix(s.ProjectID, "github:") {
				repo = strings.TrimPrefix(s.ProjectID, "github:")
			}
			switch {
			case repo != "":
				fact("place", iconGithub, locales.InfoRepository(), func() { text(repo) })
				facts++
			default:
				if pr := a.catalog.Project(s.ProjectID); pr != nil {
					fact("place", iconFolder, locales.InfoFolder(), func() { text(pr.Name) })
					facts++
				}
			}
			if s.BranchName != "" {
				label := locales.InfoBranch()
				if a.sessionWorktree(s) {
					label = locales.InfoWorktree()
				}
				fact("branch", iconBranch, label, func() {
					branch := s.BranchName
					b := ui.ButtonBase(c.Key("copy-branch")).Padding(0, 4).Margin(0, 0, 0, -4).Radius(4).MinWidth(0).Cursor(ui.CursorPointer).Gap(6).
						Label(locales.InfoCopy()).Tooltip(locales.InfoCopy()).OnClick(func() {
						writeClipboard(branch)
						hc.copied = time.Now()
					})
					if b.Hovered() {
						b.Background(p.Text.Alpha(0.06))
					}
					b.Children(func() {
						ui.Text(c, branch).FontSize(sizeFoot).Font(fontMono).TextColor(p.Text).SingleLine().MinWidth(0)
						if since := time.Since(hc.copied); since < 1500*time.Millisecond {
							ui.Text(c, locales.InfoCopied()).FontSize(sizeCaption).TextColor(p.Secondary).Shrink(0)
							c.After(1500*time.Millisecond - since)
						}
					})
				})
				facts++
			}
			machine := a.machineLabel(s.MachineID)
			if s.UserID != "" {
				fact("author", iconUser, locales.InfoAuthor(), func() {
					text(a.authorName(s.UserID))
					if machine != "" {
						ui.Row(c).Gap(4).AlignItems(ui.Center).MinWidth(0).Tooltip(locales.InfoMachine()).Children(func() {
							ui.Icon(c, iconMonitor).Size(12, 12).TextColor(p.Secondary)
							ui.Text(c, machine).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine().MinWidth(0)
						})
					}
				})
				facts++
			} else if machine != "" {
				fact("machine", iconMonitor, locales.InfoMachine(), func() { text(machine) })
				facts++
			}
			if facts == 0 {
				fact("chat", iconMessageSquare, locales.InfoChat(), func() { text(locales.InfoChat()) })
			}
		})
		pr := s.LatestPR()
		changes := s.DiffAdd != 0 || s.DiffDel != 0
		if pr == nil && !changes {
			return
		}
		ui.Box(c).FillWidth().Height(0.5).Background(p.Border)
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if pr != nil {
				icon, hue := prTone(p, pr.Status)
				ui.Icon(c, icon).Size(14, 14).TextColor(hue)
				label := prStatusLabel(pr.Status)
				number := "PR"
				if n := pr.PRNumber(); n > 0 {
					number = "#" + itoa(n)
				}
				url := pr.URL
				open := ui.ButtonBase(c.Key("open-pr")).Gap(4).Padding(0, 4).Radius(4).Cursor(ui.CursorPointer).Label(locales.InfoOpenPR() + " " + number).
					OnClick(func() { openExternal(url) })
				if open.Hovered() {
					open.Background(p.Text.Alpha(0.06))
				}
				open.Children(func() {
					ui.Text(c, number).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).FontFeatures("tnum")
					ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				if verdict := ciVerdict(s.PRState[pr.URL].CI); verdict != "" {
					ui.Text(c, "·").FontSize(sizeFoot).TextColor(p.Tertiary)
					ui.Text(c, verdict).FontSize(sizeFoot).TextColor(p.Secondary)
				}
			}
			ui.Spacer(c)
			if changes {
				ui.Row(c).Gap(4).Tooltip(locales.InfoChanges()).Children(func() {
					ui.Text(c, "+"+itoa(int(s.DiffAdd))).FontSize(sizeFoot).Font(fontMono).TextColor(p.DiffAdd)
					ui.Text(c, "−"+itoa(int(s.DiffDel))).FontSize(sizeFoot).Font(fontMono).TextColor(p.DiffDel)
				})
			}
		})
	})
	return card
}

// ciVerdict is a pull request's CI in words, as the card says it.
func ciVerdict(ci string) string {
	switch ci {
	case "s":
		return locales.InfoCiPassing()
	case "f", "e":
		return locales.InfoCiFailing()
	case "x":
		return locales.InfoCiExpected()
	case "p":
		return locales.InfoCiRunning()
	}
	return ""
}

// sessionWorktree reports whether a chat works in a worktree of its own:
// a GitHub project's, or a local project's asked for one.
func (a *App) sessionWorktree(s *catalog.Session) bool {
	if s.IsWorktree || strings.HasPrefix(s.ProjectID, "github:") {
		return true
	}
	v, _ := s.Project["useWorktree"].(bool)
	return v
}

// authorName is who started a chat: the signed-in user's own name, or a
// member's.
func (a *App) authorName(id string) string {
	if a.account != nil && id == a.account.User.ID {
		return firstNonEmpty(a.account.User.Name, a.account.User.Email)
	}
	return a.userName(id)
}
