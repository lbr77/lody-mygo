package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/github"
	"github.com/lbr77/lody-mygo/locales"
)

// The info bar's pull request, as Lody's context stage shows one
// (session-info-chips.tsx, pr-merge-button.tsx and
// session-chat-interface.tsx): it leads with the pull request's mark in
// its state's hue with its CI's verdict, which opens the PR tab, then its
// checks' verdict, whose popover lists them. Once it exists, the bar's
// actions are Commit & Push while work is unpublished, then for a draft
// Ready for review, and otherwise Resolve Conflicts, Fix CI Errors and
// Merge, as the PR's state calls for them: Fix CI Errors and Resolve
// Conflicts are prompts the agent carries out; Ready for review and
// Merge act on GitHub directly, with no question first. The pull
// request's live details are the PR tab's, read once the bar shows it,
// its checks read again every 15s while it is open, as Lody's
// useGitHubPrDetails polls them; they speak over the compact state the
// machine reports.

var (
	iconCircleMinus = lucide(`<circle cx="12" cy="12" r="10"/><path d="M8 12h8"/>`)
	iconCircleSlash = lucide(`<circle cx="12" cy="12" r="10"/><line x1="9" x2="15" y1="15" y2="9"/>`)
)

// githubClient is a client calling GitHub for a repository with the token
// the workspace mints for this client, to read it or, with operate, to
// act on it. Tests swap it for one calling their own server.
var githubClient = func(ctx context.Context, a *App, repo string, operate bool) (*github.Client, error) {
	if a.convex == nil {
		return nil, errors.New("GitHub token unavailable")
	}
	token, err := a.githubToken(ctx, repo, operate)
	if err != nil {
		return nil, err
	}
	return &github.Client{Token: token}, nil
}

// prChecksPoll is how often the bar reads an open pull request's checks
// again, as Lody's CHECK_RUNS_POLL_INTERVAL_MS: CI can go back in flight
// after it settles, so the poll lasts while the pull request is open.
const prChecksPoll = 15 * time.Second

// readySettle and readyPoll are how long, and how often, Lody reads a
// pull request after marking it ready, while GitHub settles it out of
// draft (READY_FOR_REVIEW_SETTLE_TIMEOUT_MS, MERGEABILITY_POLL_INTERVAL_MS).
var readySettle, readyPoll = 15 * time.Second, time.Second

// errStillDraft is GitHub still calling the pull request a draft once the
// settling is over, Lody's ReadyForReviewStillDraftError.
var errStillDraft = errors.New("GitHub is still reporting this pull request as a draft.")

// ciRun is a check as the bar shows it, Lody's PrCiRun: success, failure,
// running, queued, skipped, or cancelled (aborted rather than failed, so
// it never turns CI red).
type ciRun struct {
	name, status, url string
	dur               time.Duration
}

// ciRuns are the checks as Lody's mapGitHubCheckRunToPrCiRun has them: a
// completed one by its conclusion, a failure unless it succeeded, was
// neutral or skipped, or was cancelled or went stale; each with how long
// it ran, when GitHub says.
func ciRuns(checks []github.CheckRun) []ciRun {
	var out []ciRun
	for _, r := range checks {
		run := ciRun{name: r.Name, url: r.HTMLURL}
		switch {
		case r.Status == "in_progress":
			run.status = "running"
		case r.Status != "completed":
			run.status = "queued"
		case r.Conclusion == "success":
			run.status = "success"
		case r.Conclusion == "neutral" || r.Conclusion == "skipped":
			run.status = "skipped"
		case r.Conclusion == "cancelled" || r.Conclusion == "stale":
			run.status = "cancelled"
		default:
			run.status = "failure"
		}
		if r.StartedAt != nil && r.CompletedAt != nil {
			run.dur = max(0, r.CompletedAt.Sub(*r.StartedAt))
		}
		out = append(out, run)
	}
	return out
}

