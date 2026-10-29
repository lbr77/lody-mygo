package convmd

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/session"
)

// Lody's conversation-markdown.test.ts, on our projection's entries. Its
// share-card tests are for collectConversationMessages, which is not
// ported, and its comment and visual annotation reference tests need
// payloads our projection does not keep.

var nextID int

func entry(role string, items ...session.Item) session.Entry {
	nextID++
	at := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC).Add(time.Duration(nextID) * time.Second)
	return session.Entry{ID: fmt.Sprintf("entry-%d", nextID), Role: role, Timestamp: at.Format(time.RFC3339Nano), Items: items}
}

func text(s string) session.Item { return session.Item{Type: "text", Text: s} }

func thought(s string) session.Item { return session.Item{Type: "thought", Text: s} }

// toolCall is a call whose output is a result or, with terminal, npm
// install's. Lody's tests name the tool; our projection keeps its kind.
func toolCall(title, output string, terminal bool, kind string) session.Item {
	content := []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": output}}}
	if terminal {
		content = []any{
			map[string]any{"type": "terminal_command", "command": "npm", "args": []any{"install"}},
			map[string]any{"type": "terminal_output", "output": output},
		}
	}
	if kind == "" {
		kind = "other"
	}
	return session.Item{Type: "tool_call", Title: title, Kind: kind, Status: "completed", Content: content, HasDetail: true}
}

// heavyHistory is a conversation whose bulk is tool output, not prose.
func heavyHistory(turns, outputChars int) []session.Entry {
	var history []session.Entry
	for i := range turns {
		history = append(history,
			entry("user", text(fmt.Sprintf("Question %d", i))),
			entry("assistant",
				thought(strings.Repeat(fmt.Sprintf("deliberating %d ", i), 200)),
				toolCall(fmt.Sprintf("Read src/file-%d.ts", i), strings.Repeat(fmt.Sprintf("line %d ", i), outputChars/8), false, "read"),
				toolCall(fmt.Sprintf("Bash %d", i), strings.Repeat(fmt.Sprintf("stdout %d ", i), outputChars/8), true, "execute"),
				text(fmt.Sprintf("Answer %d", i))))
	}
	return history
}

func matches(t *testing.T, md, pattern string) bool {
	t.Helper()
	return regexp.MustCompile(pattern).MatchString(md)
}

func TestPartialItems(t *testing.T) {
	md, _ := Build([]session.Entry{entry("assistant", session.Item{Type: "text"}, session.Item{Type: "thought"}, text("Kept"))}, Options{WorkspaceID: "ws"})
	if !strings.Contains(md, "Kept") {
		t.Errorf("markdown = %q", md)
	}
}

func TestSessionLinksLeaveEntries(t *testing.T) {
	history := func() []session.Entry {
		e := entry("user", text("See [Old](session://child) and `session://example`"))
		e.ID, e.Timestamp = "fixed", "2026-09-08T14:00:00Z"
		return []session.Entry{e}
	}
	entries := history()
	md, stats := Build(entries, Options{WorkspaceID: "ws_1"})
	if !strings.Contains(md, "[Old](lody://session/child?workspace=ws_1)") || !strings.Contains(md, "`session://example`") {
		t.Errorf("markdown = %q", md)
	}
	if stats.Chars != jsLen(md) {
		t.Errorf("chars = %d, want %d", stats.Chars, jsLen(md))
	}
	if !reflect.DeepEqual(entries, history()) {
		t.Errorf("entries changed: %+v", entries)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(strings.Repeat("a", 400)); got != 100 {
		t.Errorf("latin = %d", got)
	}
	if got := EstimateTokens(strings.Repeat("中文测试", 100)); got != 400 {
		t.Errorf("CJK = %d", got)
	}
}

func TestHeadingsAndTitle(t *testing.T) {
	md, stats := Build([]session.Entry{entry("user", text("hello")), entry("assistant", text("hi there"))}, Options{Title: "My session"})
	for _, want := range []string{"# My session", "hello", "hi there"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in %q", want, md)
		}
	}
	if !matches(t, md, `(?m)^## 1 · User · `) || !matches(t, md, `(?m)^## 1 · Assistant · `) {
		t.Errorf("turn headings: %q", md)
	}
	if stats.EntryCount != 2 || stats.OverBudget {
		t.Errorf("stats = %+v", stats)
	}
}

