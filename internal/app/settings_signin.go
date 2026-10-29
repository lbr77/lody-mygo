package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// A provider's sign-in, as Lody's account panel runs it: the machine runs
// the agent's login (machine/acp-authenticate) and reports its steps — a
// page to open, a code to show, a method to choose, a form to fill — and
// what the user gives back goes to it sealed to a key it made for this
// sign-in.

type authMethod struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type authField struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // text, secret or select
	Label        string `json:"label"`
	Description  string `json:"description"`
	Required     bool   `json:"required"`
	DefaultValue string `json:"defaultValue"`
	Options      []struct {
		Value string `json:"value"`
		Label string `json:"label"`
	} `json:"options"`
}

type authForm struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Fields      []authField `json:"fields"`
}

// authProgress is a step the machine reports.
type authProgress struct {
	RequestID                    string           `json:"requestId"`
	Status                       string           `json:"status"`
	AuthMethods                  []authMethod     `json:"authMethods"`
	InteractionID                string           `json:"interactionId"`
	Message                      string           `json:"message"`
	Form                         *authForm        `json:"form"`
	AuthorizationURL             string           `json:"authorizationUrl"`
	UserCode                     string           `json:"userCode"`
	AcceptsAuthorizationCode     bool             `json:"acceptsAuthorizationCode"`
	AuthorizationCodePublicKey   *cloud.SecretKey `json:"authorizationCodePublicKey"`
	AuthenticationInputPublicKey *cloud.SecretKey `json:"authenticationInputPublicKey"`
	RequiresAuthorizationConsent bool             `json:"requiresAuthorizationConsent"`
	ExpiresInSeconds             int              `json:"expiresInSeconds"`
	Error                        string           `json:"error"`
	RuntimeName                  string           `json:"runtimeName"`
	RuntimePhase                 string           `json:"runtimePhase"`
	RuntimePercent               *float64         `json:"runtimePercent"`
}

// authResponse is how the sign-in, or an answer to it, ended.
type authResponse struct {
	Success               bool   `json:"success"`
	Disposition           string `json:"disposition"`
	CapabilitiesRefreshed *bool  `json:"capabilitiesRefreshed"`
	AuthRequired          bool   `json:"authRequired"`
	AuthMethods           []any  `json:"authMethods"`
	Error                 string `json:"error"`
}

// authFlow is a sign-in in the dialog.
type authFlow struct {
	phase     string // running, authenticated, cancelled or error; "" before one
	machine   string
	requestID string
	key       *cloud.SecretKey
	err       string
	runtime   string // a runtime download's step

	// An authorization page, its code, and the code it may give back.
	url         string
	userCode    string
	acceptsCode bool
	expires     int
	consent     string // the interaction the page waits on, until accepted
	opened      string
	copied      bool
	code        string
	codeSent    bool

	// A question: a method to choose, or a form.
	interaction string
	methods     []authMethod
	form        *authForm
	message     string
	values      map[string]string
	submitting  bool
}

// authAccountName is the account a builtin agent signs in with.
func authAccountName(agentType string) string {
	switch agentType {
	case "claude":
		return "Claude"
	case "codex":
		return "ChatGPT"
	case "kimi":
		return "Kimi"
	case "grok":
		return "xAI"
	}
	return ""
}

// openExternal opens a page in the user's browser.
var openExternal = func(u string) { mygo.Shell.OpenExternal(u) }

func safeAuthURL(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && len(u) <= 8192 && (parsed.Scheme == "https" || parsed.Scheme == "http")
}

// startSignIn saves the provider, then starts its sign-in on the machine.
func (a *App) startSignIn() {
	f := &a.provider
	if f.auth != nil && f.auth.phase == "running" {
		return
	}
	row, err := f.row()
	if err != nil {
		f.err = err.Error()
		return
	}
	flow := &authFlow{phase: "running", machine: f.machine, requestID: uuid.NewString()}
	f.auth, f.err = flow, ""
	machine, id := f.machine, f.id
	a.writeProvider(machine, id, row, func(err error) {
		if err != nil {
			flow.phase, flow.err = "error", err.Error()
			return
		}
		f.persisted = true
		a.runSignIn(flow, id)
	})
}