// ciOverall is the checks' verdict, as Lody's summarizePrCiRuns: a
// failure decides it, then anything still in flight; else passing.
func ciOverall(runs []ciRun) string {
	running := false
	for _, r := range runs {
		switch r.status {
		case "failure":
			return "failing"
		case "running", "queued":
			running = true
		}
	}
	if running {
		return "running"
	}
	return "passing"
}

// ciCompact is the checks' verdict as the machine's compact CI state, for
// the pull request's mark: the live checks speak over the reported state
// once there are any.
func ciCompact(runs []ciRun, reported string) string {
	if len(runs) == 0 {
		return reported
	}
	switch ciOverall(runs) {
	case "failing":
		return "f"
	case "running":
		return "p"
	}
	return "s"
}

// ciVerdictFace is a verdict's words, glyph and ink, as Lody's
// usePrCiPresentation.
func ciVerdictFace(p *palette, overall string) (string, *ui.SVG, ui.Color) {
	switch overall {
	case "passing":
		return locales.InfoCiPassing(), iconCircleCheck, p.Success
	case "failing":
		return locales.InfoCiFailing(), iconCircleX, p.Danger
	}
	return locales.InfoCiRunning(), iconCircleDot, p.Warning
}

// ciRunFace is a check's glyph and ink, as Lody's PR_CI_RUN_ICON.
func ciRunFace(p *palette, status string) (*ui.SVG, ui.Color) {
	switch status {
	case "success":
		return iconCircleCheck, p.Success
	case "failure":
		return iconCircleX, p.Danger
	case "running":
		return iconCircleDot, p.Warning
	case "queued":
		return iconCircleDot, p.Secondary
	case "skipped":
		return iconCircleMinus, p.Secondary.Alpha(0.7)
	}
	return iconCircleSlash, p.Secondary.Alpha(0.7)
}

// failedForFix is a check Fix CI Errors asks about, as Lody's
// isFailedPrCheckRun: completed, and failed, timed out or wanting action.
func failedForFix(r github.CheckRun) bool {
	if r.Status != "completed" {
		return false
	}
	switch r.Conclusion {
	case "failure", "timed_out", "action_required":
		return true
	}
	return false
}

// The most failed checks a Fix CI Errors prompt lists, and the longest
// it is, as Lody's MAX_FAILED_RUNS and MAX_PROMPT_LENGTH (in UTF-16
// units, as JavaScript counts).
const (
	fixCIMaxRuns   = 12
	fixCIMaxLength = 6000
)

// fixCIPrompt is the message Fix CI Errors sends, as Lody's
// buildFixCiErrorsPrompt: the pull request as GitHub has it now and its
// failed checks, each with its app, conclusion and link, at most 12 and
// a line for the rest; "" with none failed.
func fixCIPrompt(repo string, pr *github.PullRequest, checks []github.CheckRun) string {
	var failed []github.CheckRun
	for _, r := range checks {
		if failedForFix(r) {
			failed = append(failed, r)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	var lines []string
	for _, r := range failed[:min(len(failed), fixCIMaxRuns)] {
		line := "- " + r.Name
		if r.App.Name != "" {
			line += " · " + r.App.Name
		}
		line += " · " + firstNonEmpty(r.Conclusion, r.Status)
		if r.HTMLURL != "" {
			line += " · " + r.HTMLURL
		}
		lines = append(lines, line)
	}
	if omitted := len(failed) - fixCIMaxRuns; omitted > 0 {
		lines = append(lines, locales.PromptFixCiMoreFailures(omitted))
	}
	prompt := locales.PromptFixCiErrors(repo, fmt.Sprint(pr.Number), pr.HTMLURL, pr.Base.Ref, pr.Head.Ref, pr.Head.SHA, strings.Join(lines, "\n"))
	return truncateUTF16(prompt, fixCIMaxLength)
}

// truncateUTF16 keeps the first n UTF-16 units of s, as JavaScript's
// slice does, without splitting a character.
func truncateUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		if units += utf16.RuneLen(r); units > n {
			return s[:i]
		}
	}
	return s
}

