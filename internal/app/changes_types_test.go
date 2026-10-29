package app

import (
	"testing"
	"time"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/collab"
)

// Changed files sort as Lody's: only a strong signal takes a file out of
// code.
func TestClassifyChange(t *testing.T) {
	for file, want := range map[string]string{
		"internal/app/chat.go":           "code",
		"internal/app/chat_test.go":      "test",
		"src/__tests__/view.tsx":         "test",
		"src/view.spec.ts":               "test",
		"README.md":                      "doc",
		"docs/guide/intro.txt":           "doc",
		"LICENSE":                        "doc",
		".github/workflows/release.yml":  "dev",
		"package-lock.json":              "dev",
		"vite.config.ts":                 "dev",
		"tsconfig.app.json":              "dev",
		".env.local":                     "dev",
		"src/types.d.ts":                 "code",
		"cmd/lody/main.go":               "code",
		"packages/ui/src/test-utils.tsx": "test",
		"./src/app.ts":                   "code",
	} {
		if got := classifyChange(file); got != want {
			t.Errorf("%s: %s, want %s", file, got, want)
		}
	}
}

// The Changes panel folds its files by type, as Lody's, and Files lays
// them out in their folders.
func TestChangesPanelViews(t *testing.T) {
	a := fixture()
	a.view.Entries = a.view.Entries[:2]
	a.view.Working = false
	a.conn = &cloud.Conn{ID: "w1"}
	a.panelOpen = true
	one := 1
	in := a.inspectFor("s1")
	in.changes[collab.ScopeBranch] = &changesState{res: &collab.Changes{Status: "ok", Entries: []collab.Change{
		{Status: "ok", Path: "internal/app/view.go", Add: &one, Del: &one},
		{Status: "ok", Path: "internal/app/view_test.go", Add: &one, Del: &one},
		{Status: "ok", Path: "README.md", Add: &one, Del: &one},
	}}}
	in.openPanel(tabChanges)
	tt := snap(t, a, "changes-types", false, nil)
	for _, want := range []string{"Types", "Files", "Code", "Tests", "Docs", "view.go", "view_test.go", "README.md"} {
		if !tt.HasText(want) {
			t.Errorf("types view misses %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "changes-files")
	for _, want := range []string{"internal", "app", "view.go", "README.md"} {
		if !tt.HasText(want) {
			t.Errorf("files view misses %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Tests") {
		t.Error("files view still folds by type")
	}
}
