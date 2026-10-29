package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// System notices, as Lody's conversation shows them: a failed turn and an
// agent's warning as one banner in two tones, a fork's origin and a
// resumed history as a ruled line, a proposed schedule as its card. Others
// show nothing.

// mergeableNotices are the notices that are their own system turn but
// read as the end of the turn before them.
var mergeableNotices = map[string]bool{"agent_warning": true, "chat_failed": true}

// foldNotices folds a warning or failure that is its own turn into the
// assistant turn right before it, for reading only; one that opens the
// chat or follows a user turn stays where it is.
func foldNotices(entries []session.Entry) []session.Entry {
	var out []session.Entry
	for _, e := range entries {
		notices := e.Role != "user" && len(e.Items) > 0
		for _, it := range e.Items {
			notices = notices && it.Type == "system_notice" && mergeableNotices[it.Title]
		}
		if n := len(out); notices && n > 0 && out[n-1].Role != "user" {
			prev := out[n-1]
			prev.Items = append(append([]session.Item(nil), prev.Items...), e.Items...)
			out[n-1] = prev
			continue
		}
		out = append(out, e)
	}
	return out
}

func noticeText(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// noticeBanner is Lody's AgentNoticeBanner: a well tinted 4.5% with the
// tone, no ring, its glyph and label on one line, the raw detail under
// them at the well's leading edge, and one trailing control.
func noticeBanner(c *ui.Context, p *palette, tone, label, detail string, action, footer func()) {
	accent, icon := p.Danger, iconCircleX
	switch tone {
	case "warning":
		accent, icon = ui.Hex("#D97706"), iconTriangleAlert
		if p.Dark {
			accent = ui.Hex("#FBBF24")
		}
	case "muted":
		accent, icon = p.Secondary, iconCircleAlert
	}
	trailing := action
	if trailing == nil {
		// Copy rides the header when it would be the only control there.
		trailing, footer = footer, nil
	}
	body := detail != "" || footer != nil
	ui.Column(c).FillWidth().Radius(8).Background(accent.Alpha(0.045)).Clip().Role(ui.RoleStatus).Children(func() {
		head := ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(8, 12, 4, 12)
		if !body {
			head.Padding(8, 12, 12, 12)
		}
		head.Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(accent).Shrink(0)
			ui.Text(c, label).FontSize(sizeSub).LineHeight(1.4).FontWeight(500).TextColor(accent).Grow(1).MinWidth(0)
			if trailing != nil {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(trailing)
			}
		})
		if detail != "" {
			ui.Text(c, detail).FontSize(sizeSub).LineHeight(1.5).TextColor(p.Secondary).Selectable().FillWidth().Padding(0, 12, 12, 12)
		}
		if footer != nil {
			ui.Row(c).FillWidth().Gap(4).Padding(0, 12, 12, 12).Children(footer)
		}
	})
}

// chatFailedTitle is a failure's title, by its diagnostic or its reason.
func chatFailedTitle(reason string, diag *noticeDiagnostic) string {
	if diag != nil {
		return diag.title
	}
	titles := map[string]func() string{
		"session_archived": locales.NoticeSessionArchived, "agent_type_mismatch": locales.NoticeAgentTypeMismatch,
		"session_init_failed": locales.NoticeSessionInitFailed, "session_restore_failed": locales.NoticeSessionRestoreFailed,
		"session_not_found": locales.NoticeSessionNotFound, "acp_not_ready": locales.NoticeAcpNotReady,
		"agent_disconnected": locales.NoticeAgentDisconnected, "agent_no_output": locales.NoticeAgentNoOutput,
		"turn_pre_prompt_failed": locales.NoticeTurnPrePromptFailed, "message_delivery_failed": locales.NoticeMessageDeliveryFailed,
		"machine_access_denied": locales.NoticeMachineAccessDenied, "memory_pressure": locales.NoticeMemoryPressure,
		"acp_auth_required": locales.NoticeAcpAuthRequired, "acp_internal_error": locales.NoticeAcpInternalError,
		"acp_upstream_api_error": locales.NoticeAcpUpstreamApiError, "acp_provider_overloaded": locales.NoticeAcpProviderOverloaded,
		"acp_session_storage_incompatible": locales.NoticeAcpSessionStorageIncompatible, "acp_resource_not_found": locales.NoticeAcpResourceNotFound,
		"acp_request_cancelled": locales.NoticeAcpRequestCancelled, "acp_method_not_found": locales.NoticeAcpMethodNotFound,
		"acp_invalid_params": locales.NoticeAcpInvalidParams, "acp_invalid_request": locales.NoticeAcpInvalidRequest,
		"acp_parse_error": locales.NoticeAcpParseError, "acp_unknown_error": locales.NoticeAcpUnknownError,
	}
	if t, ok := titles[reason]; ok {
		return t()
	}
	return locales.NoticeUnknown()
}