// resolveConflictsPrompt is the message Resolve Conflicts sends, as Lody's
// buildResolvePrConflictsPrompt: the pull request by its number, or its
// URL when the number is unknown.
func resolveConflictsPrompt(repo string, number int, url string) string {
	label := url
	if number > 0 {
		label = fmt.Sprintf("#%d", number)
	}
	return locales.PromptResolveConflicts(repo, label, url)
}

// sessionTurnAction is whether an action sends the agent a turn, which
// waits on the conversation having loaded, as Lody's
// SESSION_TURN_GITHUB_ACTION_IDS; Ready for review and Merge don't.
func sessionTurnAction(id string) bool {
	return id != "ready-for-review" && id != "merge"
}

// infoGitHub is what the bar's pull request and actions go by: the
// conversation's GitHub state, with the pull request's live details over
// the compact ones, as Lody's effectivePrStatus and its live CI, and
// those details, the PR tab's.
func (a *App) infoGitHub(s *catalog.Session) (githubState, *prPanel) {
	st := a.sessionGitHub(s)
	if st.pr == nil {
		return st, nil
	}
	live := a.prPanelFor(s)
	if live == nil {
		return st, nil
	}
	if live.pr != nil {
		st.status = live.pr.Status()
	}
	// Whenever the CI pill says CI failed, Fix CI Errors is there, though
	// the reported state hasn't caught up.
	if ciOverall(ciRuns(live.checks)) == "failing" {
		st.state.CI = "f"
	}
	return st, live
}

// mergeMethod is the merge method last chosen, one for the info bar and
// the PR tab, kept across launches as Lody's
// lody:pr-tab:last-merge-method; merge until one is.
func (a *App) mergeMethod() string {
	if a.prMergeMethod == "" {
		var saved string
		readJSON("pr-merge-method.json", &saved)
		a.prMergeMethod = "merge"
		if saved == "squash" || saved == "rebase" {
			a.prMergeMethod = saved
		}
	}
	return a.prMergeMethod
}

// setMergeMethod chooses the merge method, for both.
func (a *App) setMergeMethod(method string) {
	if a.mergeMethod() == method {
		return
	}
	a.prMergeMethod = method
	writeJSON("pr-merge-method.json", method)
}

// mergeMethodLabel is the merge a method makes, as Lody's
// PrMergeMethodLabel says it.
func mergeMethodLabel(method string) string {
	switch method {
	case "squash":
		return locales.PrTabMergeSquashAction()
	case "rebase":
		return locales.PrTabMergeRebaseAction()
	}
	return locales.PrTabMergeAction()
}

// runInfoAction carries out one of the bar's actions for the open
// conversation.
func (a *App) runInfoAction(sessionID, id string) {
	s := a.catalog.Session(sessionID)
	if s == nil || a.selected != sessionID {
		return
	}
	switch id {
	case "resolve-conflicts":
		a.resolveConflicts(s)
	case "fix-ci-errors":
		a.fixCIErrors(s)
	case "ready-for-review":
		if _, live := a.infoGitHub(s); live != nil {
			a.markPRReady(live, func(err error) { a.prToast(locales.PrTabReadyForReviewError(), err) })
		}
	case "merge":
		if _, live := a.infoGitHub(s); live != nil {
			a.mergePullRequest(live, a.mergeMethod(), func(err error) { a.prToast(locales.PrTabMergeError(), err) })
		}
	default:
		a.dispatchPrompt(a.infoPrompt(s, id))
	}
}

// offersInfoAction is whether the bar offers an action for s, and whether
// it can be used yet: the PR tab's Resolve conflicts goes with the bar's.
func (a *App) offersInfoAction(s *catalog.Session, id string) (offered, usable bool) {
	gs, live := a.infoGitHub(s)
	for _, act := range a.infoActions(s, gs, live) {
		if act.id == id {
			return true, !act.disabled
		}
	}
	return false, false
}

// prErrorText is why an operation on the pull request failed, in the
// app's language where Lody says it in its own.
func prErrorText(err error) string {
	if errors.Is(err, errStillDraft) {
		return locales.InfoReadyStillDraft()
	}
	return err.Error()
}

