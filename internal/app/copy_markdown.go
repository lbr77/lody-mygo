package app

import (
	"slices"
	"strings"

	"github.com/lbr77/lody-mygo/internal/convmd"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// copyConversation copies the open conversation as Markdown, as Lody's
// "Copy as Markdown": sized to paste into another chat, its trims named in
// the toast. A turn still being written is said so at the top.
func (a *App) copyConversation(id string) {
	a.copyConversationThrough(id, "")
}

// copyConversationThrough copies the conversation up to and including the
// entry through, the whole of it when through is "", as Lody's Copy
// context does from a message.
func (a *App) copyConversationThrough(id, through string) {
	v := a.view
	if v == nil || id != a.selected || len(v.Entries) == 0 {
		a.toast("error", locales.AccessHistoryEmpty())
		return
	}
	s := a.catalog.Session(id)
	opt := convmd.Options{WorkspaceID: a.workspaceID}
	if s != nil {
		opt.Title = s.Title
		repo := s.RepoFullName
		if strings.HasPrefix(s.ProjectID, "github:") {
			repo = strings.TrimPrefix(s.ProjectID, "github:")
		}
		var parts []string
		for _, p := range []string{strings.TrimSpace(repo), strings.TrimSpace(s.BranchName)} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		opt.Source = strings.Join(parts, " · ")
	}
	if members, _ := a.members(); len(members) > 0 {
		opt.Participants = map[string]string{}
		for uid, m := range members {
			opt.Participants[uid] = firstNonEmpty(m.User.Name, m.User.Email)
		}
	}
	entries := foldNotices(v.Entries)
	if through != "" {
		end := slices.IndexFunc(entries, func(e session.Entry) bool { return e.ID == through })
		if end < 0 {
			a.toast("error", locales.AccessHistoryEmpty())
			return
		}
		entries = entries[:end+1]
	}
	if last := entries[len(entries)-1]; last.Role == "assistant" && !last.Finished {
		opt.IncompleteFinalResponse = locales.AccessIncomplete()
	}
	// A long session takes a moment to build: off the main thread.
	go func() {
		md, stats := convmd.Build(entries, opt)
		a.update(func() {
			writeClipboard(md)
			a.toast("success", copiedDescription(stats))
		})
	}()
}

// copiedDescription is what the toast says of a copy: what was trimmed to
// fit, or that the messages alone are over.
func copiedDescription(st convmd.Stats) string {
	if st.OverBudget {
		return locales.CopiedMarkdownOver(int(float64(st.EstimatedTokens)/1000 + 0.5))
	}
	var trimmed []string
	if st.ToolCallsCollapsed {
		trimmed = append(trimmed, locales.TrimToolCalls())
	}
	if st.ThinkingTruncated {
		trimmed = append(trimmed, locales.TrimThinking())
	}
	if st.TerminalOutputOmitted || st.TerminalOutputTruncated {
		trimmed = append(trimmed, locales.TrimTerminal())
	}
	if st.ToolResultsTruncated > 0 {
		trimmed = append(trimmed, locales.TrimToolResults(st.ToolResultsTruncated))
	}
	if len(trimmed) == 0 {
		return locales.AccessHistoryCopied()
	}
	return locales.CopiedMarkdownTrimmed(strings.Join(trimmed, ", "))
}