// noticeDiagnostic is what a known failure says to do.
type noticeDiagnostic struct {
	title, action, installURL string
}

var nodeFailure = regexp.MustCompile(`\[(DSH_NODE_NOT_FOUND|DSH_NODE_UNSUPPORTED|DSH_NODE_EXEC_FAILED)\]`)

func chatFailedDiagnostic(code, message string) *noticeDiagnostic {
	if m := nodeFailure.FindStringSubmatch(message); m != nil {
		title := map[string]func() string{
			"DSH_NODE_NOT_FOUND": locales.NoticeDeepseekNodeNotFound, "DSH_NODE_UNSUPPORTED": locales.NoticeDeepseekNodeUnsupported,
			"DSH_NODE_EXEC_FAILED": locales.NoticeDeepseekNodeExecFailed,
		}[m[1]]()
		return &noticeDiagnostic{title: title, action: locales.NoticeDeepseekNodeAction(), installURL: "https://nodejs.org/en/download"}
	}
	if code == "git_executable_not_found" {
		return &noticeDiagnostic{title: locales.NoticeGitExecutableNotFound(), action: locales.NoticeGitExecutableNotFoundAction()}
	}
	return nil
}

// chatFailedDetail is the full raw message once, and the remedy when the
// message doesn't already say it.
func chatFailedDetail(title, action, message string) string {
	var parts []string
	action, raw := strings.TrimSpace(action), message
	if strings.TrimSpace(raw) == "" {
		raw = ""
	}
	if action != "" && action != strings.TrimSpace(title) && !strings.Contains(raw, action) {
		parts = append(parts, action)
	}
	if raw != "" && strings.TrimSpace(raw) != strings.TrimSpace(title) {
		parts = append(parts, raw)
	}
	return strings.Join(parts, "\n\n")
}

// chatFailedReport is the plain text Copy error copies: who and what
// first, then the raw message.
func chatFailedReport(title, action, reason, code, message, sessionID, agentType, machine string) string {
	var lines []string
	for _, f := range [][2]string{{"Error details", title}, {"Reason", reason}, {"Code", code}, {"Session", sessionID},
		{"Agent", agentType}, {"Machine", machine}, {"Suggested action", action}} {
		if v := strings.TrimSpace(f[1]); v != "" {
			lines = append(lines, f[0]+": "+v)
		}
	}
	header := strings.Join(lines, "\n")
	msg := strings.TrimSpace(message)
	switch {
	case msg == "":
		return header
	case header == "":
		return msg
	}
	return header + "\n\n" + msg
}

// systemNotice draws a notice by its name.
func (a *App) systemNotice(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	meta := it.Notice
	switch it.Title {
	case "chat_failed":
		a.chatFailedNotice(c, p, e, it)
	case "agent_warning":
		if msg := noticeText(meta, "message"); msg != "" {
			ui.Column(c).FillWidth().Padding(4, 0).Children(func() {
				noticeBanner(c, p, "warning", locales.NoticeAgentWarning(), msg, nil, nil)
			})
		}
	case "session_fork_origin":
		source, title := noticeText(meta, "sourceSessionId"), noticeText(meta, "sourceTitle")
		if source == "" {
			return
		}
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Padding(16, 0).Children(func() {
			dashedRule(c, p)
			ui.Row(c).Gap(4).MinWidth(0).AlignItems(ui.Center).Shrink(1).Children(func() {
				ui.Text(c, locales.NoticeForkOrigin()).FontSize(sizeFoot).TextColor(p.Secondary.Alpha(0.75)).Shrink(0)
				link := tip(c, p, ui.ButtonBase(c).Cursor(ui.CursorPointer).Label(title).MinWidth(0).OnClick(func() { a.open(source) }), title)
				link.Children(func() {
					ink := p.Secondary
					if link.Hovered() {
						ink = p.Text
					}
					t := ui.Text(c, title).FontSize(sizeFoot).FontWeight(500).TextColor(ink).SingleLine()
					if link.Hovered() {
						t.Underline()
					}
				})
			})
			dashedRule(c, p)
		})
	case "resume_from_external_chat_history":
		msg := locales.NoticeResumeHistory()
		if meta["truncated"] == true {
			msg += " " + locales.NoticeResumeTruncated()
		}
		tip(c, p, ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Padding(16, 0), locales.NoticeResumeTooltip()).Children(func() {
			ui.Box(c).Grow(1).Height(1).Background(p.Border)
			ui.Text(c, msg).FontSize(sizeFoot).TextColor(p.Secondary).Shrink(1)
			ui.Box(c).Grow(1).Height(1).Background(p.Border)
		})
	case "schedule_proposal":
		a.scheduleProposal(c, p, e, it)
	case "agent_role_proposal":
		a.roleProposalCard(c, p, e, it)
	}
}