// prToast says an operation on the pull request failed, and why.
func (a *App) prToast(title string, err error) {
	a.toasts = append(a.toasts, ui.Toast{Type: "error", Title: title, Description: prErrorText(err)})
}

// resolveConflicts asks the agent to resolve the pull request's conflicts
// with its base.
func (a *App) resolveConflicts(s *catalog.Session) {
	st := a.sessionGitHub(s)
	if st.pr == nil || st.pr.URL == "" {
		return
	}
	repo, _, ok := github.ParsePR(st.pr.URL)
	if !ok {
		repo = st.repo
	}
	a.dispatchPrompt(resolveConflictsPrompt(repo, st.pr.PRNumber(), st.pr.URL))
}

// fixCIErrors reads the pull request's checks again and asks the agent to
// fix those that failed, as Lody's handleFixCiErrors: it says so when the
// checks can't be read, when none failed, or when the request couldn't be
// sent. The action waits, disabled, while it reads.
func (a *App) fixCIErrors(s *catalog.Session) {
	_, live := a.infoGitHub(s)
	if live == nil || live.fixing {
		return
	}
	live.fixing = true
	sessionID, repo := s.ID, live.repo
	a.refreshPRChecks(live, func(pr *github.PullRequest, checks []github.CheckRun, err error) {
		live.fixing = false
		if err != nil {
			a.prToast(locales.InfoFixCiFetchError(), err)
			return
		}
		prompt := fixCIPrompt(repo, pr, checks)
		if prompt == "" {
			a.toast("info", locales.InfoFixCiNoFailures())
			return
		}
		if a.selected != sessionID || !a.dispatchPrompt(prompt) {
			a.toast("error", locales.InfoFixCiSendError())
		}
	})
}

// refreshPRChecks reads the pull request and its head's checks again with
// the token that reads the repository, as Lody's refreshCheckRuns; done,
// if any, has what was read.
func (a *App) refreshPRChecks(st *prPanel, done func(*github.PullRequest, []github.CheckRun, error)) {
	st.polling = true
	repo, n := st.repo, st.number
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var pr *github.PullRequest
		var checks []github.CheckRun
		gh, err := githubClient(ctx, a, repo, false)
		if err == nil {
			if pr, err = gh.PullRequest(ctx, repo, n); err == nil {
				checks, err = gh.CheckRuns(ctx, repo, pr.Head.SHA)
			}
		}
		a.update(func() {
			st.polling, st.checksAt = false, time.Now()
			if err == nil {
				st.pr, st.checks, st.checksErr = pr, checks, ""
			}
			if done != nil {
				done(pr, checks, err)
			}
		})
	}()
}

// pollPRChecks reads an open pull request's checks again every 15s while
// the bar shows it.
func (a *App) pollPRChecks(c *ui.Context, st *prPanel) {
	if st == nil || st.pr == nil || a.convex == nil || a.offline || st.loading || st.polling {
		return
	}
	if status := st.pr.Status(); status != "open" && status != "draft" {
		return
	}
	last := st.loadedAt
	if st.checksAt.After(last) {
		last = st.checksAt
	}
	if wait := prChecksPoll - time.Since(last); wait > 0 {
		c.After(wait)
		return
	}
	a.refreshPRChecks(st, nil)
}

// mergePullRequest merges the pull request the way chosen, as Lody's
// mergePullRequest: it reads the head as GitHub has it now and merges
// that, then marks the pull request merged as GitHub answered, not
// reading it again, as GitHub's details lag a merge. failed has why it
// didn't.
func (a *App) mergePullRequest(st *prPanel, method string, failed func(error)) {
	if st.pr == nil || st.busy != "" || st.blocked {
		return
	}
	st.busy = "merge"
	repo, n := st.repo, st.number
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		gh, err := githubClient(ctx, a, repo, true)
		var current *github.PullRequest
		if err == nil {
			current, err = gh.PullRequest(ctx, repo, n)
		}
		if err == nil {
			err = gh.Merge(ctx, repo, n, method, current.Head.SHA)
		}
		a.update(func() {
			st.busy = ""
			if err != nil {
				failed(err)
				return
			}
			merged := *current
			if st.pr != nil {
				merged = *st.pr
			}
			merged.Merged, merged.State = true, "closed"
			st.pr = &merged
		})
	}()
}

