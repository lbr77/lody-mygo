package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/github"
	"github.com/lbr77/lody-mygo/locales"
)

// The Pull request panel, as Lody's PR tab: the session's pull request
// read from GitHub with a token the workspace mints for this client
// (github:getAccessTokenByRepoNameForClient), its checks, reviews and
// conversation, the files it changes, and merging, commenting, closing
// and marking it ready with the token that acts as the user where they
// allowed it (github:getOperationAccessTokenByRepoNameForClient).

const tabPR = "pr"

type prPanel struct {
	repo        string
	number      int
	loading     bool
	err         string
	pr          *github.PullRequest
	checks      []github.CheckRun
	checksErr   string
	reviews     []github.Review
	comments    []github.Comment
	files       []github.File
	filesErr    string
	view        string // conversation or changes
	method      string // merge, squash or rebase
	busy        string // the operation under way
	opErr       string
	draft       string // the comment being written
	open        map[string]bool
	blocked     bool // the repository's identity can't be verified: operations pause
	loadedAt    time.Time
	identityRun bool
}

func (a *App) prPanelFor(s *catalog.Session) *prPanel {
	pr := s.LatestPR()
	if pr == nil {
		return nil
	}
	repo, n, ok := github.ParsePR(pr.URL)
	if !ok {
		return nil
	}
	if a.prPanels == nil {
		a.prPanels = map[string]*prPanel{}
	}
	key := s.ID + "|" + pr.URL
	st := a.prPanels[key]
	if st == nil {
		st = &prPanel{repo: repo, number: n, view: "conversation", method: "merge", open: map[string]bool{}}
		a.prPanels[key] = st
	}
	return st
}

// githubToken asks the workspace for a token to read the repository, or
// with operate, to act on it.
func (a *App) githubToken(ctx context.Context, repo string, operate bool) (string, error) {
	fn := "github:getAccessTokenByRepoNameForClient"
	if operate {
		fn = "github:getOperationAccessTokenByRepoNameForClient"
	}
	var res struct {
		Success      bool   `json:"success"`
		Token        string `json:"token"`
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := a.convex.Action(ctx, fn, map[string]any{"workspaceId": a.workspaceID, "repoFullName": repo}, &res); err != nil {
		return "", err
	}
	if !res.Success || res.Token == "" {
		return "", fmt.Errorf("%s", firstNonEmpty(res.ErrorMessage, res.ErrorCode, "GitHub token unavailable"))
	}
	return res.Token, nil
}

// loadPR reads the pull request, its checks, reviews, conversation and
// files, after checking the session's link to its repository.
func (a *App) loadPR(s *catalog.Session, st *prPanel) {
	if st.loading || a.convex == nil || a.offline {
		return
	}
	st.loading, st.err = true, ""
	convex, ws, sessionID := a.convex, a.workspaceID, s.ID
	firstIdentity := !st.identityRun
	st.identityRun = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		// The session's repository identity: a legacy link is resolved once.
		var versions struct {
			IdentityPending bool `json:"identityPending"`
			RepositoryID    *int `json:"repositoryId"`
		}
		blocked := false
		if err := convex.Query(ctx, "github:getPrCacheVersions", map[string]any{"workspaceId": ws, "repoFullName": st.repo, "prNumber": st.number, "sessionId": sessionID}, &versions); err == nil {
			if versions.IdentityPending || versions.RepositoryID == nil {
				if firstIdentity {
					var res struct {
						Resolved bool `json:"resolved"`
					}
					convex.Mutation(ctx, "github:resolveLegacyPrRepositoryIdentity", map[string]any{"workspaceId": ws, "repoFullName": st.repo, "prNumber": st.number, "sessionId": sessionID}, &res)
					blocked = !res.Resolved
				} else {
					blocked = true
				}
			}
		}
		token, err := a.githubToken(ctx, st.repo, false)
		var pr *github.PullRequest
		var checks []github.CheckRun
		var reviews []github.Review
		var comments []github.Comment
		var files []github.File
		checksErr, filesErr := "", ""
		if err == nil {
			gh := &github.Client{Token: token}
			if pr, err = gh.PullRequest(ctx, st.repo, st.number); err == nil {
				if checks, err = gh.CheckRuns(ctx, st.repo, pr.Head.SHA); err != nil {
					checksErr = err.Error()
					if github.IsStatus(err, 403) {
						checksErr = locales.PrTabChecksPermissionTitle()
					}
					err = nil
				}
				reviews, _ = gh.Reviews(ctx, st.repo, st.number)
				comments, _ = gh.Comments(ctx, st.repo, st.number)
				if files, err = gh.Files(ctx, st.repo, st.number); err != nil {
					filesErr, err = locales.PrTabChangesError(), nil
				}
			}
		}
		a.update(func() {
			st.loading, st.loadedAt, st.blocked = false, time.Now(), blocked
			if err != nil {
				st.err = err.Error()
				if github.IsStatus(err, 404) {
					st.err += "\n" + locales.PrTabNotFoundHint()
				}
				return
			}
			st.pr, st.checks, st.checksErr, st.reviews, st.comments, st.files, st.filesErr = pr, checks, checksErr, reviews, comments, files, filesErr
		})
	}()
}

