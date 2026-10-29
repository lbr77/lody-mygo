package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// The workspace's MCP servers, as Lody's page keeps them: rows of the
// workspace stream, ['mcpServer', id], each a server sessions may load,
// some by default.

type mcpForm struct {
	open        bool
	id          string
	createdAt   float64
	createdBy   string
	name        string
	description string
	http        bool
	command     string
	args        []string
	env         string // KEY=value, a line each
	url         string
	bearer      string
	headers     string // Name: value, a line each
	err         string
}

func (a *App) mcpPage(c *ui.Context, p *palette) {
	pageHeader(c, p, "MCP", locales.McpLead(), func() {
		outlineButton(c, p, iconPlus, locales.McpAdd()).OnClick(func() { a.editMCP(nil) })
	})
	a.settingsErrorLine(c, p)
	servers := a.catalog.MCPServers
	if len(servers) == 0 {
		emptyCard(c, p, locales.McpNone())
	} else {
		card(c, p, func() {
			for _, m := range servers {
				a.mcpRow(c.Key(m.ID), p, m)
			}
		})
	}
	a.mcpDialog(c, p)
}

func (a *App) mcpRow(c *ui.Context, p *palette, m catalog.MCPServer) {
	server := m
	ui.Row(c).FillWidth().Padding(12, 14).Gap(12).AlignItems(ui.Center).Children(func() {
		icon := iconSquareTerminal
		badge := "stdio"
		if m.Transport == "http" {
			icon, badge = iconGlobe, "Streamable HTTP"
		}
		ui.Box(c).Size(28, 28).Radius(7).Center().Background(p.Text.Alpha(0.05)).Shrink(0).Children(func() {
			ui.Icon(c, icon).Size(15, 15).TextColor(p.Secondary)
		})
		body := ui.Column(c).Grow(1).MinWidth(0).Gap(3).Cursor(ui.CursorPointer).Label(m.Name).OnClick(func() { a.editMCP(&server) })
		body.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
				ui.Text(c, badge).FontSize(sizeCaption).FontWeight(500).TextColor(p.Text).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.07))
			})
			ui.Text(c, m.Summary()).Font(fontMono).FontSize(sizeFoot).TextColor(p.Secondary).SingleLine()
			if m.Description != "" {
				ui.Text(c, m.Description).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine()
			}
		})
		ui.Row(c).Gap(4).AlignItems(ui.Center).Opacity(0.45).Tooltip(locales.McpToolsLocal()).Children(func() {
			ui.Icon(c, iconRefresh).Size(13, 13).TextColor(p.Secondary)
			ui.Text(c, locales.McpLoadTools()).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		ui.Text(c, locales.McpDefault()).FontSize(sizeFoot).TextColor(p.Secondary)
		key := "mcp/" + m.ID
		if on, flipped := a.toggle(c, key, m.EnabledByDefault, locales.McpDefault()+" "+m.Name, false); flipped {
			server.EnabledByDefault = on
			row := server.Row(nowMs(), a.account.User.ID)
			a.streamWrite("", func(f *flock.Flock) error { return f.Put([]any{"mcpServer", server.ID}, row) }, func(error) { delete(a.toggles, key) })
		}
		del := iconButton(c, p, iconTrash, locales.McpDelete(m.Name), p.Hover)
		del.OnClick(func() {
			a.confirm(locales.McpDeleteTitle(server.Name), locales.McpDeleteBody(), locales.ActionDelete(), func() {
				a.streamWrite("", func(f *flock.Flock) error { return f.Delete([]any{"mcpServer", server.ID}) }, nil)
			})
		})
	})
}

// editMCP opens the editor, on a new server or an existing one.
func (a *App) editMCP(m *catalog.MCPServer) {
	f := mcpForm{open: true, id: uuid.NewString(), args: []string{""}}
	if m != nil {
		f.id, f.createdAt, f.createdBy, f.name, f.description = m.ID, m.CreatedAt, m.CreatedBy, m.Name, m.Description
		f.http = m.Transport == "http"
		conn := m.Connection
		f.command, f.url, f.bearer = str(conn["command"]), str(conn["url"]), str(conn["bearerToken"])
		f.args = nil
		for _, arg := range anyList(conn["args"]) {
			f.args = append(f.args, str(arg))
		}
		if len(f.args) == 0 {
			f.args = []string{""}
		}
		f.env = pairs(conn["env"], "=")
		f.headers = pairs(conn["headers"], ": ")
	}
	a.mcp = f
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// pairs writes a map a line an entry, sorted.
func pairs(v any, sep string) string {
	m, _ := v.(map[string]any)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, k+sep+str(m[k]))
	}
	return strings.Join(lines, "\n")
}

