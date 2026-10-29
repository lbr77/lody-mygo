package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localterm"
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
	passthrough string // variables passed on from the machine, a line each
	enabled     bool   // selected for new sessions
	manualOpen  bool   // the manual credentials are unfolded
	busy        bool
	saved       bool // the server is in the workspace
	auth        *mcpAuth
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

// mcpTools is a server's tool list, as this computer's daemon last
// loaded it.
type mcpTools struct {
	conn    string // the connection it was loaded for
	loading bool
	tools   []mcpTool
	err     string
	oauth   bool // the server wants a sign-in
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func connKey(conn map[string]any) string {
	b, _ := json.Marshal(conn)
	return string(b)
}

// toolsMachine is this computer's machine, when its daemon serves the
// workspace, which lists servers' tools.
func (a *App) toolsMachine() string {
	if id := a.localMachine(); id != "" && slices.Contains(a.catalog.MachineIDs, id) {
		return id
	}
	return ""
}

// loadTools asks the daemon on this computer for a server's tools, as
// Lody's desktop app does: the daemon runs the command, or reaches the
// URL, with its own environment and the user's MCP sign-ins.
func (a *App) loadTools(m catalog.MCPServer) {
	machine := a.toolsMachine()
	if m.Connection == nil || machine == "" {
		return
	}
	if a.mcpTools == nil {
		a.mcpTools = map[string]*mcpTools{}
	}
	state := &mcpTools{conn: connKey(m.Connection), loading: true}
	a.mcpTools[m.ID] = state
	if a.machineCap(machine, "mcpToolDiscovery") < 1 {
		state.loading, state.err = false, locales.McpToolsUpgrade()
		return
	}
	params := map[string]any{"server": m.Row(m.CreatedAt, "")}
	if a.machineCap(machine, "mcpLocalAuthentication") >= 1 && a.account != nil {
		params["requesterUserId"] = a.account.User.ID
	}
	ws := a.workspaceID
	go func() {
		ctx := context.Background()
		raw, err := localterm.MachineRPC(ctx, ws, "mcp/list-tools", params, 35*time.Second)
		var res struct {
			Tools []mcpTool `json:"tools"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		// A server that wants an OAuth sign-in fails the same way; Lody
		// asks the daemon which it was.
		oauth := false
		if err != nil && params["requesterUserId"] != nil {
			status, serr := localterm.MachineRPC(ctx, ws, "mcp/authentication", map[string]any{
				"server": params["server"], "requesterUserId": params["requesterUserId"], "action": "status"}, 35*time.Second)
			var st struct {
				Reason string `json:"reason"`
			}
			oauth = serr == nil && json.Unmarshal(status, &st) == nil && st.Reason == "oauth_required"
		}
		a.update(func() {
			state.loading, state.tools = false, res.Tools
			var daemonErr *localterm.Error
			switch {
			case err == nil:
			case oauth:
				state.err, state.oauth = locales.McpToolsAuthRequired(), true
			case errors.As(err, &daemonErr) && daemonErr.Code == "daemon_unavailable":
				state.err = locales.McpToolsNoDaemon()
			default:
				state.err = locales.McpToolsFailed()
			}
		})
	}()
}

func (a *App) mcpRow(c *ui.Context, p *palette, m catalog.MCPServer) {
	server := m
	tools := a.mcpTools[m.ID]
	if tools != nil && tools.conn != connKey(m.Connection) {
		tools = nil // loaded for a connection since changed
	}
	ui.Column(c).FillWidth().Children(func() {
		a.mcpServerRow(c, p, server, tools)
		if tools != nil {
			a.mcpToolList(c, p, server, tools)
		}
	})
}

func (a *App) mcpToolList(c *ui.Context, p *palette, m catalog.MCPServer, t *mcpTools) {
	ui.Row(c).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Padding(0, 14, 12, 54).Children(func() {
		switch {
		case t.loading:
			spinner(c).Size(13, 13)
			ui.Text(c, locales.McpToolsLoading()).FontSize(sizeFoot).TextColor(p.Secondary)
		case t.oauth:
			ui.Text(c, t.err).FontSize(sizeFoot).TextColor(p.Secondary)
			server := m
			outlineButton(c, p, iconKey, locales.McpAuthAuthorize()).OnClick(func() { a.editMCP(&server) })
		case t.err != "":
			ui.Text(c, t.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
		case len(t.tools) == 0:
			ui.Text(c, locales.McpToolsEmpty()).FontSize(sizeFoot).TextColor(p.Secondary)
		default:
			for _, tool := range t.tools {
				badge := ui.Text(c.Key(tool.Name), tool.Name).Font(fontMono).FontSize(sizeCaption).TextColor(p.Text).
					Padding(2, 7).Radius(5).Background(p.Text.Alpha(0.06))
				if tool.Description != "" {
					badge.Tooltip(tool.Description)
				}
			}
		}
	})
}

func (a *App) mcpServerRow(c *ui.Context, p *palette, server catalog.MCPServer, tools *mcpTools) {
	m := server
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
		settings := ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(4, 8).Radius(6).Cursor(ui.CursorPointer).Label(locales.McpAuthSettings()).
			Transition(colorFade).OnClick(func() { a.editMCP(&server) })
		if settings.Hovered() {
			settings.Background(p.Hover)
		}
		settings.Children(func() {
			ui.Icon(c, iconSettings).Size(13, 13).TextColor(p.Secondary)
			ui.Text(c, locales.McpAuthSettings()).FontSize(sizeFoot).TextColor(p.Secondary)
		})
		load := ui.Row(c).Gap(4).AlignItems(ui.Center).Padding(4, 8).Radius(6).Tooltip(locales.McpToolsLocal()).Transition(colorFade)
		if m.Connection == nil || (tools != nil && tools.loading) || a.toolsMachine() == "" {
			load.Opacity(0.45)
		} else {
			load.Cursor(ui.CursorPointer).Label(locales.McpLoadTools()).OnClick(func() { a.loadTools(server) })
			if load.Hovered() {
				load.Background(p.Hover)
			}
		}
		load.Children(func() {
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
	a.endMCPAuth(a.mcp.auth, a.mcp.id)
	f := mcpForm{open: true, id: uuid.NewString(), args: []string{""}}
	if m != nil {
		f.id, f.createdAt, f.createdBy, f.name, f.description = m.ID, m.CreatedAt, m.CreatedBy, m.Name, m.Description
		f.enabled, f.saved, f.auth = m.EnabledByDefault, true, &mcpAuth{}
		var names []string
		for _, v := range anyList(m.Connection["envPassthrough"]) {
			names = append(names, str(v))
		}
		f.passthrough = strings.Join(names, "\n")
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
		f.manualOpen = f.bearer != "" || f.headers != ""
	}
	a.mcp = f
	if m != nil {
		a.refreshAuth(a.mcp.auth, *m)
	}
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
	var passthrough []any
	for _, name := range strings.FieldsFunc(f.passthrough, func(r rune) bool { return r == '\n' || r == ',' || r == ' ' }) {
		if !slices.Contains(passthrough, any(name)) {
			passthrough = append(passthrough, name)
		}
	}
	if len(passthrough) > 0 {
		conn["envPassthrough"] = passthrough
	}
	return conn, nil
}

// sharedConnection is the form's connection as the workspace keeps it,
// and the credentials only this computer's daemon keeps. A server signed
// in here stays marked so while it reaches the same place.
func (a *App) sharedConnection(f *mcpForm) (shared, local map[string]any, err error) {
	full, err := f.connection()
	if err != nil || full == nil {
		return nil, nil, err
	}
	shared, local = splitMCP(full)
	if m := a.savedMCP(f.id); m != nil && m.Connection["authentication"] == "local" && sameDestination(shared, m.Connection) {
		shared["authentication"] = "local"
	}
	return shared, local, nil
}

// mcpDirty is whether the form says other than the workspace has, so a
// sign-in waits for a save.
func (a *App) mcpDirty(f *mcpForm) bool {
	m := a.savedMCP(f.id)
	if m == nil {
		return true
	}
	shared, local, err := a.sharedConnection(f)
	return err != nil || len(local) > 0 || connKey(shared) != connKey(m.Connection) ||
		strings.TrimSpace(f.name) != m.Name || strings.TrimSpace(f.description) != m.Description
}

// saveMCP writes the server; credentials the form holds go to this
// computer's daemon instead, as Lody saves them.
func (a *App) saveMCP() {
	f := &a.mcp
	name := strings.TrimSpace(f.name)
	for _, m := range a.catalog.MCPServers {
		if m.ID != f.id && strings.EqualFold(m.Name, name) {
			f.err = locales.McpNameTaken(name)
			return
		}
	}
	shared, local, err := a.sharedConnection(f)
	if err != nil {
		f.err = err.Error()
		return
	}
	transport := "stdio"
	if f.http {
		transport = "http"
	}
	server := catalog.MCPServer{ID: f.id, Name: name, Transport: transport, Description: strings.TrimSpace(f.description),
		Connection: shared, EnabledByDefault: f.enabled, CreatedAt: f.createdAt, CreatedBy: f.createdBy}
	row := server.Row(nowMs(), a.account.User.ID)
	server.CreatedAt, server.UpdatedAt, server.CreatedBy = row["createdAt"].(float64), row["updatedAt"].(float64), str(row["createdBy"])
	f.busy, f.err = true, ""
	a.streamWrite("", func(fl *flock.Flock) error { return fl.Put([]any{"mcpServer", server.ID}, row) }, func(err error) {
		if err != nil {
			f.busy, f.err = false, err.Error()
			return
		}
		f.saved, f.createdAt, f.createdBy = true, server.CreatedAt, server.CreatedBy
		if f.auth == nil {
			f.auth = &mcpAuth{}
		}
		if len(local) == 0 {
			f.busy, f.open = false, false
			a.loadTools(server)
			return
		}
		if !a.mcpAuthReady() {
			f.busy, f.err = false, locales.McpAuthLocalRequired()
			return
		}
		local["mode"] = "manual"
		meta := server.Meta()
		go func() {
			res, err := a.mcpAuthCall(meta, "configure", map[string]any{"authentication": local})
			a.update(func() {
				f.busy = false
				if err != nil || res.Status == "error" {
					f.err = locales.McpAuthError()
					return
				}
				if a.mcpAuthStatus == nil {
					a.mcpAuthStatus = map[string]mcpAuthResult{}
				}
				a.mcpAuthStatus[server.ID] = res
				f.open = false
				a.loadTools(server)
			})
		}()
	})
}

func (a *App) mcpDialog(c *ui.Context, p *palette) {
	f := &a.mcp
	if !f.open {
		if f.auth != nil && f.auth.attempt != "" {
			a.endMCPAuth(f.auth, f.id)
		}
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
				field(c, p, "URL", "", func() { input(c, p, &f.url, "https://mcp.example.com/mcp", "URL").Font(fontMono) })
				foldSection(c, p, f.manualOpen, func() { f.manualOpen = !f.manualOpen }, "manual", locales.McpAuthManual(), func() {
					field(c, p, "Bearer Token", "", func() { input(c, p, &f.bearer, "${MCP_TOKEN}", "Bearer Token").Font(fontMono).Password() })
					field(c, p, locales.McpHeaders(), locales.McpHeadersHint(), func() {
						area(c, p, &f.headers, "X-Api-Key: ${API_KEY}", locales.McpHeaders(), 2).Font(fontMono)
					})
					ui.Text(c, locales.McpLocalCredentials("${VAR}")).FontSize(sizeCaption).LineHeight(1.45).TextColor(p.Tertiary)
				})
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
				field(c, p, locales.McpEnv(), locales.McpEnvHint()+" "+locales.McpLocalCredentials("${VAR}"), func() {
					area(c, p, &f.env, "API_KEY=${API_KEY}", locales.McpEnv(), 2).Font(fontMono)
				})
				field(c, p, locales.McpEnvPassthrough(), locales.McpEnvPassthroughHint(), func() {
					area(c, p, &f.passthrough, "API_TOKEN", locales.McpEnvPassthrough(), 2).Font(fontMono)
				})
			}
			if m := a.savedMCP(f.id); m != nil && f.saved && f.auth != nil {
				a.mcpAuthPanel(c, p, *m, a.mcpDirty(f) || f.busy)
			} else {
				ui.Text(c, locales.McpAuthSaveFirst()).FontSize(sizeFoot).TextColor(p.Secondary)
			}
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Padding(10, 12).Radius(8).Background(p.Text.Alpha(0.035)).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, locales.McpDefaultEnabled()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
					ui.Text(c, locales.McpDefaultEnabledHint()).FontSize(sizeCaption).TextColor(p.Secondary)
				})
				lodySwitch(c, &f.enabled, false).Label(locales.McpDefaultEnabled())
			})
			if f.err != "" {
				ui.Text(c, f.err).FontSize(sizeFoot).TextColor(p.Danger)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
			ghostButton(c, p, locales.Close()).Border(1, p.Border).OnClick(func() { f.open = false })
			label := locales.McpSaveConfiguration()
			if f.busy {
				spinner(c).Size(14, 14)
				label = locales.McpSaving()
			}
			primaryButton(c, p, label, strings.TrimSpace(f.name) != "" && !f.busy).OnClick(a.saveMCP)
		})
	})
}