// prAct runs an operation on the pull request with the acting token, then
// reads it again.
func (a *App) prAct(s *catalog.Session, st *prPanel, name string, op func(ctx context.Context, gh *github.Client) error, failed string) {
	if st.busy != "" || st.blocked || a.convex == nil {
		return
	}
	st.busy, st.opErr = name, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		token, err := a.githubToken(ctx, st.repo, true)
		if err == nil {
			err = op(ctx, &github.Client{Token: token})
		}
		a.update(func() {
			st.busy = ""
			if err != nil {
				st.opErr = failed + ": " + err.Error()
				return
			}
			a.loadPR(s, st)
		})
	}()
}

func (a *App) prTab(c *ui.Context, p *palette) {
	s := a.catalog.Session(a.selected)
	if s == nil {
		return
	}
	st := a.prPanelFor(s)
	if st == nil {
		panelStatus(c, p, false, locales.PrTabLoadError(), "")
		return
	}
	if st.pr == nil && st.err == "" && !st.loading {
		a.loadPR(s, st)
	}
	switch {
	case st.pr == nil && st.err != "":
		ui.Column(c).Grow(1).Center().Padding(24).Gap(10).Children(func() {
			ui.Text(c, locales.PrTabLoadError()).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			ui.Text(c, st.err).FontSize(sizeFoot).TextColor(p.Secondary).TextAlign(ui.Center).Selectable().MaxWidth(360)
			ui.Row(c).Gap(8).Children(func() {
				outlineButton(c, p, nil, locales.PrTabRetry()).OnClick(func() { st.err = ""; a.loadPR(s, st) })
				outlineButton(c, p, iconExternal, locales.PrTabOpenOnGitHub()).OnClick(func() { openExternal(s.LatestPR().URL) })
			})
		})
		return
	case st.pr == nil:
		ui.Column(c).FillWidth().Padding(16).Gap(10).Children(func() {
			skeleton(c, p, "pr-title", 18).WidthPercent(70)
			skeleton(c, p, "pr-meta", 12).WidthPercent(45)
			skeleton(c, p, "pr-body", 64).FillWidth()
		})
		return
	}
	pr := st.pr
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(14, 16, 24, 16).Gap(14).Children(func() {
		a.prHeader(c, p, s, st, pr)
		segmented(c, p, "pr-view", []option{{"conversation", locales.PrTabConversation()}, {"changes", locales.PrTabChangesCount(len(st.files))}}, st.view, func(v string) { st.view = v })
		if st.blocked {
			note(c, p, locales.PrTabRepositoryIdentityUnresolved(), p.Warning)
		}
		if st.opErr != "" {
			note(c, p, st.opErr, p.Danger)
		}
		if st.view == "changes" {
			a.prChanges(c, p, st)
			return
		}
		a.prConversation(c, p, s, st, pr)
	})
}

