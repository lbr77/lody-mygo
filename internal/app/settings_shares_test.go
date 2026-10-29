package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/locales"
)

// seedFetch puts what a settings page would have fetched under key.
func seedFetch(t *testing.T, a *App, key, raw string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		t.Fatal(key, err)
	}
	if a.fetches == nil {
		a.fetches = map[string]*fetched{}
	}
	a.fetches[a.workspaceID+"|"+key] = &fetched{value: into}
}

// The published shares come 20 at a time: Next asks for the page after
// this one by its cursor, Previous goes back, and neither shows while
// there is nowhere to go. A share whose link this device holds opens its
// published page in the browser, and copies its link, from its details.
// The revoked ones show once asked for.
func TestSharesPaging(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "shares"
	secret := strings.Repeat("ab", 32)
	old := linkSecret
	linkSecret = func(string, float64) string { return secret }
	defer func() { linkSecret = old }()
	var opened, copied string
	oldOpen, oldCopy := openExternal, writeClipboard
	openExternal = func(u string) { opened = u }
	writeClipboard = func(s string) { copied = s }
	defer func() { openExternal, writeClipboard = oldOpen, oldCopy }()

	seedFetch(t, a, "shares", `{"page":[{"shareId":"sh1","rootSessionId":"s1","publisherUserId":"u1","status":"active","revision":1,"credentialVersion":1,"canManage":true,"canRevoke":true,"title":"Flock port","createdAt":1798761600000,"totalBytes":2048,"conversationCount":1}],"isDone":false,"continueCursor":"c1"}`, &sharesPage{})
	tt := snap(t, a, "settings-shares-paged", false, nil)
	for _, want := range []string{"Flock port", locales.SharesPrevious(), locales.SharesNext()} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	openDetail(t, tt, "Flock port")
	if err := tt.Click(locales.SharesOpenPublished()); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	link := "https://share.lody.ai/s/sh1#access=v1." + secret
	if opened != link {
		t.Fatalf("opened %q", opened)
	}
	if err := tt.Click(locales.ShareCopy()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if copied != link {
		t.Fatalf("copied %q", copied)
	}
	a.sharePages.detailOpen = false
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	// Previous does nothing on the first page.
	if err := tt.Click(locales.SharesPrevious()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.sharePages.cursors) != 0 {
		t.Fatalf("went back from the first page: %q", a.sharePages.cursors)
	}

	if err := tt.Click(locales.SharesNext()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sharePages.cursor() != "c1" || a.fetches["w1|shares/c1"] == nil {
		t.Fatalf("next page: %q, fetched %v", a.sharePages.cursors, a.fetches["w1|shares/c1"])
	}
	seedFetch(t, a, "shares/c1", `{"page":[{"shareId":"sh2","publisherUserId":"u2","status":"revoked","revision":2,"credentialVersion":1,"title":"Sidebar marks","createdAt":1798761600000,"totalBytes":10,"conversationCount":1}],"isDone":true,"continueCursor":""}`, &sharesPage{})
	tt.Frame()
	// The revoked share hides until asked for.
	if tt.HasText("Sidebar marks") || !tt.HasText(locales.SharesShowRevoked(1)) || !tt.HasText(locales.SharesNone()) {
		t.Fatalf("second page: %q", tt.Texts())
	}
	if err := tt.Click(locales.SharesShowRevoked(1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.sharePages.showRevoked || !tt.HasText("Sidebar marks") || tt.HasText("Flock port") || !tt.HasText(locales.SharesPrevious()) {
		t.Fatalf("second page, revoked shown: %q", tt.Texts())
	}
	snapFrame(t, tt, "settings-shares-page2")
	if err := tt.Click(locales.SharesPrevious()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sharePages.cursor() != "" || !tt.HasText("Flock port") {
		t.Fatalf("back: %q in %q", a.sharePages.cursors, tt.Texts())
	}

	// A list of one page has no pager.
	seedFetch(t, a, "shares", `{"page":[],"isDone":true,"continueCursor":""}`, &sharesPage{})
	tt.Frame()
	if tt.HasText(locales.SharesNext()) || !tt.HasText(locales.SharesNone()) {
		t.Fatalf("one page: %q", tt.Texts())
	}
	// Another workspace starts from its first page.
	a.sharePages.cursors = []string{"c1"}
	a.workspaceID = "w2"
	tt.Frame()
	if a.sharePages.cursor() != "" || a.sharePages.workspace != "w2" || a.sharePages.showRevoked {
		t.Fatalf("paging kept across workspaces: %+v", a.sharePages)
	}
}

// openDetail opens a share's details from its row, and lets the dialog
// rise into place.
func openDetail(t *testing.T, tt *ui.Tester, title string) {
	t.Helper()
	if err := tt.Click(title); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
}

// A share's details, as Lody's desktop app shows them: when it was
// published and last updated, its conversations, size and status; why
// there is no link where there is none; the conversation it was made
// from, Update share, and Reset link and Revoke share, which ask first
// and act only on the share as it was asked about. The publisher's face
// opens their name and email.
func TestShareDetail(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "shares"
	old := linkSecret
	linkSecret = func(string, float64) string { return "" }
	defer func() { linkSecret = old }()
	seedFetch(t, a, "members", `[{"id":"mem1","userId":"u1","role":"owner","user":{"id":"u1","name":"Ada Lovelace","email":"ada@example.com"}},
		{"id":"mem2","userId":"u2","role":"member","user":{"id":"u2","name":"Grace Hopper","email":"grace@example.com"}}]`, &[]cloud.Member{})
	a.fetches["w1|members"].value = *a.fetches["w1|members"].value.(*[]cloud.Member)
	const page = `{"page":[
		{"shareId":"sh1","rootSessionId":"s2","publisherUserId":"u1","status":"active","revision":4,"credentialVersion":2,"canManage":true,"canRevoke":true,"title":"Sidebar marks","createdAt":1798804800000,"updatedAt":1798891200000,"totalBytes":2048,"conversationCount":3},
		{"shareId":"sh2","rootSessionId":"gone","publisherUserId":"u2","status":"active","revision":1,"credentialVersion":1,"canManage":false,"canRevoke":true,"title":"","createdAt":1798804800000,"updatedAt":1798804800000,"totalBytes":512,"conversationCount":1},
		{"shareId":"sh3","rootSessionId":"gone","publisherUserId":"u2","status":"revoked","revision":2,"credentialVersion":1,"title":"Old notes","createdAt":1798804800000,"updatedAt":1798804800500,"totalBytes":0,"conversationCount":1}
	],"isDone":true,"continueCursor":""}`
	seedFetch(t, a, "shares", page, &sharesPage{})
	tt := snap(t, a, "settings-shares-rows", false, nil)
	// The list's rows: the revoked share hidden, the untitled one named.
	if !tt.HasText("Sidebar marks") || !tt.HasText(locales.UntitledSession()) || tt.HasText("Old notes") {
		t.Fatalf("rows: %q", tt.Texts())
	}

	// The publisher's profile, from their face.
	if err := tt.Click(locales.SharesPublisherProfile("Grace Hopper")); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("grace@example.com") || a.sharePages.detailOpen {
		t.Fatalf("profile: %q", tt.Texts())
	}
	snapFrame(t, tt, "settings-shares-profile")
	a.sharePages.profileOpen = false
	tt.Frame()

	openDetail(t, tt, "Sidebar marks")
	snapFrame(t, tt, "settings-shares-detail")
	want := []string{
		locales.SharesPublishedAt(), formatYMD(time.UnixMilli(1798804800000)),
		locales.SharesUpdatedAt(), formatYMD(time.UnixMilli(1798891200000)),
		locales.SharesConversationsLabel(), "3", locales.SharesSize(), "2.0 KB",
		locales.SharesStatusLabel(), locales.SharesActive(),
		// Its link was made on another device: reset it to copy one here.
		locales.SharesLinkOtherDevice(),
		locales.SharesOpenConversation(), locales.ShareUpdate(), locales.ShareReset(), locales.ShareRevoke(),
	}
	for _, w := range want {
		if !tt.HasText(w) {
			t.Fatalf("no %q in %q", w, tt.Texts())
		}
	}
	if tt.HasText(locales.SharesOpenPublished()) || tt.HasText(locales.ShareCopy()) {
		t.Fatalf("a link without its secret: %q", tt.Texts())
	}

	// Reset asks first, the dialog closing under the question; a share
	// that changed since is left alone.
	if err := tt.Click(locales.ShareReset()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.confirming.open || a.confirming.title != locales.ShareReset() || a.confirming.body != locales.ShareInvalidateNotice() || a.sharePages.detailOpen {
		t.Fatalf("reset confirmation: %+v", a.confirming)
	}
	a.fetches["w1|shares"].value.(*sharesPage).Page[0].Revision = 5
	a.convex = nil // nothing reaches the cloud
	a.confirming.run()
	if a.sharePages.op != nil {
		t.Fatalf("reset a share that changed: %+v", a.sharePages.op)
	}
	seedFetch(t, a, "shares", page, &sharesPage{})
	a.confirming.run()
	if op := a.sharePages.op; op == nil || op.entry.ShareID != "sh1" || op.entry.Revision != 4 {
		t.Fatalf("reset: %+v", op)
	}
	a.confirming.open, a.sharePages.op = false, nil
	tt.Frame()

	// Revoke asks the same.
	openDetail(t, tt, "Sidebar marks")
	if err := tt.Click(locales.ShareRevoke()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.confirming.open || a.confirming.title != locales.ShareRevoke() {
		t.Fatalf("revoke confirmation: %+v", a.confirming)
	}
	a.confirming.open = false
	tt.Frame()

	// Update share opens the conversation's share dialog over Settings.
	openDetail(t, tt, "Sidebar marks")
	if err := tt.Click(locales.ShareUpdate()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.share.open || a.share.job.root != "s2" || a.sharePages.detailOpen || !a.settingsOpen {
		t.Fatalf("update: %+v", a.share)
	}
	a.share.open = false
	tt.Frame()

	// Another member's share: only they hold its link, and its
	// conversation isn't here.
	openDetail(t, tt, locales.UntitledSession())
	if !tt.HasText(locales.SharesLinkOtherPublisher()) || tt.HasText(locales.SharesOpenConversation()) || tt.HasText(locales.ShareReset()) ||
		tt.HasText(locales.SharesUpdatedAt()) || !tt.HasText("512 B") || !tt.HasText(locales.ShareRevoke()) {
		t.Fatalf("another's share: %q", tt.Texts())
	}
	a.sharePages.detailOpen = false
	tt.Frame()

	// A revoked one has no live link.
	a.sharePages.showRevoked = true
	tt.Frame()
	openDetail(t, tt, "Old notes")
	if !tt.HasText(locales.SharesLinkRevoked()) || !tt.HasText(locales.SharesRevoked()) {
		t.Fatalf("revoked: %q", tt.Texts())
	}
	a.sharePages.detailOpen = false
	tt.Frame()

	// Open conversation closes Settings over it.
	openDetail(t, tt, "Sidebar marks")
	if err := tt.Click(locales.SharesOpenConversation()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settingsOpen || a.selected != "s2" {
		t.Fatalf("open conversation: settings %v, selected %q", a.settingsOpen, a.selected)
	}
}

// A share is placed in time coarser with age, as Lody's list places it,
// and sized as its details size it.
func TestShareTimesAndSizes(t *testing.T) {
	now := time.Date(2026, 10, 29, 15, 0, 0, 0, time.Local)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 10, 29, 9, 5, 0, 0, time.Local), "09:05"},
		{time.Date(2026, 3, 7, 23, 0, 0, 0, time.Local), "03-07"},
		{time.Date(2025, 12, 31, 12, 0, 0, 0, time.Local), "2025-12-31"},
	} {
		if got := sharePublishedAt(c.at, now); got != c.want {
			t.Errorf("sharePublishedAt(%v) = %q, want %q", c.at, got, c.want)
		}
	}
	for n, want := range map[float64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 2048: "2.0 KB", 15360: "15 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB", 4096 << 30: "4096 GB"} {
		if got := shareSize(n); got != want {
			t.Errorf("shareSize(%v) = %q, want %q", n, got, want)
		}
	}
}

// The usage donut shows what a range's tokens were by type, as Lody's,
// where the range reports the split, from its timeline or else from its
// summary; without it, the models, the four largest and the rest as
// Other. The slices of the new split sweep in.
func TestUsageBreakdownRing(t *testing.T) {
	day := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	tl := hourlyTimeline(day, 24)
	caption, segs := usageRings(tl, nil)
	if caption != locales.UsageByModel() || len(segs) != 3 {
		t.Fatalf("models: %q %+v", caption, segs)
	}
	many := hourlyTimeline(day, 24)
	for i := range many.Buckets {
		many.Buckets[i].ByModel[0].ModelID = []string{"a", "b (recommended)", "c", "d", "e", "f"}[i%6]
	}
	if _, segs := usageRings(many, nil); len(segs) != 5 || segs[4].label != locales.UsageOther() || strings.Contains(segs[0].label+segs[1].label+segs[2].label+segs[3].label, "recommended") {
		t.Fatalf("top four and Other: %+v", segs)
	}
	b := &usageBreakdown{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 30, CacheCreationInputTokens: 20}
	caption, segs = usageRings(tl, b)
	if caption != locales.UsageBreakdownTitle() || len(segs) != 3 || segs[0].key != "cache" || segs[0].share != 0.25 || segs[1].share != 0.5 {
		t.Fatalf("breakdown: %q %+v", caption, segs)
	}

	a := fixture()
	a.settingsOpen, a.settingsTab, a.usageRange = true, "ai-usage", "day"
	a.fetches = map[string]*fetched{"w1|usage/day": {value: tl}, "w1|usage/week": {value: hourlyTimeline(day.Add(-6*24*time.Hour), 7*24)}}
	tt := snap(t, a, "usage-by-model", false, nil)
	if !tt.HasText(strings.ToUpper(locales.UsageByModel())) {
		t.Fatalf("no model caption in %q", tt.Texts())
	}
	// The summary's split stands in for the timeline's.
	a.fetches["w1|usage-summary/day"] = &fetched{value: &usageSummary{Totals: usageTotals{Tokens: 200, Breakdown: b}}}
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "usage-breakdown-sweeping")
	tt.Advance(time.Second)
	tt.Frame()
	snapFrame(t, tt, "usage-breakdown")
	for _, want := range []string{strings.ToUpper(locales.UsageBreakdownTitle()), locales.UsageCache(), locales.UsageInput(), locales.UsageOutput(), "50%"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText(locales.UsageReasoning()) {
		t.Fatalf("an empty type has a slice: %q", tt.Texts())
	}

	// Where the timeline fails, the summary's totals still show.
	a.fetches["w1|usage/day"] = &fetched{err: "timeline unavailable"}
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText("timeline unavailable") || !tt.HasText(locales.UsageCostUSD()) {
		t.Fatalf("summary totals: %q", tt.Texts())
	}
}

// A session that isn't archived can be deleted from its menus, as in
// Lody's iOS app, after the same confirmation as an archived one; not
// while it runs.
func TestDeleteUnarchived(t *testing.T) {
	a := fixture()
	a.view.Working = false
	tt := snap(t, a, "header-menu-delete-running", false, nil)
	openMenu := func() {
		t.Helper()
		if err := tt.Click(locales.MoreActions()); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
	}
	// s1 runs: Delete session is there, but does nothing.
	openMenu()
	if !tt.HasText(locales.SessionsDeleteSession()) {
		t.Fatalf("no delete in %q", tt.Texts())
	}
	if err := tt.Click(locales.SessionsDeleteSession()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.deleteOpen {
		t.Fatal("a running session asked to be deleted")
	}
	a.hmenu = menuState{}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)

	a.open("s2")
	tt.Frame()
	openMenu()
	snapFrame(t, tt, "header-menu-delete")
	if err := tt.Click(locales.SessionsDeleteSession()); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !a.deleteOpen || a.deleting == nil || a.deleting.ID != "s2" || !tt.HasText(locales.ArchiveDeleteTitle()) {
		t.Fatalf("no confirmation: %v %v in %q", a.deleteOpen, a.deleting, tt.Texts())
	}

	// A running child tab blocks its parent too.
	a.catalog.Sessions = append(a.catalog.Sessions, a.catalog.Sessions[1])
	child := &a.catalog.Sessions[len(a.catalog.Sessions)-1]
	child.ID, child.ParentSessionID, child.Status = "s2c", "s2", "running"
	if !a.deletionBlocked(*a.catalog.Session("s2")) {
		t.Fatal("a running child tab doesn't block")
	}
	child.Archived = true
	if a.deletionBlocked(*a.catalog.Session("s2")) {
		t.Fatal("an archived child tab blocks")
	}
}
