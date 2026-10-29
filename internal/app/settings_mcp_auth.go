package app

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localterm"
	"github.com/lbr77/lody-mygo/locales"
)

// An MCP server's sign-in, as Lody keeps it: the workspace shares only how
// to reach a server, with ${VAR} for what each machine fills in. A token,
// a header or a variable's value is the user's, kept by the daemon on
// this computer (mcp/authentication), as is an OAuth sign-in the daemon
// runs in the browser; the server's connection is then marked local.

var (
	envRefOnly     = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)
	secretQueryKey = regexp.MustCompile(`(?i)^(?:(?:access|refresh|id)[_-]?token|api[_-]?(?:key|token)|auth(?:orization)?|bearer|token|secret|password|client[_-]?secret)$`)
)

// publicURL is u without the credentials it may carry: a user, a
// fragment, a token in its query.
func publicURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	q := parsed.Query()
	sensitive := parsed.User != nil || parsed.Fragment != ""
	for k := range q {
		if secretQueryKey.MatchString(k) {
			sensitive = true
			q.Del(k)
		}
	}
	if !sensitive {
		return u
	}
	parsed.User, parsed.Fragment, parsed.RawQuery = nil, "", q.Encode()
	return parsed.String()
}

// splitValues parts a map into its ${VAR} references and its literal
// values.
func splitValues(v any) (shared, local map[string]any) {
	m, _ := v.(map[string]any)
	for k, val := range m {
		s := str(val)
		if envRefOnly.MatchString(s) {
			if shared == nil {
				shared = map[string]any{}
			}
			shared[k] = s
		} else {
			if local == nil {
				local = map[string]any{}
			}
			local[k] = s
		}
	}
	return shared, local
}

// splitMCP parts a connection into what the workspace may share and the
// credentials this computer's daemon keeps, as Lody's
// splitMcpAuthentication does.
func splitMCP(conn map[string]any) (shared, local map[string]any) {
	local = map[string]any{}
	marked := conn["authentication"] == "local"
	if str(conn["transport"]) == "stdio" {
		env, secrets := splitValues(conn["env"])
		shared = map[string]any{"transport": "stdio", "command": conn["command"]}
		for _, k := range []string{"args", "envPassthrough"} {
			if conn[k] != nil {
				shared[k] = conn[k]
			}
		}
		if env != nil {
			shared["env"] = env
		}
		if secrets != nil {
			local["env"] = secrets
		}
		if marked || secrets != nil {
			shared["authentication"] = "local"
		}
		return shared, local
	}
	raw := str(conn["url"])
	headers, secrets := splitValues(conn["headers"])
	shared = map[string]any{"transport": "http", "url": publicURL(raw)}
	bearer, hasBearer := conn["bearerToken"].(string)
	if hasBearer && envRefOnly.MatchString(bearer) {
		shared["bearerToken"] = bearer
	} else if bearer != "" {
		local["bearerToken"] = bearer
	}
	if headers != nil {
		shared["headers"] = headers
	}
	if secrets != nil {
		local["headers"] = secrets
	}
	if marked || len(local) > 0 || publicURL(raw) != raw {
		shared["authentication"] = "local"
	}
	return shared, local
}

// sameDestination is whether two connections reach the same server, so a
// sign-in made for one holds for the other.
func sameDestination(a, b map[string]any) bool {
	if a == nil || b == nil || a["transport"] != b["transport"] {
		return false
	}
	if a["transport"] == "http" {
		return a["url"] == b["url"]
	}
	ja, _ := json.Marshal(a["args"])
	jb, _ := json.Marshal(b["args"])
	return a["command"] == b["command"] && string(ja) == string(jb)
}

// mcpAuthResult is the daemon's account of a server's sign-in.
type mcpAuthResult struct {
	Status           string `json:"status"` // unknown, not_required, required, waiting, authorized or error
	HasCredentials   bool   `json:"hasCredentials"`
	Mode             string `json:"mode"`
	AttemptID        string `json:"attemptId"`
	AuthorizationURL string `json:"authorizationUrl"`
	Reason           string `json:"reason"`
}

var mcpAuthFailed = mcpAuthResult{Status: "error", Reason: "connection_failed"}