func (a *App) runSignIn(flow *authFlow, configID string) {
	conn := a.conn
	if conn == nil {
		flow.phase, flow.err = "error", locales.AuthFailed()
		return
	}
	params := map[string]any{"requestId": flow.requestID, "action": "start", "configId": configID}
	go func() {
		raw, err := conn.MachineRPCProgress(context.Background(), flow.machine, "machine/acp-authenticate", params, 300*time.Second, func(raw json.RawMessage) {
			var p authProgress
			if json.Unmarshal(raw, &p) != nil || (p.RequestID != "" && p.RequestID != flow.requestID) {
				return
			}
			a.update(func() { a.signInProgress(flow, p) })
		})
		var res authResponse
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() { a.signInDone(flow, res, err) })
	}()
}

func (a *App) signInProgress(flow *authFlow, p authProgress) {
	if flow.phase != "running" {
		return
	}
	if key := p.AuthenticationInputPublicKey; key != nil {
		flow.key = key
	} else if key := p.AuthorizationCodePublicKey; key != nil {
		flow.key = key
	}
	switch p.Status {
	case "starting":
		flow.runtime = ""
	case "runtime-download":
		flow.runtime = runtimeStep(p)
	case "authorization":
		if p.AuthorizationURL == "" {
			return
		}
		flow.url, flow.userCode, flow.acceptsCode, flow.expires = p.AuthorizationURL, p.UserCode, p.AcceptsAuthorizationCode, p.ExpiresInSeconds
		flow.methods, flow.form, flow.interaction, flow.submitting = nil, nil, "", false
		flow.consent = ""
		if p.RequiresAuthorizationConsent {
			flow.consent = p.InteractionID
		} else {
			a.openAuthPage(flow)
		}
	case "auth-methods":
		if p.InteractionID != "" && len(p.AuthMethods) > 0 {
			flow.interaction, flow.methods, flow.form, flow.url, flow.submitting = p.InteractionID, p.AuthMethods, nil, "", false
		}
	case "input-required":
		if p.InteractionID != "" && p.Form != nil {
			flow.interaction, flow.form, flow.methods, flow.url, flow.submitting = p.InteractionID, p.Form, nil, "", false
			flow.message = p.Message
			flow.values = map[string]string{}
			for _, field := range p.Form.Fields {
				if field.Type != "secret" && field.DefaultValue != "" {
					flow.values[field.ID] = field.DefaultValue
				}
			}
		}
	case "cancelled":
		flow.phase = "cancelled"
	case "error":
		flow.phase, flow.err = "error", p.Error
	}
}

func runtimeStep(p authProgress) string {
	name := p.RuntimeName
	for _, k := range providerKinds {
		if k.group == "builtin" && strings.Contains(p.RuntimeName, k.agentType) {
			name = k.name
		}
	}
	switch p.RuntimePhase {
	case "complete", "":
		return ""
	case "downloading":
		if p.RuntimePercent != nil {
			return locales.AuthRuntimeDownloadingPercent(name, int(*p.RuntimePercent))
		}
		return locales.AuthRuntimeDownloading(name)
	}
	return locales.AuthRuntimePreparing(name)
}