func TestSmallKeepsEverything(t *testing.T) {
	md, stats := Build([]session.Entry{
		entry("user", text("run the tests")),
		entry("assistant", thought("thinking about it"), toolCall("Bash", "all tests passed", true, "execute"), text("Done.")),
	}, Options{})
	if !strings.Contains(md, "thinking about it") || !strings.Contains(md, "all tests passed") || strings.Contains(md, "Trimmed to fit") {
		t.Errorf("markdown = %q", md)
	}
	if stats.ThinkingTruncated || stats.TerminalOutputOmitted || stats.ToolCallsCollapsed {
		t.Errorf("stats = %+v", stats)
	}
}

func TestTrimsToBudget(t *testing.T) {
	md, stats := Build(heavyHistory(12, 8000), Options{})
	if jsLen(md) > MaxChars || stats.EstimatedTokens > MaxTokens || stats.OverBudget {
		t.Errorf("chars %d, stats %+v", jsLen(md), stats)
	}
	if !strings.Contains(md, "Trimmed to fit") {
		t.Error("no trim notice")
	}
	// Message text is never dropped.
	for i := range 12 {
		if !strings.Contains(md, fmt.Sprintf("Question %d", i)) || !strings.Contains(md, fmt.Sprintf("Answer %d", i)) {
			t.Errorf("turn %d's text is missing", i)
		}
	}
}

func TestOlderTurnsFirst(t *testing.T) {
	md, _ := Build(heavyHistory(12, 8000), Options{RecentEntries: 4})
	toolBody := func(summary string) string {
		start := strings.Index(md, summary)
		if start < 0 {
			t.Fatalf("no %q", summary)
		}
		if end := strings.Index(md[start:], "</details>"); end >= 0 {
			return md[start : start+end]
		}
		return md[start:]
	}
	if strings.Contains(toolBody("Read src/file-11.ts"), "characters elided") {
		t.Error("the newest turn's output was cut")
	}
	if !strings.Contains(toolBody("Read src/file-0.ts"), "characters elided") {
		t.Error("the oldest turn's output was kept whole")
	}
}

func TestOverBudgetProse(t *testing.T) {
	prose := strings.Repeat("word ", 30_000)
	md, stats := Build([]session.Entry{entry("user", text(prose)), entry("assistant", text(prose))}, Options{})
	if !stats.OverBudget || jsLen(md) <= MaxChars || strings.Count(md, prose) != 2 {
		t.Errorf("chars %d, stats %+v", jsLen(md), stats)
	}
}

func TestCJKBudget(t *testing.T) {
	// 66k CJK characters is under the character ceiling but ~66k tokens.
	md, stats := Build([]session.Entry{entry("user", text(strings.Repeat("这是一段中文对话内容。", 6000)))}, Options{})
	if jsLen(md) >= MaxChars || stats.EstimatedTokens <= MaxTokens || !stats.OverBudget {
		t.Errorf("chars %d, stats %+v", jsLen(md), stats)
	}
}

func TestRedactsTerminalOutput(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 30)
	md, _ := Build([]session.Entry{entry("assistant", toolCall("Bash", "token "+secret+" used", true, "execute"))}, Options{})
	if !strings.Contains(md, "ghp_***") || strings.Contains(md, secret) {
		t.Errorf("markdown = %q", md)
	}
}

func TestFencesFences(t *testing.T) {
	md, _ := Build([]session.Entry{entry("assistant", toolCall("Read", "```ts\nconst a = 1;\n```", false, "read"))}, Options{})
	if !strings.Contains(md, "````") {
		t.Errorf("markdown = %q", md)
	}
}

func TestSkipsSystemAndEmpty(t *testing.T) {
	md, stats := Build([]session.Entry{
		entry("system", text("system notice")),
		entry("assistant"),
		entry("user", text("only this")),
	}, Options{})
	if strings.Contains(md, "system notice") || stats.EntryCount != 1 {
		t.Errorf("markdown = %q, stats %+v", md, stats)
	}
}

func TestSkipsQueued(t *testing.T) {
	queued := entry("user", text("not sent yet"))
	queued.Queued = true
	md, stats := Build([]session.Entry{entry("user", text("sent")), queued}, Options{})
	if strings.Contains(md, "not sent yet") || stats.EntryCount != 1 {
		t.Errorf("markdown = %q, stats %+v", md, stats)
	}
}

