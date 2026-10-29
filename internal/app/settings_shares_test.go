package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
// published page in the browser.
func TestSharesPaging(t *testing.T) {
	a := fixture()
	a.settingsOpen, a.settingsTab = true, "shares"
	secret := strings.Repeat("ab", 32)
	old := linkSecret
	linkSecret = func(string, float64) string { return secret }
	defer func() { linkSecret = old }()
	var opened string
	oldOpen := openExternal
	openExternal = func(u string) { opened = u }
	defer func() { openExternal = oldOpen }()

	seedFetch(t, a, "shares", `{"page":[{"shareId":"sh1","rootSessionId":"s1","publisherUserId":"u1","status":"active","revision":1,"credentialVersion":1,"canManage":true,"canRevoke":true,"title":"Flock port","createdAt":1798761600000,"totalBytes":2048,"conversationCount":1}],"isDone":false,"continueCursor":"c1"}`, &sharesPage{})
	tt := snap(t, a, "settings-shares-paged", false, nil)
	for _, want := range []string{"Flock port", locales.SharesPrevious(), locales.SharesNext(), locales.ShareCopy()} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click(locales.SharesOpenPublished()); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	if opened != "https://share.lody.ai/s/sh1#access=v1."+secret {
		t.Fatalf("opened %q", opened)
	}
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
	if !tt.HasText("Sidebar marks") || tt.HasText("Flock port") || !tt.HasText(locales.SharesPrevious()) {
		t.Fatalf("second page: %q", tt.Texts())
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
	if a.sharePages.cursor() != "" || a.sharePages.workspace != "w2" {
		t.Fatalf("paging kept across workspaces: %+v", a.sharePages)
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