// mcpAuth is the dialog's sign-in panel.
type mcpAuth struct {
	result       *mcpAuthResult
	busy         bool
	polling      bool
	clientOpen   bool
	clientID     string
	clientSecret string
	attempt      string // the authorization this panel started, until it ends
	gen          int    // bumped by each action, so a late answer is dropped
}

// mcpAuthReady is whether this computer's daemon keeps sign-ins for the
// workspace.
func (a *App) mcpAuthReady() bool {
	machine := a.toolsMachine()
	return machine != "" && a.machineCap(machine, "mcpLocalAuthentication") >= 1 && a.account != nil
}

// mcpAuthCall asks the daemon about a server's sign-in. It blocks.
func (a *App) mcpAuthCall(server map[string]any, action string, extra map[string]any) (mcpAuthResult, error) {
	params := map[string]any{"server": server, "requesterUserId": a.account.User.ID, "action": action}
	for k, v := range extra {
		params[k] = v
	}
	raw, err := localterm.MachineRPC(context.Background(), a.workspaceID, "mcp/authentication", params, 35*time.Second)
	var res mcpAuthResult
	if err == nil {
		err = json.Unmarshal(raw, &res)
	}
	return res, err
}

// publishAuth records a server's sign-in, for its row too.
func (a *App) publishAuth(st *mcpAuth, id string, res mcpAuthResult) {
	st.result = &res
	if a.mcpAuthStatus == nil {
		a.mcpAuthStatus = map[string]mcpAuthResult{}
	}
	a.mcpAuthStatus[id] = res
	if res.Status == "waiting" {
		a.pollAuth(st, id)
	}
}

// savedMCP is the server the dialog edits, as the workspace has it.
func (a *App) savedMCP(id string) *catalog.MCPServer {
	for i := range a.catalog.MCPServers {
		if a.catalog.MCPServers[i].ID == id {
			return &a.catalog.MCPServers[i]
		}
	}
	return nil
}

// refreshAuth reads a server's sign-in when its dialog opens.
func (a *App) refreshAuth(st *mcpAuth, m catalog.MCPServer) {
	if !a.mcpAuthReady() {
		return
	}
	gen, server := st.gen, m.Meta()
	go func() {
		res, err := a.mcpAuthCall(server, "status", nil)
		a.update(func() {
			if st.gen != gen {
				return
			}
			if err != nil {
				res = mcpAuthFailed
			}
			a.publishAuth(st, m.ID, res)
		})
	}()
}

// onUI runs fn on the UI thread and returns its answer, or false when
// the window doesn't run it.
func (a *App) onUI(fn func() bool) bool {
	ch := make(chan bool, 1)
	a.update(func() { ch <- fn() })
	select {
	case ok := <-ch:
		return ok
	case <-time.After(10 * time.Second):
		return false
	}
}

// pollAuth asks every second while a browser sign-in is pending; once it
// lands, the server's tools load.
func (a *App) pollAuth(st *mcpAuth, id string) {
	if st.polling {
		return
	}
	st.polling = true
	gen := st.gen
	go func() {
		defer a.update(func() { st.polling = false })
		for {
			time.Sleep(time.Second)
			var server map[string]any
			if !a.onUI(func() bool {
				m := a.savedMCP(id)
				if st.gen != gen || m == nil || !a.mcp.open || a.mcp.auth != st {
					return false
				}
				server = m.Meta()
				return true
			}) {
				return
			}
			res, err := a.mcpAuthCall(server, "status", nil)
			if !a.onUI(func() bool {
				if st.gen != gen {
					return false
				}
				if err != nil {
					return true // try again
				}
				st.result = &res
				a.mcpAuthStatus[id] = res
				if res.Status == "authorized" {
					st.attempt = ""
					if m := a.savedMCP(id); m != nil {
						a.loadTools(*m)
					}
				}
				return res.Status == "waiting"
			}) {
				return
			}
		}
	}()
}