// dashedRule is a growing dashed line, as the fork origin's.
func dashedRule(c *ui.Context, p *palette) {
	ui.Row(c).Grow(1).Height(1).Gap(3).Clip().Children(func() {
		for i := 0; i < 80; i++ {
			ui.Box(c.Key(i)).Size(4, 1).Background(p.Border).Shrink(0)
		}
	})
}

// chatFailedNotice is a failed turn: its title, the raw error and the
// remedy, Copy error, and a retry when the model was at capacity.
func (a *App) chatFailedNotice(c *ui.Context, p *palette, e session.Entry, it session.Item) {
	meta := it.Notice
	reason, code, message := noticeText(meta, "reason"), noticeText(meta, "code"), noticeText(meta, "message")
	diag := chatFailedDiagnostic(code, message)
	title := chatFailedTitle(reason, diag)
	action := ""
	if diag != nil {
		action = diag.action
	}
	sess := a.catalog.Session(a.selected)
	tone := "error"
	if reason == "acp_provider_overloaded" {
		tone = "muted"
	}
	var retry func()
	if reason == "acp_provider_overloaded" && sess != nil && latestCapacityFailure(a.view) == it.ID {
		retry = func() { a.capacityRetryButton(c, p, it.ID) }
	}
	footer := func() {
		if diag != nil && diag.installURL != "" {
			url := diag.installURL
			ghostButton(c, p, locales.NoticeInstallNode()).OnClick(func() { mygo.Shell.OpenExternal(url) })
		}
		copyBtn := ui.ButtonBase(c.Key("copy-error")).Height(26).Padding(0, 8).Radius(6).Gap(5).Cursor(ui.CursorPointer).Label(locales.NoticeCopyError()).Transition(colorFade)
		if copyBtn.Hovered() {
			copyBtn.Background(p.Text.Alpha(0.06))
		}
		copyBtn.OnClick(func() {
			agent, machine := "", ""
			if sess != nil {
				agent, machine = sess.AgentType, sess.MachineID
			}
			writeClipboard(chatFailedReport(title, action, reason, code, message, a.selected, agent, machine))
			a.toast("success", locales.NoticeCopied())
		})
		copyBtn.Children(func() {
			ui.Icon(c, iconCopy).Size(14, 14).TextColor(p.Secondary)
			ui.Text(c, locales.NoticeCopyError()).FontSize(sizeFoot).TextColor(p.Secondary)
		})
	}
	ui.Column(c).FillWidth().Padding(4, 0).Children(func() {
		noticeBanner(c, p, tone, title, chatFailedDetail(title, action, message), retry, footer)
	})
}

// latestCapacityFailure is the capacity failure a retry would continue:
// the last failure's notice, if no user turn came after it.
func latestCapacityFailure(v *session.View) string {
	if v == nil {
		return ""
	}
	for i := len(v.Entries) - 1; i >= 0; i-- {
		e := v.Entries[i]
		if e.Role == "user" {
			return ""
		}
		for _, it := range e.Items {
			if it.Type == "system_notice" && it.Title == "chat_failed" {
				if noticeText(it.Notice, "reason") == "acp_provider_overloaded" {
					return it.ID
				}
				return ""
			}
		}
	}
	return ""
}

// capacityPrompt is what a retry after a capacity failure sends.
const capacityPrompt = "Continue working from where you left off. The previous turn stopped because the selected model was at capacity."

// capRetry is a chat's capacity retries: the first click allows three
// automatic ones, 5s after a failure and twice as long each time after,
// which a click stops.
type capRetry struct {
	auto      bool
	attempts  int
	pending   bool
	handled   map[string]bool
	countFrom time.Time // the countdown for countFor
	countFor  string
}

const (
	capFirstDelay = 5 * time.Second
	capMaxAuto    = 3
	capTick       = 250 * time.Millisecond // how often the countdown is read
)

func (a *App) capState() *capRetry {
	if a.capRetries == nil {
		a.capRetries = map[string]*capRetry{}
	}
	st := a.capRetries[a.selected]
	if st == nil {
		st = &capRetry{handled: map[string]bool{}}
		a.capRetries[a.selected] = st
	}
	return st
}

