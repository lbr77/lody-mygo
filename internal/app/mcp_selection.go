package app

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// The MCP servers a turn loads, as Lody's composer chooses them: a new
// chat starts from its project's defaults, or the workspace's; a chat
// keeps what it recorded; either may be changed from the composer's "+"
// menu, whose second level lists the servers to tick.

// defaultMCP is what a new chat in project loads.
func (a *App) defaultMCP(projectID string) []string {
	servers := a.catalog.MCPServers
	var ids []string
	pr := a.catalog.Project(projectID)
	if pr != nil {
		if _, local, ok := strings.Cut(pr.ID, ":local:"); ok {
			if own, custom := a.catalog.ProjectMCP[catalog.ProjectMCPKey(pr.MachineID, local)]; custom {
				ids = own
			}
		}
	}
	if ids == nil {
		for _, s := range servers {
			if s.EnabledByDefault {
				ids = append(ids, s.ID)
			}
		}
	}
	return availableMCP(servers, ids)
}

// availableMCP keeps the ids of servers the workspace has, once each.
func availableMCP(servers []catalog.MCPServer, ids []string) []string {
	have := map[string]bool{}
	for _, s := range servers {
		have[s.ID] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if have[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// mcpSelection is the composer's MCP servers: the new chat's, or the open
// chat's (sessionID).
func (a *App) mcpSelection(sessionID string) []string {
	if sessionID == "" {
		if a.newChat.mcpSet && a.newChat.mcpProject == a.newChat.project {
			return availableMCP(a.catalog.MCPServers, a.newChat.mcp)
		}
		return a.defaultMCP(a.newChat.project)
	}
	if ids, ok := a.sessionMCP[sessionID]; ok {
		return availableMCP(a.catalog.MCPServers, ids)
	}
	// What the chat recorded with its last message.
	if a.view != nil {
		for i := len(a.view.Entries) - 1; i >= 0; i-- {
			if e := a.view.Entries[i]; e.Role == "user" && e.Config != nil {
				var ids []string
				if list, ok := e.Config["mcpServerIds"].([]any); ok {
					for _, v := range list {
						if id, ok := v.(string); ok {
							ids = append(ids, id)
						}
					}
				}
				return availableMCP(a.catalog.MCPServers, ids)
			}
		}
	}
	return []string{}
}

func (a *App) setMCPSelection(sessionID string, ids []string) {
	if sessionID == "" {
		a.newChat.mcp, a.newChat.mcpSet, a.newChat.mcpProject = ids, true, a.newChat.project
		return
	}
	if a.sessionMCP == nil {
		a.sessionMCP = map[string][]string{}
	}
	a.sessionMCP[sessionID] = ids
}

// plusMenu is the composer's "+": add attachments, and choose the MCP
// servers, on a second level that slides in from the right as the first
// slides back from the left, 8px over 150ms.
func (a *App) plusMenu(c *ui.Context, p *palette, sessionID string) {
	open := a.popover("plus")
	b := iconButton(c, p, iconPlus, locales.AttachMenu(), p.Text.Alpha(0.06)).OnClick(func() {
		*open = !*open
		a.plusLevel = ""
	})
	if *open {
		b.Background(p.Hover)
	}
	servers := a.catalog.MCPServers
	if len(servers) == 0 {
		// Nothing to choose: the button picks files at once.
		b.OnClick(a.pickFiles)
		return
	}
	selected := a.mcpSelection(sessionID)
	popup(c, b, open, func() {
		level := a.plusLevel
		panel := ui.Column(c.Key("plus/" + level)).Padding(4).Gap(1).MinWidth(140)
		if level == "mcp" {
			panel.Width(320)
		}
		dir := float32(-8)
		if level == "mcp" {
			dir = 8
		}
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: 150 * time.Millisecond, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(dir*(1-k), 0).Opacity(k)
		}
		panel.Children(func() {
			if level == "mcp" {
				back := menuRow(c.Key("back"), p, locales.McpServersTitle()).OnClick(func() { a.plusLevel = "" })
				back.Children(func() {
					ui.Icon(c, iconChevronLeft).Size(14, 14).TextColor(p.Secondary)
					ui.Text(c, locales.McpServersTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
				})
				ui.Box(c).FillWidth().Height(1).Margin(3, 0).Background(p.Border)
				on := map[string]bool{}
				for _, id := range selected {
					on[id] = true
				}
				for _, s := range servers {
					srv := s
					row := menuRow(c.Key(s.ID), p, s.Name).Height(44)
					row.OnClick(func() {
						ids := slices.DeleteFunc(slices.Clone(selected), func(id string) bool { return id == srv.ID })
						if !on[srv.ID] {
							ids = append(ids, srv.ID)
						}
						a.setMCPSelection(sessionID, ids)
					})
					row.Children(func() {
						ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
							if on[srv.ID] {
								ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.Text)
							}
						})
						ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
							ui.Text(c, srv.Name).FontSize(sizeSub).TextColor(p.Text).SingleLine()
							detail := firstNonEmpty(srv.Description, srv.Summary())
							t := ui.Text(c, detail).FontSize(sizeCaption).TextColor(p.Secondary).SingleLine()
							if srv.Description == "" {
								t.Font(fontMono)
							}
						})
					})
				}
				if sessionID != "" {
					ui.Text(c, locales.McpNextStartHint()).FontSize(11).LineHeight(1.4).TextColor(p.Secondary).Padding(8, 10, 6, 10)
				}
				return
			}
			attach := menuRow(c.Key("attach"), p, locales.AttachMenu()).OnClick(func() {
				*open = false
				a.pickFiles()
			})
			attach.Children(func() {
				ui.Icon(c, iconPaperclip).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, locales.AttachMenu()).FontSize(sizeSub).TextColor(p.Text)
			})
			ui.Box(c).FillWidth().Height(1).Margin(3, 0).Background(p.Border)
			label := locales.McpLoadNone()
			if n := len(selected); n > 0 {
				label = locales.McpLoadCount(n)
			}
			mcp := menuRow(c.Key("mcp"), p, label).OnClick(func() { a.plusLevel = "mcp" })
			mcp.Children(func() {
				ui.Icon(c, iconPlug).Size(14, 14).TextColor(p.Secondary)
				ui.Text(c, label).FontSize(sizeSub).TextColor(p.Text).Grow(1)
				ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(p.Secondary)
			})
		})
	})
}