// prHeader is the pull request's state, title, author and branches, with
// its actions.
func (a *App) prHeader(c *ui.Context, p *palette, s *catalog.Session, st *prPanel, pr *github.PullRequest) {
	status := pr.Status()
	icon, hue := prTone(p, status)
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Height(22).Padding(0, 8).Radius(11).Gap(4).AlignItems(ui.Center).Background(hue.Alpha(0.12)).Children(func() {
				ui.Icon(c, icon).Size(13, 13).TextColor(hue)
				ui.Text(c, prStatusLabel(status)).FontSize(sizeCaption).FontWeight(600).TextColor(hue)
			})
			ui.Spacer(c)
			if st.loading {
				spinner(c).Size(13, 13)
			}
			iconButton(c, p, iconRefresh, locales.PrTabRefresh(), p.Hover).OnClick(func() { a.loadPR(s, st) })
			iconButton(c, p, iconExternal, locales.PrTabOpenOnGitHub(), p.Hover).OnClick(func() { openExternal(pr.HTMLURL) })
			iconButton(c, p, iconMore, locales.PrTabMoreActions(), p.Hover).Menu(func(m *ui.Menu) {
				if m.Item(locales.PrTabCopyBranch()).Chosen() {
					mygo.Clipboard.WriteText(pr.Head.Ref)
				}
				switch {
				case status == "open" || status == "draft":
					if m.Item(locales.PrTabCloseAction()).Disabled(st.blocked).Chosen() {
						a.prAct(s, st, "close", func(ctx context.Context, gh *github.Client) error {
							return gh.SetState(ctx, st.repo, st.number, "closed")
						}, locales.PrTabCloseError())
					}
				case status == "closed":
					if m.Item(locales.PrTabReopenAction()).Disabled(st.blocked).Chosen() {
						a.prAct(s, st, "reopen", func(ctx context.Context, gh *github.Client) error {
							return gh.SetState(ctx, st.repo, st.number, "open")
						}, locales.PrTabReopenError())
					}
				}
				if status == "merged" || status == "closed" {
					if m.Item(locales.PrTabDeleteBranch()).Disabled(st.blocked).Chosen() {
						a.prAct(s, st, "delete", func(ctx context.Context, gh *github.Client) error { return gh.DeleteBranch(ctx, st.repo, pr.Head.Ref) }, locales.PrTabDeleteBranchError())
					}
				}
			})
		})
		ui.RichText(c, ui.Span{Text: pr.Title, Weight: 600, Color: p.Strong}, ui.Span{Text: fmt.Sprintf(" #%d", pr.Number), Color: p.Secondary}).
			FontSize(16).LineHeight(1.35).Selectable()
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, pr.User.Login).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text)
			ui.Text(c, locales.PrTabOpened(prAgo(pr.CreatedAt))).FontSize(sizeFoot).TextColor(p.Secondary)
			ui.Text(c, "·").FontSize(sizeFoot).TextColor(p.Tertiary)
			branch := func(ref string) {
				ui.Text(c, ref).Font(fontMono).FontSize(sizeCaption).TextColor(p.Text).Padding(1, 6).Radius(5).Background(p.Text.Alpha(0.06)).SingleLine().MaxWidth(180)
			}
			branch(pr.Head.Ref)
			ui.Icon(c, iconArrowRight).Size(12, 12).TextColor(p.Tertiary)
			branch(pr.Base.Ref)
		})
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, locales.PrTabCommitsSummary(pr.Commits)).FontSize(sizeCaption).TextColor(p.Secondary)
			ui.Text(c, locales.PrTabFilesChanged(pr.ChangedFiles)).FontSize(sizeCaption).TextColor(p.Secondary)
			ui.Text(c, fmt.Sprintf("+%d", pr.Additions)).Font(fontMono).FontSize(sizeCaption).TextColor(p.DiffAdd)
			ui.Text(c, fmt.Sprintf("−%d", pr.Deletions)).Font(fontMono).FontSize(sizeCaption).TextColor(p.DiffDel)
		})
	})
}

func prAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return locales.PrTabTimeJustNow()
	case d < time.Hour:
		return locales.PrTabTimeMinutesAgo(int(d.Minutes()))
	case d < 24*time.Hour:
		return locales.PrTabTimeHoursAgo(int(d.Hours()))
	}
	return locales.PrTabTimeDaysAgo(int(d.Hours() / 24))
}