// authorizeMCP is 前往授权: the server's connection is marked local, the
// user's own OAuth client goes to the daemon if given, and the daemon's
// authorization page opens in the browser.
func (a *App) authorizeMCP(st *mcpAuth, m catalog.MCPServer) {
	if m.Connection == nil || !a.mcpAuthReady() {
		return
	}
	st.gen++
	gen := st.gen
	st.busy = true
	next := m
	conn := map[string]any{}
	for k, v := range m.Connection {
		conn[k] = v
	}
	conn["authentication"] = "local"
	next.Connection = conn
	row := next.Row(nowMs(), "")
	next.UpdatedAt = row["updatedAt"].(float64)
	clientID, clientSecret := strings.TrimSpace(st.clientID), st.clientSecret
	st.clientSecret = ""
	fail := func() {
		if st.gen == gen {
			a.publishAuth(st, m.ID, mcpAuthFailed)
			st.busy = false
		}
	}
	a.streamWrite("", func(f *flock.Flock) error { return f.Put([]any{"mcpServer", m.ID}, row) }, func(err error) {
		if err != nil {
			fail()
			return
		}
		server := next.Meta()
		go func() {
			if clientID != "" || clientSecret != "" {
				client := map[string]any{"mode": "oauth"}
				if clientID != "" {
					client["clientId"] = clientID
				}
				if clientSecret != "" {
					client["clientSecret"] = clientSecret
				}
				res, err := a.mcpAuthCall(server, "configure", map[string]any{"authentication": client})
				if err != nil || res.Status == "error" {
					a.update(func() {
						if err != nil {
							res = mcpAuthFailed
						}
						if st.gen == gen {
							a.publishAuth(st, m.ID, res)
							st.busy = false
						}
					})
					return
				}
			}
			res, err := a.mcpAuthCall(server, "authorize", nil)
			a.update(func() {
				if st.gen != gen {
					if res.AttemptID != "" {
						go a.mcpAuthCall(server, "cancel", map[string]any{"attemptId": res.AttemptID})
					}
					return
				}
				st.busy = false
				if err != nil {
					a.publishAuth(st, m.ID, mcpAuthFailed)
					return
				}
				st.attempt = res.AttemptID
				if res.AuthorizationURL != "" {
					if !mcpAuthURLOK(res.AuthorizationURL) {
						if res.AttemptID != "" {
							go a.mcpAuthCall(server, "cancel", map[string]any{"attemptId": res.AttemptID})
						}
						st.attempt = ""
						a.publishAuth(st, m.ID, mcpAuthFailed)
						return
					}
					openExternal(res.AuthorizationURL)
				}
				a.publishAuth(st, m.ID, res)
			})
		}()
	})
}

// mcpAuthURLOK allows HTTPS pages, and HTTP on this computer.
func mcpAuthURLOK(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" || (parsed.Scheme == "http" && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, parsed.Hostname()))
}

// mcpAuthAct is 检测连接 (check), 取消 (cancel) or 退出授权 (signout).
func (a *App) mcpAuthAct(st *mcpAuth, m catalog.MCPServer, action string) {
	if !a.mcpAuthReady() {
		return
	}
	st.gen++
	gen := st.gen
	st.busy = true
	server := m.Meta()
	extra := map[string]any{}
	if action == "cancel" && st.result != nil && st.result.AttemptID != "" {
		extra["attemptId"] = st.result.AttemptID
	}
	ws, user := a.workspaceID, a.account.User.ID
	go func() {
		var res mcpAuthResult
		var err error
		if action == "check" {
			localterm.MachineRPC(context.Background(), ws, "mcp/list-tools", map[string]any{"server": server, "requesterUserId": user}, 35*time.Second)
			res, err = a.mcpAuthCall(server, "status", nil)
		} else {
			res, err = a.mcpAuthCall(server, action, extra)
		}
		a.update(func() {
			if st.gen != gen {
				return
			}
			st.busy = false
			if err != nil {
				res = mcpAuthFailed
			}
			if action != "check" {
				st.attempt = ""
			}
			a.publishAuth(st, m.ID, res)
		})
	}()
}

// endMCPAuth gives up the authorization a closed dialog started.
func (a *App) endMCPAuth(st *mcpAuth, id string) {
	if st == nil {
		return
	}
	st.gen++
	if st.attempt == "" {
		return
	}
	attempt := st.attempt
	st.attempt = ""
	if m := a.savedMCP(id); m != nil && a.mcpAuthReady() {
		server := m.Meta()
		go a.mcpAuthCall(server, "cancel", map[string]any{"attemptId": attempt})
	}
}