func (a *App) signInDone(flow *authFlow, res authResponse, err error) {
	if flow.phase != "running" {
		return
	}
	flow.interaction, flow.methods, flow.form, flow.values, flow.code = "", nil, nil, nil, ""
	switch {
	case err != nil:
		var rpcErr *cloud.RPCError
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			flow.err = locales.AuthTimeout()
		} else if errors.As(err, &rpcErr) {
			flow.err = firstNonEmpty(rpcErr.Message, locales.AuthFailed())
		} else {
			flow.err = err.Error()
		}
		flow.phase = "error"
		a.cancelProviderAuth(flow)
	case !res.Success:
		flow.phase, flow.err = "error", firstNonEmpty(res.Error, locales.AuthFailed())
	case res.Disposition == "authenticated":
		flow.phase = "authenticated"
		if res.CapabilitiesRefreshed != nil && !*res.CapabilitiesRefreshed {
			flow.err = firstNonEmpty(res.Error, locales.ProviderTestFailed())
			if res.AuthRequired {
				flow.phase, flow.err = "error", firstNonEmpty(res.Error, locales.AuthFailed())
			}
		}
		if flow.phase == "authenticated" && a.provider.auth == flow {
			a.provider.authRequired, a.provider.testErr = false, ""
		}
	case res.Disposition == "cancelled":
		flow.phase = "cancelled"
	case res.Disposition == "method-required" && len(res.AuthMethods) == 0:
		flow.phase, flow.err = "error", locales.AuthNoMethods()
	default:
		flow.phase, flow.err = "error", locales.AuthFailed()
	}
}

// cancelProviderAuth tells the machine to stop a sign-in, which frees its
// login for another try.
func (a *App) cancelProviderAuth(flow *authFlow) {
	conn := a.conn
	if conn == nil || flow == nil {
		return
	}
	params := map[string]any{"requestId": uuid.NewString(), "action": "cancel", "authenticationRequestId": flow.requestID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn.MachineRPC(ctx, flow.machine, "machine/acp-authenticate", params, 10*time.Second)
	}()
}

// stopSignIn is the panel's Cancel.
func (a *App) stopSignIn() {
	flow := a.provider.auth
	if flow == nil || flow.phase != "running" {
		return
	}
	a.cancelProviderAuth(flow)
}

func (a *App) openAuthPage(flow *authFlow) {
	if !safeAuthURL(flow.url) {
		flow.err = locales.AuthUnsafeURL()
		return
	}
	if flow.opened == flow.url {
		return
	}
	flow.opened = flow.url
	openExternal(flow.url)
}

// submitAuth answers the machine's question: the user's choice, a form,
// the authorization page's consent, or the code the page showed. It goes
// sealed to the sign-in's key.
func (a *App) submitAuth(flow *authFlow, action string, input map[string]any, code string, done func()) {
	conn := a.conn
	if conn == nil || flow.key == nil {
		flow.err = locales.AuthInputSubmitFailed()
		return
	}
	params := map[string]any{"requestId": uuid.NewString(), "action": action, "authenticationRequestId": flow.requestID}
	var env *cloud.SecretEnvelope
	var err error
	if action == "submit-code" {
		env, err = cloud.SealSecret(*flow.key, code, cloud.AuthCodeContext(conn.ID, flow.machine, flow.requestID))
		params["authorizationCodeEnvelope"] = env
	} else {
		b, _ := json.Marshal(input)
		interaction := flow.interaction
		if interaction == "" {
			interaction = flow.consent
		}
		params["interactionId"] = interaction
		env, err = cloud.SealSecret(*flow.key, string(b), cloud.AuthInputContext(conn.ID, flow.machine, flow.requestID, interaction))
		params["authenticationInputEnvelope"] = env
	}
	if err != nil {
		flow.err = err.Error()
		return
	}
	flow.submitting, flow.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		raw, err := conn.MachineRPC(ctx, flow.machine, "machine/acp-authenticate", params, 10*time.Second)
		var res authResponse
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		a.update(func() {
			flow.submitting = false
			if err == nil && (!res.Success || res.Disposition != "input-accepted") {
				err = fmt.Errorf("%s", firstNonEmpty(res.Error, submitFailed(action)))
			}
			if err != nil {
				flow.err = err.Error()
				return
			}
			if done != nil {
				done()
			}
		})
	}()
}

func submitFailed(action string) string {
	if action == "submit-code" {
		return locales.AuthCodeSubmitFailed()
	}
	return locales.AuthInputSubmitFailed()
}