func (a *App) capacityRetryButton(c *ui.Context, p *palette, notice string) {
	st := a.capState()
	if st.handled[notice] && !st.pending {
		return
	}
	exhausted := st.auto && st.attempts >= capMaxAuto
	counting := st.auto && !exhausted && !st.pending && !st.handled[notice]
	// The countdown, as Lody's: 5s·2ⁿ after n automatic retries, the time
	// left read every 250ms.
	delay := capFirstDelay << st.attempts
	var left time.Duration
	if counting {
		if st.countFor != notice {
			st.countFor, st.countFrom = notice, c.Now()
		}
		since := c.Now().Sub(st.countFrom)
		if since >= delay {
			counting = false
			a.capacityRetry(notice, true)
		} else {
			tick := since / capTick * capTick
			left = delay - tick
			c.After(min(tick+capTick, delay) - since)
		}
	}
	label := locales.NoticeRetryAndEnableAuto()
	switch {
	case st.pending:
		label = locales.NoticeRetrying()
	case counting:
		label = locales.NoticeRetryIn(fmt.Sprint(int((left + time.Second - 1) / time.Second)))
	case exhausted:
		label = locales.NoticeRetryAgain()
	case st.auto:
		label = locales.NoticeRetryNow()
	}
	b := ui.ButtonBase(c.Key("cap-retry")).Height(28).Padding(0, 10).Radius(14).Background(p.Secondary.Alpha(0.04)).Cursor(ui.CursorPointer).
		Disabled(st.pending).Clip().Transition(twFade)
	if st.pending {
		b.Opacity(0.5)
	}
	hovered := b.Hovered() && !st.pending
	if hovered {
		b.Background(p.Secondary.Alpha(0.07))
	}
	// Counting down, a click stops it, and the button says so.
	name := label
	if counting {
		name = locales.NoticeStopAutoRetry()
	}
	b.Label(name).OnClick(func() {
		if counting {
			st.auto, st.countFor = false, ""
			return
		}
		st.attempts, st.auto = 0, true
		a.capacityRetry(notice, false)
	})
	b.Children(func() {
		if counting {
			// What is left drains away, each reading eased in linearly over
			// 300ms.
			ratio := float32(left) / float32(delay)
			ui.Box(c.Key("drain")).Absolute().Left(0).Top(0).Bottom(0).WidthPercent(ratio * 100).Background(p.Secondary.Alpha(0.08)).
				Transition(ui.ElementTransition{Size: true, Duration: 300 * time.Millisecond, Ease: ui.Linear})
		}
		if !counting {
			ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Text.Alpha(0.8))
			return
		}
		// The countdown and Stop share one cell, the pill as wide as the
		// wider, the pointer choosing which shows.
		ui.Grid(c.Key("labels")).ColumnTracks(ui.FitContent()).Children(func() {
			count := ui.Text(c.Key("count"), label).FontSize(sizeFoot).FontFeatures("tnum").TextColor(p.Text.Alpha(0.8)).ColumnStart(1).RowStart(1)
			stop := ui.Text(c.Key("stop"), locales.NoticeStopAutoRetry()).FontSize(sizeFoot).TextColor(p.Text.Alpha(0.8)).ColumnStart(1).RowStart(1)
			if hovered {
				count.Invisible()
			} else {
				stop.Invisible()
			}
		})
	})
}

// capacityRetry continues the turn a capacity failure stopped.
func (a *App) capacityRetry(notice string, automatic bool) {
	st := a.capState()
	if st.pending || st.handled[notice] {
		return
	}
	sess := a.catalog.Session(a.selected)
	if sess == nil || a.replica == nil || a.sync == nil {
		st.auto = false
		return
	}
	turn := session.Turn{
		MCPServerIDs: a.mcpSelection(a.selected), Text: capacityPrompt, UserID: a.userID(),
		MachineID: sess.MachineID, AgentType: sess.AgentType, CLIType: sess.CLIType, Resume: sess.Resume,
		ModelID: a.models[a.selected], ModeID: a.modes[a.selected],
		ConfigOptionValues: a.chosenRunValues(a.selected, sess.AgentConfigID, a.currentModel(sess)), MarkDispatch: a.sync.MarkDispatch,
	}
	r := a.replica
	st.pending, st.countFor = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		state, err := r.Send(ctx, turn)
		a.update(func() {
			st.pending = false
			if err != nil && state == session.NotSent {
				// Not accepted: control goes back to the reader.
				st.auto = false
				return
			}
			st.handled[notice] = true
			if automatic {
				st.attempts++
			}
		})
	}()
}

// noticeMeta reads a notice's metadata into v.
func noticeMeta(m map[string]any, v any) bool {
	b, err := json.Marshal(m)
	return err == nil && json.Unmarshal(b, v) == nil
}