// markPRReady takes the pull request out of draft, as Lody's
// markReadyForReview: then it reads the pull request each second until
// GitHub has settled it, for 15s at most, and fails if it is still a
// draft by then. failed has why it didn't.
func (a *App) markPRReady(st *prPanel, failed func(error)) {
	if st.pr == nil || st.pr.NodeID == "" || st.busy != "" || st.blocked {
		return
	}
	st.busy = "ready"
	repo, n, node, last := st.repo, st.number, st.pr.NodeID, st.pr
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		gh, err := githubClient(ctx, a, repo, true)
		if err == nil {
			err = gh.ReadyForReview(ctx, node)
		}
		if err == nil {
			last, err = settleReady(ctx, gh, repo, n, last)
		}
		a.update(func() {
			st.busy = ""
			if last != nil {
				st.pr = last
			}
			if err != nil {
				failed(err)
			}
		})
	}()
}

// settleReady reads the pull request until GitHub has it out of draft,
// last being what was read before.
func settleReady(ctx context.Context, gh *github.Client, repo string, n int, last *github.PullRequest) (*github.PullRequest, error) {
	start := time.Now()
	for {
		if pr, err := gh.PullRequest(ctx, repo, n); err == nil {
			last = pr
		}
		if last == nil || !last.ReadyForReviewPending() {
			return last, nil
		}
		if time.Since(start) >= readySettle {
			if last.Status() == "draft" {
				return last, errStillDraft
			}
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(readyPoll):
		}
	}
}

// prGlyph is a pull request's mark: its state's glyph in GitHub's hue,
// muted in the sidebar (compact), with its CI's verdict cut into its
// corner on a disc of the ground it sits on, as Lody's SessionPrIcon
// masks it: a tick, a cross, a dot while CI is expected, and a smaller
// dot while it runs.
func prGlyph(c *ui.Context, p *palette, key, status, ci, label string, size float32, compact bool, ground ui.Color) ui.Element {
	icon, hue := prTone(p, status)
	if compact {
		hue = hue.Mix(p.Secondary, 0.35)
	}
	return ui.Box(c.Key(key)).Size(size+2, size+2).Shrink(0).Label(label).Children(func() {
		ui.Icon(c, icon).Size(size, size).TextColor(hue)
		var verdict *ui.SVG
		var ink ui.Color
		switch ci {
		case "s":
			verdict, ink = iconCheck, p.Success
		case "f", "e":
			verdict, ink = iconX, p.Danger
		case "x":
			verdict, ink = iconDot, p.Warning
		case "p":
			ink = p.Warning
		default:
			return
		}
		ui.Row(c).Absolute().Right(-1).Bottom(-1).Size(9, 9).Radius(5).Background(ground).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if verdict == nil {
				ui.Box(c).Size(5, 5).Radius(3).Background(ink)
				return
			}
			ui.Icon(c, verdict).Size(8, 8).TextColor(ink)
		})
	})
}

// infoPRPill is the bar's pull request, as Lody's ContextChip leads with
// it: its mark and, once the bar is 420 wide, its number, in a 24-tall
// button that tints 10% under the pointer over Tailwind's 150ms. It opens
// the PR tab, or the pull request on GitHub when its number is unknown;
// its tooltip says Open pull request.
func (a *App) infoPRPill(c *ui.Context, p *palette, status, ci, url string, number int, wide bool) {
	label := locales.InfoOpenPR()
	b := ui.ButtonBase(c.Key("info-pr")).Height(24).Padding(0, 4).Radius(6).Gap(4).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Label(label).Transition(twFade).OnClick(func() {
		if number <= 0 {
			openExternal(url)
			return
		}
		a.inspectFor(a.selected).openPanel(tabPR)
		a.panelOpen = true
	})
	tip(c, p, b, label)
	ground := p.Composer
	if b.Hovered() {
		b.Background(p.Secondary.Alpha(0.1))
		ground = p.Composer.Mix(p.Secondary, 0.1)
	}
	b.Children(func() {
		prGlyph(c, p, "mark", status, ci, prStatusLabel(status), 14, false, ground)
		if wide && number > 0 {
			ui.Text(c, fmt.Sprintf("#%d", number)).FontSize(sizeFoot).FontWeight(500).FontFeatures("tnum").TextColor(p.Secondary).Shrink(0)
		}
	})
}