// prConversation is the description, the checks and the merge box, then
// the reviews and comments, and a box to comment.
func (a *App) prConversation(c *ui.Context, p *palette, s *catalog.Session, st *prPanel, pr *github.PullRequest) {
	card := func(key string, fn func()) {
		ui.Column(c.Key(key)).FillWidth().Gap(8).Padding(12).Radius(10).Border(1, p.Border).Children(fn)
	}
	card("summary", func() {
		ui.Text(c, locales.PrTabSummary()).FontSize(sizeCaption).FontWeight(600).TextColor(p.Secondary)
		if strings.TrimSpace(pr.Body) == "" {
			ui.Text(c, locales.PrTabNoDescription()).FontSize(sizeFoot).TextColor(p.Tertiary)
		} else {
			markdown.View(c.Key("pr-body"), pr.Body, &a.md)
		}
	})
	a.prChecks(c, p, st)
	if pr.Status() == "open" || pr.Status() == "draft" {
		a.prMergeBox(c, p, s, st, pr)
	} else if pr.Merged {
		note(c, p, locales.PrTabMergedAlready(), p.Secondary)
	} else {
		note(c, p, locales.PrTabClosed(), p.Secondary)
	}
	type event struct {
		at        time.Time
		who, verb string
		body      string
		tone      ui.Color
		key       string
	}
	var events []event
	for _, r := range st.reviews {
		verb, tone := locales.PrTabReviewCommented(), p.Secondary
		switch r.State {
		case "APPROVED":
			verb, tone = locales.PrTabReviewApproved(), p.Success
		case "CHANGES_REQUESTED":
			verb, tone = locales.PrTabReviewChangesRequested(), p.Danger
		case "DISMISSED":
			verb = locales.PrTabReviewDismissed()
		}
		events = append(events, event{r.SubmittedAt, r.User.Login, verb, r.Body, tone, fmt.Sprint("r", r.ID)})
	}
	for _, cm := range st.comments {
		events = append(events, event{cm.CreatedAt, cm.User.Login, locales.PrTabCommented(), cm.Body, p.Secondary, fmt.Sprint("c", cm.ID)})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	for _, e := range events {
		card(e.key, func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Size(20, 20).Radius(10).Background(p.Text.Alpha(0.08)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Text(c, strings.ToUpper(firstNonEmpty(e.who, "?")[:1])).FontSize(10).FontWeight(600).TextColor(p.Secondary)
				})
				ui.Text(c, e.who).FontSize(sizeFoot).FontWeight(600).TextColor(p.Text)
				ui.Text(c, e.verb).FontSize(sizeFoot).TextColor(e.tone)
				ui.Text(c, prAgo(e.at)).FontSize(sizeCaption).TextColor(p.Tertiary)
			})
			if strings.TrimSpace(e.body) != "" {
				markdown.View(c.Key("body/"+e.key), e.body, &a.md)
			}
		})
	}
	// The composer.
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		area(c, p, &st.draft, locales.PrTabComposerPlaceholder(), locales.PrTabComposerPlaceholder(), 3).Disabled(st.blocked)
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).AlignItems(ui.Center).Children(func() {
			if st.busy == "comment" {
				spinner(c).Size(13, 13)
			}
			body := strings.TrimSpace(st.draft)
			primaryButton(c, p, locales.PrTabComposerSubmit(), body != "" && st.busy == "" && !st.blocked).OnClick(func() {
				a.prAct(s, st, "comment", func(ctx context.Context, gh *github.Client) error {
					if err := gh.Comment(ctx, st.repo, st.number, body); err != nil {
						return err
					}
					a.update(func() { st.draft = "" })
					return nil
				}, locales.PrTabPostCommentError())
			})
		})
	})
}