// mcpAuthMessage is the panel's line, as Lody words each state.
func mcpAuthMessage(res *mcpAuthResult, available, disabled, http bool) string {
	reason, status := "", "unknown"
	if res != nil {
		reason, status = res.Reason, res.Status
	}
	switch {
	case !available:
		return locales.McpAuthLocalRequired()
	case disabled:
		return locales.McpAuthSaveFirst()
	case reason == "storage_unavailable":
		return locales.McpAuthStorageUnavailable()
	case reason == "client_required":
		return locales.McpAuthClientRequired()
	case reason == "expired":
		return locales.McpAuthExpired()
	case reason == "unsupported":
		return locales.McpAuthUnsupported()
	case reason == "cancelled":
		return locales.McpAuthCancelled()
	case !http && (res == nil || !res.HasCredentials):
		return locales.McpAuthStdio()
	}
	switch status {
	case "not_required":
		return locales.McpAuthNotRequired()
	case "required":
		return locales.McpToolsAuthRequired()
	case "waiting":
		return locales.McpAuthWaiting()
	case "authorized":
		return locales.McpAuthAuthorized()
	case "error":
		return locales.McpAuthError()
	}
	return locales.McpAuthUnknown()
}

// mcpAuthPanel is the dialog's 登录与授权 section, for a saved server.
func (a *App) mcpAuthPanel(c *ui.Context, p *palette, m catalog.MCPServer, dirty bool) {
	st := a.mcp.auth
	http := a.mcp.http
	available := a.mcpAuthReady()
	res := st.result
	ui.Column(c).FillWidth().Gap(10).Padding(12).Radius(8).Background(p.Text.Alpha(0.035)).Children(func() {
		ui.Text(c, locales.McpAuthTitle()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		ui.Text(c, mcpAuthMessage(res, available, dirty, http)).FontSize(sizeFoot).TextColor(p.Secondary)
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			if st.busy {
				ui.Spinner(c).Size(13, 13)
			}
			enabled := available && !st.busy
			button := func(b ui.Element, ok bool, click func()) {
				if ok {
					b.OnClick(click)
				} else {
					b.Disabled(true).Opacity(0.5)
				}
			}
			if res != nil && res.Status == "waiting" {
				button(ghostButton(c, p, locales.Cancel()).Border(1, p.Border), enabled, func() { a.mcpAuthAct(st, m, "cancel") })
				return
			}
			if http {
				canAuthorize := strings.TrimSpace(st.clientID) != ""
				if res != nil {
					canAuthorize = canAuthorize || res.Mode == "oauth" || slices.Contains([]string{"oauth_required", "expired", "client_required", "cancelled"}, res.Reason)
				}
				if canAuthorize {
					button(primaryButton(c, p, locales.McpAuthAuthorize(), enabled && !dirty), enabled && !dirty, func() { a.authorizeMCP(st, m) })
				} else {
					button(outlineButton(c, p, iconRefresh, locales.McpAuthCheck()), enabled && !dirty, func() { a.mcpAuthAct(st, m, "check") })
				}
			}
			if res != nil && res.HasCredentials {
				button(ghostButton(c, p, locales.McpAuthSignout()), enabled && !dirty, func() { a.mcpAuthAct(st, m, "signout") })
			}
		})
		if http {
			foldSection(c, p, st.clientOpen, func() { st.clientOpen = !st.clientOpen }, "oauth-client", locales.McpAuthClient(), func() {
				field(c, p, "Client ID", "", func() { input(c, p, &st.clientID, "", "Client ID").Font(fontMono) })
				field(c, p, "Client secret", "", func() { input(c, p, &st.clientSecret, "", "Client secret").Password() })
			})
		}
		if res != nil && res.AuthorizationURL != "" {
			if u, err := url.Parse(res.AuthorizationURL); err == nil {
				ui.Text(c, u.Scheme+"://"+u.Host).Font(fontMono).FontSize(sizeCaption).TextColor(p.Tertiary)
			}
		}
	})
}
