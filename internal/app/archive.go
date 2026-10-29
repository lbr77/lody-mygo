package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// archiveView is the Archived chats page, in the workspace card.
func (a *App) archiveView(c *ui.Context, p *palette) {
	left := float32(16)
	if a.sidebarHidden {
		left = 76
	}
	ui.Row(c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Padding(0, 8, 0, left).Gap(8).DragWindow().Children(func() {
		if a.sidebarHidden {
			iconButton(c, p, iconSidebar, "Toggle Sidebar", p.Hover).OnClick(func() { a.sidebarHidden = false })
		}
		ui.Text(c, "Archived chats").FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		ui.Spacer(c)
		iconButton(c, p, iconX, "Close", p.Hover).OnClick(func() { a.page = "" })
	})
	var archived []catalog.Session
	q := strings.ToLower(strings.TrimSpace(a.archiveSearch))
	for _, s := range a.catalog.Sessions {
		if s.Archived && s.ParentSessionID == "" && (q == "" || strings.Contains(strings.ToLower(s.Title), q)) {
			archived = append(archived, s)
		}
	}
	sort.SliceStable(archived, func(i, j int) bool { return archived[i].CreatedAt > archived[j].CreatedAt })
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		column(c, "archive", func() {
			ui.Column(c).Gap(16).Padding(24, 0, 32, 0).Children(func() {
				ui.Row(c).AlignItems(ui.End).Gap(12).Children(func() {
					ui.Column(c).Grow(1).Gap(4).Children(func() {
						ui.Text(c, "Archived chats").FontSize(24).FontWeight(600).LetterSpacing(-0.4).TextColor(p.Strong)
						ui.Text(c, "Archived chats leave the sidebar. Restore one to bring it back.").FontSize(sizeSub).TextColor(p.Secondary)
					})
					ui.TextInput(c, &a.archiveSearch).Placeholder("Search").Width(200).Label("Search archived chats")
				})
				if len(archived) == 0 {
					msg := "No archived chats."
					if q != "" {
						msg = "No archived chats match."
					}
					ui.Text(c, msg).FontSize(sizeSub).TextColor(p.Secondary).Padding(24, 0)
					return
				}
				ui.Column(c).FillWidth().Radius(10).Border(1, p.Border).Clip().Dividers(1, p.Border).Gap(1).Children(func() {
					for _, s := range archived {
						a.archivedRow(c.Key(s.ID), p, s)
					}
				})
			})
		})
	})
}

func (a *App) archivedRow(c *ui.Context, p *palette, s catalog.Session) {
	row := ui.Row(c).Height(52).Padding(0, 12, 0, 16).Gap(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(s.Title)
	if row.Hovered() {
		row.Background(p.Hover)
	}
	if row.Clicked() {
		a.open(s.ID)
	}
	row.ContextMenu(func(m *ui.Menu) { a.sessionMenu(m, s) })
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, s.Title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
			var meta []string
			if name := a.projectName(s.ProjectID); name != "" {
				meta = append(meta, name)
			}
			meta = append(meta, a.machineLabel(s.MachineID))
			if t, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil {
				meta = append(meta, fmt.Sprintf("created %s", t.Local().Format("Jan 2, 2006")))
			}
			ui.Text(c, strings.Join(meta, " · ")).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine()
		})
		id := s.ID
		ghostButton(c, p, "Restore").Border(1, p.Border).OnClick(func() {
			a.catalogAction("restore the chat", func(ctx context.Context, sync *catalog.Sync) error {
				return sync.Archive(ctx, id, false)
			})
		})
	})
}