// prChecks is the CI on the pull request's head: a verdict, and each
// check with its state.
func (a *App) prChecks(c *ui.Context, p *palette, st *prPanel) {
	if st.checksErr != "" {
		ui.Column(c).FillWidth().Gap(6).Padding(12).Radius(10).Background(p.Warning.Alpha(0.08)).Children(func() {
			ui.Text(c, locales.PrTabChecksPermissionTitle()).FontSize(sizeFoot).FontWeight(600).TextColor(p.Text)
			ui.Text(c, locales.PrTabChecksPermissionBody()).FontSize(sizeCaption).TextColor(p.Secondary)
		})
		return
	}
	if len(st.checks) == 0 {
		return
	}
	failed, running := 0, 0
	for _, r := range st.checks {
		switch {
		case r.Running():
			running++
		case r.Failed():
			failed++
		}
	}
	verdict, mark, ink := locales.PrTabChecksPassed(), markSuccess, p.Success
	switch {
	case failed > 0:
		verdict, mark, ink = locales.PrTabChecksFailedOf(failed, len(st.checks)), markDanger, p.Danger
	case running > 0:
		verdict, mark, ink = locales.PrTabChecksRunning(), markInfo, p.Warning
	}
	open := st.open["checks"]
	ui.Column(c.Key("checks")).FillWidth().Gap(6).Padding(12).Radius(10).Border(1, p.Border).Children(func() {
		head := ui.ButtonBase(c.Key("head")).FillWidth().Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(verdict).OnClick(func() { st.open["checks"] = !open })
		head.Children(func() {
			ui.Icon(c, mark).Size(15, 15).TextColor(ink)
			ui.Text(c, verdict).FontSize(sizeFoot).FontWeight(600).TextColor(p.Text).Grow(1)
			ui.Text(c, locales.PrTabChecksCount(len(st.checks))).FontSize(sizeCaption).TextColor(p.Secondary)
			ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.Secondary).Rotate(map[bool]float32{true: 180, false: 0}[open])
		})
		if !open {
			return
		}
		for _, r := range st.checks {
			run := r
			row := ui.ButtonBase(c.Key(fmt.Sprint(r.ID, r.Name))).FillWidth().Height(26).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(r.Name).Tooltip(locales.PrTabOpenCheck()).OnClick(func() { openExternal(run.HTMLURL) })
			row.Children(func() {
				switch {
				case run.Running():
					spinner(c).Size(12, 12)
				case run.Failed():
					ui.Icon(c, iconX).Size(12, 12).TextColor(p.Danger)
				case run.Conclusion == "success":
					ui.Icon(c, iconCheck).Size(12, 12).TextColor(p.Success)
				default:
					ui.Icon(c, iconDot).Size(12, 12).TextColor(p.Tertiary)
				}
				ui.Text(c, run.Name).FontSize(sizeFoot).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
			})
		}
	})
}

// prMergeBox says whether the pull request can merge, and merges it the
// way chosen, or takes it out of draft.
func (a *App) prMergeBox(c *ui.Context, p *palette, s *catalog.Session, st *prPanel, pr *github.PullRequest) {
	failed := 0
	for _, r := range st.checks {
		if r.Failed() {
			failed++
		}
	}
	verdict, ink := locales.PrTabVerdictReady(), p.Success
	switch {
	case pr.Draft:
		verdict, ink = locales.PrTabVerdictDraft(), p.Secondary
	case pr.MergeableState == "dirty":
		verdict, ink = locales.PrTabVerdictConflict(), p.Danger
	case pr.MergeableState == "blocked":
		verdict, ink = locales.PrTabVerdictBlocked(), p.Warning
	case failed > 0:
		verdict, ink = locales.PrTabVerdictChecksFailed(failed), p.Danger
	case pr.MergeableState == "unknown" || pr.Mergeable == nil:
		verdict, ink = locales.PrTabMergeChecking(), p.Secondary
	}
	ui.Column(c.Key("merge")).FillWidth().Gap(10).Padding(12).Radius(10).Border(1, ink.Alpha(0.35)).Background(ink.Alpha(0.05)).Children(func() {
		ui.Text(c, verdict).FontSize(sizeFoot).FontWeight(600).TextColor(ink)
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if pr.Draft {
				if st.busy == "ready" {
					spinner(c).Size(13, 13)
				}
				primaryButton(c, p, locales.PrTabReadyForReview(), st.busy == "" && !st.blocked).OnClick(func() {
					a.prAct(s, st, "ready", func(ctx context.Context, gh *github.Client) error { return gh.ReadyForReview(ctx, pr.NodeID) }, locales.PrTabReadyForReviewError())
				})
				return
			}
			if pr.MergeableState == "dirty" {
				outlineButton(c, p, iconExternal, locales.PrTabResolveConflicts()).OnClick(func() { openExternal(pr.HTMLURL + "/conflicts") })
				return
			}
			methods := []option{{"merge", locales.PrTabMergeShortMerge()}, {"squash", locales.PrTabMergeShortSquash()}, {"rebase", locales.PrTabMergeShortRebase()}}
			segmented(c, p, "merge-method", methods, st.method, func(m string) { st.method = m })
			label := map[string]string{"merge": locales.PrTabMergeAction(), "squash": locales.PrTabMergeSquashAction(), "rebase": locales.PrTabMergeRebaseAction()}[st.method]
			if st.busy == "merge" {
				label = locales.PrTabMerging()
			}
			method := st.method
			primaryButton(c, p, label, st.busy == "" && !st.blocked && pr.MergeableState != "blocked").FillWidth().OnClick(func() {
				a.prAct(s, st, "merge", func(ctx context.Context, gh *github.Client) error { return gh.Merge(ctx, st.repo, st.number, method) }, locales.PrTabMergeError())
			})
		})
	})
}