func TestDemotesHeadings(t *testing.T) {
	body := strings.Join([]string{"# Summary", "", "```bash", "# not a heading", "```", "", "###### already deepest"}, "\n")
	md, _ := Build([]session.Entry{entry("assistant", text(body))}, Options{})
	if !matches(t, md, `(?m)^### Summary$`) || matches(t, md, `(?m)^# Summary$`) ||
		!matches(t, md, `(?m)^# not a heading$`) || !matches(t, md, `(?m)^###### already deepest$`) {
		t.Errorf("markdown = %q", md)
	}
}

func TestRoundsModelAndTime(t *testing.T) {
	firstUser := entry("user", text("first ask"))
	firstUser.Timestamp = "2026-09-08T14:32:00.000Z"
	reply := entry("assistant", text("first answer"))
	reply.Timestamp = "2026-09-08T14:32:10.000Z"
	// Lody takes 60s of approval waits off 312s; our projection has no
	// waits, so the reply ends 60s sooner.
	reply.EndedAt = float64(time.Date(2026, 9, 8, 14, 37, 22, 0, time.UTC).UnixMilli() - 60_000)
	reply.Model = "Opus 5"
	secondUser := entry("user", text("second ask"))
	secondUser.Timestamp = "2026-09-08T15:00:00.000Z"

	md, _ := Build([]session.Entry{firstUser, reply, secondUser}, Options{Source: "LodyAI/Lody · feat/markdown"})
	if !matches(t, md, `(?m)^## 1 · User · `) || !matches(t, md, `(?m)^## 1 · Assistant · [^\n]*Opus 5 · 4m12s$`) || !matches(t, md, `(?m)^## 2 · User · `) {
		t.Errorf("headings: %q", md)
	}
	// One rule closes the header, one opens the second round.
	if n := len(regexp.MustCompile(`(?m)^---$`).FindAllString(md, -1)); n != 2 {
		t.Errorf("%d rules in %q", n, md)
	}
	if !matches(t, md, `## 1 · Assistant[\s\S]*\n---\n\n## 2 · User`) || !strings.Contains(md, "> LodyAI/Lody · feat/markdown") || !strings.Contains(md, "> Models: Opus 5") {
		t.Errorf("markdown = %q", md)
	}
}

func TestSpeakerNames(t *testing.T) {
	participants := map[string]string{"user-ada": "Ada", "user-bo": "Bo"}
	solo := entry("user", text("solo ask"))
	solo.UserID = "user-ada"
	md, _ := Build([]session.Entry{solo}, Options{Participants: participants})
	if strings.Contains(md, "Ada") || strings.Contains(md, "Participants:") {
		t.Errorf("solo: %q", md)
	}

	ada, bo := entry("user", text("ada ask")), entry("user", text("bo ask"))
	ada.UserID, bo.UserID = "user-ada", "user-bo"
	md, _ = Build([]session.Entry{ada, bo}, Options{Participants: participants})
	if !matches(t, md, `(?m)^## 1 · User · Ada · `) || !matches(t, md, `(?m)^## 2 · User · Bo · `) || !strings.Contains(md, "> Participants: Ada, Bo") {
		t.Errorf("shared: %q", md)
	}
}

func TestCollapsesBeforeCappingThinking(t *testing.T) {
	var history []session.Entry
	for i := range 20 {
		history = append(history,
			entry("user", text(fmt.Sprintf("Question %d", i))),
			entry("assistant",
				thought(strings.Repeat(fmt.Sprintf("reasoning %d ", i), 12)),
				toolCall(fmt.Sprintf("Read src/file-%d.ts", i), strings.Repeat("x", 20_000), false, "read"),
				toolCall(fmt.Sprintf("Bash %d", i), strings.Repeat("y", 20_000), true, "execute"),
				text(fmt.Sprintf("Answer %d", i))))
	}
	// Room for prose, thinking and summaries, not tool bodies.
	md, stats := Build(history, Options{MaxChars: 11_000, MaxTokens: 100_000})
	if !stats.ToolCallsCollapsed || stats.ThinkingTruncated {
		t.Errorf("stats = %+v", stats)
	}
	if !strings.Contains(md, "2 tool calls (details omitted)") || !strings.Contains(md, "`execute` ×1 · `read` ×1") {
		t.Errorf("markdown = %q", md)
	}
	for i := range 20 {
		if !strings.Contains(md, fmt.Sprintf("reasoning %d reasoning %d", i, i)) {
			t.Errorf("turn %d's reasoning is missing", i)
		}
	}
	if strings.Contains(md, "- **Read src/file-0.ts**") {
		t.Error("a call got its own line")
	}
}