// signInPanel is the account field's body.
func (a *App) signInPanel(c *ui.Context, p *palette, interactive bool) {
	f := &a.provider
	flow := f.auth
	if flow == nil {
		flow = &authFlow{}
	}
	provider := firstNonEmpty(authAccountName(f.kind.agentType), strings.TrimSpace(f.name), f.kind.label())
	apiKey := f.codex != nil && f.codex.mode == "api-key"
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			if flow.phase == "running" {
				label := locales.AuthWaiting(provider)
				if apiKey {
					label = locales.CodexVerifyingKey()
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Height(30).Padding(0, 10).Radius(7).Background(p.Text.Alpha(0.05)).Children(func() {
					ui.Spinner(c).Size(12, 12)
					ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary)
				})
				ghostButton(c, p, locales.Cancel()).OnClick(a.stopSignIn)
				return
			}
			label := locales.AuthSignIn(provider)
			switch {
			case apiKey && (f.editing != nil || flow.phase == "authenticated"):
				label = locales.CodexUpdateKey()
			case apiKey:
				label = locales.CodexEnterKey()
			case flow.phase == "error" || flow.phase == "cancelled":
				label = locales.AuthRetry(provider)
			case flow.phase == "authenticated" || !f.authRequired:
				label = locales.AuthSignInAgain()
			}
			button := outlineButton(c, p, iconLogIn, label)
			if interactive {
				button.OnClick(a.startSignIn)
			} else {
				button.Disabled(true).Opacity(0.5)
			}
			if flow.phase == "authenticated" {
				ui.Text(c, locales.AuthSucceeded(provider)).FontSize(sizeFoot).TextColor(p.Success)
			}
		})
		if !interactive {
			ui.Text(c, locales.AuthUpgrade()).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		if flow.runtime != "" && flow.phase == "running" {
			ui.Text(c, flow.runtime).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		if flow.phase == "running" {
			switch {
			case flow.url != "":
				a.authPageView(c, p, flow, provider)
			case len(flow.methods) > 0:
				a.authMethodsView(c, p, flow)
			case flow.form != nil:
				a.authFormView(c, p, flow)
			}
		}
		if flow.err != "" {
			ui.Text(c, flow.err).FontSize(sizeFoot).TextColor(p.Danger).Selectable()
		}
	})
}

func (a *App) authPageView(c *ui.Context, p *palette, flow *authFlow, provider string) {
	ui.Column(c).FillWidth().Gap(10).Padding(12).Radius(8).Background(p.Text.Alpha(0.035)).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(200).Gap(2).Children(func() {
				ui.Text(c, locales.AuthFinish(provider)).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
				ui.Text(c, locales.AuthBrowserOpened()).FontSize(sizeFoot).TextColor(p.Secondary)
			})
			label := locales.AuthOpenPage()
			if flow.consent != "" {
				label = locales.AuthAuthorizeOpen()
			}
			open := outlineButton(c, p, iconExternal, label)
			if flow.submitting {
				open.Disabled(true).Opacity(0.5)
			} else {
				open.OnClick(func() {
					if flow.consent == "" {
						flow.opened = ""
						a.openAuthPage(flow)
						return
					}
					a.submitAuth(flow, "submit-input", map[string]any{"action": "accept"}, "", func() {
						flow.consent = ""
						a.openAuthPage(flow)
					})
				})
			}
		})
		ui.Text(c, flow.url).Font(fontMono).FontSize(sizeCaption).TextColor(p.Secondary).Selectable()
		if flow.userCode != "" {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, locales.AuthOneTimeCode()).FontSize(sizeCaption).TextColor(p.Tertiary)
					ui.Text(c, flow.userCode).Font(fontMono).FontSize(16).LetterSpacing(2).TextColor(p.Text).Selectable()
				})
				label := locales.AuthCopyCode()
				if flow.copied {
					label = locales.AuthCodeCopied()
				}
				ghostButton(c, p, label).Border(1, p.Border).OnClick(func() {
					mygo.Clipboard.WriteText(flow.userCode)
					flow.copied = true
				})
			})
			hint := locales.AuthEnterCode()
			if flow.expires > 0 {
				hint = locales.AuthEnterCodeExpiry(max(1, (flow.expires+59)/60))
			}
			ui.Text(c, hint).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		if flow.acceptsCode {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Text(c, locales.AuthCode()).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					input(c, p, &flow.code, locales.AuthCodePlaceholder(), locales.AuthCode()).Grow(1).Font(fontMono)
					label := locales.AuthSubmitCode()
					if flow.codeSent {
						label = locales.AuthCodeSubmitted()
					}
					ready := strings.TrimSpace(flow.code) != "" && !flow.submitting && !flow.codeSent
					primaryButton(c, p, label, ready).OnClick(func() {
						a.submitAuth(flow, "submit-code", nil, strings.TrimSpace(flow.code), func() { flow.codeSent = true })
					})
				})
				ui.Text(c, locales.AuthCodeHelp()).FontSize(sizeCaption).TextColor(p.Tertiary)
			})
		}
	})
}