// prChanges lists the files the pull request changes, each opening to its
// patch.
func (a *App) prChanges(c *ui.Context, p *palette, st *prPanel) {
	if st.filesErr != "" {
		note(c, p, st.filesErr, p.Danger)
		return
	}
	ui.Column(c).FillWidth().Radius(10).Border(1, p.Border).Clip().Dividers(1, p.Border).Children(func() {
		for _, f := range st.files {
			file := f
			open := st.open["file/"+f.Filename]
			ui.Column(c.Key(f.Filename)).FillWidth().Children(func() {
				head := ui.ButtonBase(c.Key("head")).FillWidth().MinHeight(34).Padding(6, 10).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
					Label(file.Filename).Transition(colorFade).OnClick(func() { st.open["file/"+file.Filename] = !open })
				if head.Hovered() {
					head.Background(p.Hover)
				}
				head.Children(func() {
					ui.Icon(c, iconChevronRight).Size(12, 12).TextColor(p.Tertiary).Rotate(map[bool]float32{true: 90, false: 0}[open])
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, file.Filename).Font(fontMono).FontSize(sizeCaption).TextColor(p.Text).SingleLine().Tooltip(file.Filename)
						if file.PreviousFilename != "" {
							ui.Text(c, locales.PrTabFileRenamed(file.PreviousFilename)).FontSize(sizeCaption).TextColor(p.Tertiary).SingleLine()
						}
					})
					ui.Text(c, fmt.Sprintf("+%d", file.Additions)).Font(fontMono).FontSize(sizeCaption).TextColor(p.DiffAdd).Shrink(0)
					ui.Text(c, fmt.Sprintf("−%d", file.Deletions)).Font(fontMono).FontSize(sizeCaption).TextColor(p.DiffDel).Shrink(0)
				})
				if !open {
					return
				}
				if file.Patch == "" {
					ui.Text(c, locales.PrTabDiffUnavailable()).FontSize(sizeCaption).TextColor(p.Secondary).Padding(8, 12)
					return
				}
				ui.ScrollHorizontal(c).FillWidth().Background(p.CodeBg).Children(func() {
					ui.Column(c).Padding(6, 0).Children(func() {
						for i, line := range strings.Split(file.Patch, "\n") {
							ink, bg := p.Text, ui.Transparent
							switch {
							case strings.HasPrefix(line, "@@"):
								ink, bg = p.Secondary, p.Text.Alpha(0.04)
							case strings.HasPrefix(line, "+"):
								bg = p.DiffAdd.Alpha(0.12)
							case strings.HasPrefix(line, "-"):
								bg = p.DiffDel.Alpha(0.12)
							}
							ui.Text(c.Key(i), line).Font(fontMono).FontSize(sizeCaption).LineHeight(1.5).TextColor(ink).Background(bg).Padding(0, 12).Selectable().SingleLine()
						}
					})
				})
			})
		}
	})
}