// infoCIPill is the checks' verdict beside the pull request, as Lody's
// PrCiPill: the verdict's glyph in its ink and, while CI runs, how many
// have settled of how many, on no fill of its own but the pointer's 10%.
// Its tooltip names the checks and their verdict. A click opens their
// list above it, aligned to its start, 8 apart: the verdict and the
// count, then each check with its state's glyph, its name and how long it
// ran, a click on one opening it on GitHub.
func (a *App) infoCIPill(c *ui.Context, p *palette, sessionID string, runs []ciRun) {
	if len(runs) == 0 {
		return
	}
	overall := ciOverall(runs)
	settled := 0
	for _, r := range runs {
		if r.status != "running" && r.status != "queued" {
			settled++
		}
	}
	verdict, glyph, ink := ciVerdictFace(p, overall)
	count := fmt.Sprintf("%d/%d", settled, len(runs))
	label := locales.InfoCiChecks() + " · " + verdict
	open := a.popover("ci/" + sessionID)
	b := ui.ButtonBase(c.Key("info-ci")).Height(24).Padding(0, 4).Radius(6).Gap(4).AlignItems(ui.Center).Shrink(0).
		Cursor(ui.CursorPointer).Label(label).Transition(twFade).OnClick(func() { *open = !*open })
	tip(c, p, b, label)
	if *open || b.Hovered() {
		b.Background(p.Secondary.Alpha(0.1))
	}
	b.Children(func() {
		ui.Icon(c, glyph).Size(14, 14).TextColor(ink).Shrink(0)
		if overall == "running" {
			ui.Text(c, count).FontSize(sizeFoot).FontFeatures("tnum").TextColor(p.Secondary).Shrink(0)
		}
	})
	ui.PopoverBase(c, b, open, func(panel ui.Element) {
		// 384 wide, at most 90% of the window; it comes from 4 further up
		// and settles as it fades in, as the bar's other popovers.
		width := float32(384)
		if w, _ := c.Size(); w > 0 {
			width = min(width, 0.9*w)
		}
		panel.Width(width).Radius(14).Background(raisedGround(p)).Border(1, p.Border.Alpha(0.6)).Clip().
			Shadow(0, 20, 25, -5, ui.RGBA(0, 0, 0, 0.1)).Label(locales.InfoCiChecks()).
			AttachTo(b, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 8, 0).
			Transition(ui.ElementTransition{Colors: true, Duration: popTime, Ease: easeStandard, Exit: &ui.Motion{Y: -4}})
		if k := panel.Keyframes("in", []ui.Keyframe{{Value: 0}, {At: popTime, Value: 1, Ease: easeStandard}}); k < 1 {
			panel.Translate(0, -4*(1-k)).Opacity(k)
		}
		panel.Children(func() {
			ui.Column(c.Key("ci-body")).FillWidth().Padding(12).Gap(6).Children(func() {
				ui.Row(c.Key("ci-head")).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, verdict).FontSize(sizeFoot).FontWeight(500).TextColor(ink).Shrink(0)
					ui.Text(c, "· "+count).FontSize(sizeFoot).FontWeight(500).FontFeatures("tnum").TextColor(p.Secondary).Shrink(0)
				})
				ui.Scroll(c.Key("ci-runs")).FillWidth().MaxHeight(224).Role(ui.RoleList).Children(func() {
					for _, r := range runs {
						a.ciRunRow(c, p, r)
					}
				})
			})
		})
	})
}