func (a *App) authMethodsView(c *ui.Context, p *palette, flow *authFlow) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, locales.AuthChooseMethod()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).Margin(0, 0, 4, 0)
		for _, m := range flow.methods {
			if m.ID == "" {
				continue
			}
			method := m
			row := ui.Column(c.Key(m.ID)).FillWidth().Gap(2).Padding(8, 10).Radius(8).Border(1, p.Border).Transition(colorFade)
			usable := m.Type != "env_var" && !flow.submitting
			if usable {
				row.Cursor(ui.CursorPointer).Label(firstNonEmpty(m.Name, m.ID)).OnClick(func() {
					a.submitAuth(flow, "submit-input", map[string]any{"action": "accept", "methodId": method.ID}, "", nil)
				})
				if row.Hovered() {
					row.Background(p.Hover)
				}
			} else {
				row.Opacity(0.45)
			}
			row.Children(func() {
				ui.Text(c, firstNonEmpty(method.Name, method.ID)).FontSize(sizeSub).TextColor(p.Text)
				desc := method.Description
				if method.Type == "env_var" {
					desc = firstNonEmpty(desc, locales.AuthEnvMethod())
				}
				if desc != "" {
					ui.Text(c, desc).FontSize(sizeFoot).TextColor(p.Secondary)
				}
			})
		}
	})
}

func (a *App) authFormView(c *ui.Context, p *palette, flow *authFlow) {
	form := flow.form
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, firstNonEmpty(form.Title, locales.AuthAdditionalInfo())).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
		if text := firstNonEmpty(form.Description, flow.message); text != "" {
			ui.Text(c, text).FontSize(sizeFoot).TextColor(p.Secondary)
		}
		ready := !flow.submitting
		for _, fd := range form.Fields {
			field := fd
			label := field.Label
			if !field.Required {
				label += " " + locales.AuthOptional()
			}
			if field.Required && strings.TrimSpace(flow.values[field.ID]) == "" {
				ready = false
			}
			ui.Column(c.Key(field.ID)).FillWidth().Gap(4).Children(func() {
				ui.Text(c, label).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
				value := flow.values[field.ID]
				switch field.Type {
				case "select":
					var options []option
					current := locales.AuthSelectOption()
					for _, o := range field.Options {
						options = append(options, option{o.Value, o.Label})
						if o.Value == value {
							current = o.Label
						}
					}
					a.toolbarPicker(c, p, "auth-"+field.ID, nil, current, options, value, func(v string) { flow.values[field.ID] = v })
				default:
					in := input(c, p, &value, "", field.Label)
					if field.Type == "secret" {
						in.Password()
					}
					flow.values[field.ID] = value
				}
				if field.Description != "" {
					ui.Text(c, field.Description).FontSize(sizeCaption).TextColor(p.Tertiary)
				}
			})
		}
		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			primaryButton(c, p, locales.AuthContinue(), ready).OnClick(func() {
				content := map[string]any{}
				for k, v := range flow.values {
					content[k] = v
				}
				a.submitAuth(flow, "submit-input", map[string]any{"action": "accept", "content": content}, "", func() {
					flow.form, flow.interaction, flow.values = nil, "", nil
				})
			})
		})
	})
}