// parsePairs reads lines of name, separator, value.
func parsePairs(text, sep string) (map[string]any, error) {
	out := map[string]any{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, strings.TrimSpace(sep))
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("%s", locales.McpBadLine(i+1))
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// connection is what the form says of how to reach the server.
func (f *mcpForm) connection() (map[string]any, error) {
	if f.http {
		u := strings.TrimSpace(f.url)
		if u == "" {
			return nil, nil
		}
		conn := map[string]any{"transport": "http", "url": u}
		if t := strings.TrimSpace(f.bearer); t != "" {
			conn["bearerToken"] = t
		}
		headers, err := parsePairs(f.headers, ":")
		if err != nil {
			return nil, err
		}
		if headers != nil {
			conn["headers"] = headers
		}
		return conn, nil
	}
	cmd := strings.TrimSpace(f.command)
	if cmd == "" {
		return nil, nil
	}
	conn := map[string]any{"transport": "stdio", "command": cmd}
	var args []any
	for _, arg := range f.args {
		if arg = strings.TrimSpace(arg); arg != "" {
			args = append(args, arg)
		}
	}
	if len(args) > 0 {
		conn["args"] = args
	}
	env, err := parsePairs(f.env, "=")
	if err != nil {
		return nil, err
	}
	if env != nil {
		conn["env"] = env
	}
	return conn, nil
}

func (a *App) saveMCP() {
	f := &a.mcp
	name := strings.TrimSpace(f.name)
	for _, m := range a.catalog.MCPServers {
		if m.ID != f.id && strings.EqualFold(m.Name, name) {
			f.err = locales.McpNameTaken(name)
			return
		}
	}
	conn, err := f.connection()
	if err != nil {
		f.err = err.Error()
		return
	}
	transport := "stdio"
	if f.http {
		transport = "http"
	}
	enabled := true
	for _, m := range a.catalog.MCPServers {
		if m.ID == f.id {
			enabled = m.EnabledByDefault
		}
	}
	server := catalog.MCPServer{ID: f.id, Name: name, Transport: transport, Description: strings.TrimSpace(f.description),
		Connection: conn, EnabledByDefault: enabled, CreatedAt: f.createdAt, CreatedBy: f.createdBy}
	row := server.Row(nowMs(), a.account.User.ID)
	f.open = false
	a.streamWrite("", func(fl *flock.Flock) error { return fl.Put([]any{"mcpServer", server.ID}, row) }, nil)
}

func (a *App) mcpDialog(c *ui.Context, p *palette) {
	f := &a.mcp
	if !f.open {
		return
	}
	title := locales.McpAddTitle()
	for _, m := range a.catalog.MCPServers {
		if m.ID == f.id {
			title = locales.McpEditTitle()
		}
	}
	sheet(c, p, &f.open, 620, func() {
		sheetHeader(c, p, title, locales.McpDialogLead(), func() { f.open = false })
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 20, 8, 20).Gap(16).Children(func() {
			ui.Text(c, locales.McpBasics()).FontSize(sizeFoot).TextColor(p.Secondary)
			field(c, p, locales.McpName(), "", func() { input(c, p, &f.name, locales.McpNamePlaceholder(), locales.McpName()).AutoFocus() })
			field(c, p, locales.McpTransport(), "", func() {
				picked := map[bool]int{false: 0, true: 1}[f.http]
				ui.Segmented(c, &picked, "stdio", "HTTP").Label(locales.McpTransport())
				f.http = picked == 1
			})
			field(c, p, locales.McpDescription(), locales.McpDescriptionHint(), func() { area(c, p, &f.description, "", locales.McpDescription(), 2) })
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, locales.McpConnection()).FontSize(sizeFoot).TextColor(p.Secondary)
				ui.Text(c, locales.McpConnectionHint("${VAR}")).FontSize(sizeCaption).TextColor(p.Tertiary)
			})
			if f.http {
				field(c, p, "URL", "", func() { input(c, p, &f.url, "https://example.com/mcp", "URL").Font(fontMono) })
				field(c, p, "Bearer token", "", func() { input(c, p, &f.bearer, "${TOKEN}", "Bearer token").Font(fontMono) })
				field(c, p, locales.McpHeaders(), locales.McpHeadersHint(), func() { area(c, p, &f.headers, "X-Api-Key: ${API_KEY}", locales.McpHeaders(), 2).Font(fontMono) })
			} else {
				field(c, p, locales.McpCommand(), "", func() { input(c, p, &f.command, "/absolute/path/to/mcp-server", locales.McpCommand()).Font(fontMono) })
				field(c, p, locales.McpArgs(), "", func() {
					for i := range f.args {
						index := i
						ui.Row(c.Key(i)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							input(c, p, &f.args[index], "--flag", locales.McpArgs()).Font(fontMono).Grow(1)
							iconButton(c, p, iconTrash, locales.McpRemoveArg(), p.Hover).OnClick(func() {
								f.args = append(f.args[:index], f.args[index+1:]...)
							})
						})
					}
					ghostButton(c, p, "+ "+locales.McpAddArg()).OnClick(func() { f.args = append(f.args, "") })
				})
				field(c, p, locales.McpEnv(), locales.McpEnvHint(), func() { area(c, p, &f.env, "API_KEY=${API_KEY}", locales.McpEnv(), 2).Font(fontMono) })
			}
			if f.err != "" {
				ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
			ghostButton(c, p, locales.Cancel()).Border(1, p.Border).OnClick(func() { f.open = false })
			primaryButton(c, p, locales.Save(), strings.TrimSpace(f.name) != "").OnClick(a.saveMCP)
		})
	})
}