// ciRunRow is a check in the CI popover: a button that tints 10% under
// the pointer over Tailwind's 150ms when it links to GitHub.
func (a *App) ciRunRow(c *ui.Context, p *palette, r ciRun) {
	icon, ink := ciRunFace(p, r.status)
	children := func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(ink).Shrink(0)
		ui.Text(c, r.name).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
		if r.dur >= time.Second {
			ui.Text(c, formatDuration(r.dur)).FontSize(sizeFoot).FontWeight(500).FontFeatures("tnum").TextColor(p.Secondary).Shrink(0)
		}
	}
	if r.url == "" {
		ui.Row(c.Key(r.name)).FillWidth().Padding(4, 6).Gap(8).AlignItems(ui.Center).Role(ui.RoleListItem).Label(r.name).Children(children)
		return
	}
	url := r.url
	row := ui.ButtonBase(c.Key(r.name)).FillWidth().Padding(4, 6).Gap(8).Radius(6).Justify(ui.Start).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(r.name).Transition(twFade).OnClick(func() { openExternal(url) })
	if row.Hovered() {
		row.Background(p.Secondary.Alpha(0.1))
	}
	row.Children(children)
}

// mergeButton is the bar's Merge when it leads, Lody's compact
// PrMergeButton: a green split pill 20 tall on an 8% green, the merge
// the chosen method makes under GitHub's merge glyph, or Merging… under a
// spinner, then past a 12px rule a chevron opening the methods above it,
// aligned to its end, the chosen one ticked. Each half deepens to 10%
// under the pointer over Tailwind's 150ms and dims to half while it
// can't be used; neither has a tooltip.
func (a *App) mergeButton(c *ui.Context, p *palette, sessionID string, act infoAction) {
	green := p.Success
	ui.Row(c.Key("merge-control")).Height(20).Shrink(0).Radius(6).Clip().Background(green.Alpha(0.08)).AlignItems(ui.Stretch).Children(func() {
		disabled := act.disabled || act.merging
		b := ui.ButtonBase(c.Key("merge")).Padding(0, 6).Gap(4).MinWidth(0).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(act.label).Disabled(disabled).Transition(twFade).OnClick(func() { a.runInfoAction(sessionID, "merge") })
		if b.Hovered() && !disabled {
			b.Background(green.Alpha(0.10))
		}
		if disabled {
			b.Opacity(0.5)
		}
		b.Children(func() {
			if act.merging {
				spinner(c).Size(14, 14)
			} else {
				ui.Icon(c, iconGitMerge).Size(14, 14).TextColor(green).Shrink(0)
			}
			ui.Text(c, act.label).FontSize(sizeFoot).FontWeight(500).TextColor(green).SingleLine().MinWidth(0)
		})
		st := &a.rowMenu("merge-method/" + sessionID).menuState
		more := ui.ButtonBase(c.Key("merge-method")).Width(20).Shrink(0).Cursor(ui.CursorPointer).Label(locales.InfoChooseMergeMethod()).
			Disabled(act.merging).Transition(twFade)
		method := a.mergeMethod()
		lodyMenuAbove(c, p, more, st, true, 4, 200, 290, 28, func(m *lodyMenu) {
			for _, o := range []option{{"merge", locales.InfoMergeCommit()}, {"squash", locales.InfoMergeSquash()}, {"rebase", locales.InfoMergeRebase()}} {
				var tick *ui.SVG
				if o.id == method {
					tick = iconCheck
				}
				if m.item(o.id, tick, o.label, menuItem{indicator: true}) {
					a.setMergeMethod(o.id)
				}
			}
		})
		more.Background(ui.Transparent)
		if (more.Hovered() || st.open) && !act.merging {
			more.Background(green.Alpha(0.10))
		}
		if act.merging {
			more.Opacity(0.5)
		}
		more.Children(func() {
			ui.Box(c).Absolute().Left(0).Top(4).Size(1, 12).Background(green.Alpha(0.25)).PassThrough()
			ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(green)
		})
	})
}