func TestCapsThinking(t *testing.T) {
	var history []session.Entry
	for i := range 12 {
		history = append(history,
			entry("user", text(fmt.Sprintf("Q%d", i))),
			entry("assistant",
				thought(strings.Repeat(fmt.Sprintf("deliberating %d ", i), 600)),
				toolCall(fmt.Sprintf("Read %d", i), strings.Repeat("x", 5000), false, "read"),
				text(fmt.Sprintf("A%d", i))))
	}
	md, stats := Build(history, Options{MaxChars: 30_000, MaxTokens: 100_000})
	if !stats.ThinkingTruncated {
		t.Errorf("stats = %+v", stats)
	}
	for _, want := range []string{"Thinking (from the original session)", "characters elided", "deliberating 0 deliberating", "deliberating 11 deliberating"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestIncompleteNoticeInHeader(t *testing.T) {
	md, _ := Build([]session.Entry{entry("user", text("ask")), entry("assistant", text("partial"))},
		Options{IncompleteFinalResponse: "The last response was still generating when copied."})
	notice, first := strings.Index(md, "still generating when copied"), strings.Index(md, "## 1 · User")
	if notice < 0 || notice > first || !strings.Contains(md, "> **The last response was still generating") || !strings.HasSuffix(strings.TrimRight(md, " \n"), "partial") {
		t.Errorf("markdown = %q", md)
	}
	if md, _ := Build([]session.Entry{entry("user", text("ask"))}, Options{}); strings.Contains(md, "still generating") {
		t.Errorf("unasked notice: %q", md)
	}
}

func TestTrimNoticeInHeader(t *testing.T) {
	md, _ := Build(heavyHistory(30, 8000), Options{})
	notice, first := strings.Index(md, "Trimmed to fit the copy budget"), strings.Index(md, "## 1 · User")
	if notice < 0 || notice > first {
		t.Errorf("notice at %d, first turn at %d", notice, first)
	}
}

func TestCollapsedNoticeSaysResultsGone(t *testing.T) {
	md, stats := Build(heavyHistory(20, 8000), Options{MaxChars: 9000, MaxTokens: 100_000})
	if !stats.ToolCallsCollapsed || !strings.Contains(md, "results and terminal output omitted") {
		t.Errorf("stats = %+v", stats)
	}
}

func TestListsFiles(t *testing.T) {
	// Lody lists a call's locations; ours come from its diffs.
	call := session.Item{Type: "tool_call", Title: "Edit", Kind: "edit", Status: "completed",
		Content: []any{map[string]any{"type": "diff", "path": "src/a.ts", "oldText": "a", "newText": "b"}}}
	md, stats := Build([]session.Entry{entry("assistant", call)}, Options{})
	if !strings.Contains(md, "## Files referenced (from tool call locations)\n\n- **Edited:** `src/a.ts`") || stats.PathsCount != 1 {
		t.Errorf("markdown = %q, stats %+v", md, stats)
	}
}

func TestAttachments(t *testing.T) {
	md, _ := Build([]session.Entry{entry("user", session.Item{Type: "file", FileName: "context.txt", SizeBytes: 5001})}, Options{})
	if !strings.Contains(md, "context.txt") || !strings.Contains(md, "File contents not included.") {
		t.Errorf("file: %q", md)
	}
	image := session.Item{Type: "image", ImageID: "private-blob-id", MimeType: "image/png", FileName: "screenshot.png"}
	md, _ = Build([]session.Entry{entry("user", image)}, Options{})
	if !strings.Contains(md, "screenshot.png") || !strings.Contains(md, "Image contents not included.") || strings.Contains(md, "private-blob-id") {
		t.Errorf("image: %q", md)
	}
}

// TestLodyGolden compares Build with Lody's own builder, run on the same
// histories by testdata/lody.ts.
func TestLodyGolden(t *testing.T) {
	f, err := os.Open("testdata/lody.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Histories map[string][]map[string]any
		Cases     []struct {
			Name, History string
			Options       struct {
				WorkspaceID, Title, Source, IncompleteFinalResponse string
				Participants                                        map[string]string
				MaxChars, MaxTokens, RecentEntryCount               int
				IncludeTrimNotice                                   *bool
			}
			Markdown string
			Stats    Stats
		}
	}
	if err := json.NewDecoder(z).Decode(&golden); err != nil || len(golden.Cases) == 0 {
		t.Fatal("no golden cases:", err)
	}

	local := time.Local
	time.Local = time.UTC // as the corpus was made, with TZ=UTC
	defer func() { time.Local = local }()

	for _, c := range golden.Cases {
		var entries []session.Entry
		for _, e := range golden.Histories[c.History] {
			entries = append(entries, fromLody(e))
		}
		o := c.Options
		md, stats := Build(entries, Options{
			WorkspaceID: o.WorkspaceID, Title: o.Title, Source: o.Source, Participants: o.Participants,
			IncompleteFinalResponse: o.IncompleteFinalResponse, MaxChars: o.MaxChars, MaxTokens: o.MaxTokens,
			RecentEntries: o.RecentEntryCount, OmitTrimNotice: o.IncludeTrimNotice != nil && !*o.IncludeTrimNotice,
		})
		if md != c.Markdown {
			t.Errorf("%s: markdown differs from Lody's at byte %d\n got %q\nwant %q", c.Name, firstDiff(md, c.Markdown), excerpt(md, c.Markdown), excerpt(c.Markdown, md))
		}
		if stats != c.Stats {
			t.Errorf("%s: stats = %+v\nwant %+v", c.Name, stats, c.Stats)
		}
	}
}

// fromLody projects a Lody history entry as session's projection does,
// as far as Build reads it.
func fromLody(raw map[string]any) session.Entry {
	str := func(v any) string { s, _ := v.(string); return s }
	e := session.Entry{ID: str(raw["id"]), Role: str(raw["role"]), Timestamp: str(raw["timestamp"]), UserID: str(raw["userId"])}
	if m, ok := raw["modelInfo"].(map[string]any); ok {
		e.Model = strings.TrimSpace(str(m["name"]))
		if e.Model == "" {
			e.Model = strings.TrimSpace(str(m["modelId"]))
		}
	}
	e.EndedAt, _ = raw["endedAt"].(float64)
	items, _ := raw["items"].([]any)
	for _, v := range items {
		m := v.(map[string]any)
		it := session.Item{Type: str(m["type"])}
		switch it.Type {
		case "text", "thought":
			it.Text = str(m["text"])
		case "proposed_plan":
			it.Text, it.Status = str(m["markdown"]), str(m["status"])
		case "file":
			it.FileName, it.MimeType = str(m["fileName"]), str(m["mimeType"])
			size, _ := m["sizeBytes"].(float64)
			it.SizeBytes = int64(size)
		case "image_group":
			images, _ := m["images"].([]any)
			for range images {
				it.Images = append(it.Images, session.Item{Type: "image"})
			}
		case "tool_call":
			it.Kind, it.Title, it.Status = str(m["kind"]), str(m["title"]), str(m["status"])
			if it.Kind == "" {
				it.Kind = "other"
			}
			if it.Title == "" {
				it.Title = str(m["toolName"])
			}
			it.Content, _ = m["content"].([]any)
		case "plan":
			entries, _ := m["entries"].([]any)
			for _, p := range entries {
				p := p.(map[string]any)
				it.Plan = append(it.Plan, session.PlanEntry{Content: str(p["content"]), Status: str(p["status"])})
			}
		case "subagent_task":
			it.Subagent = &session.Subagent{Description: str(m["description"]), Status: str(m["status"]),
				SubagentType: str(m["subagentType"]), TaskType: str(m["taskType"]), SkipTranscript: m["skipTranscript"] == true}
		case "system_notice":
			it.Title, it.Text = str(m["name"]), str(m["meta"].(map[string]any)["message"])
		}
		e.Items = append(e.Items, it)
	}
	return e
}

func firstDiff(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

// excerpt is a's text around where it first differs from b.
func excerpt(a, b string) string {
	i := firstDiff(a, b)
	return a[max(0, i-80):min(len(a), i+80)]
}
