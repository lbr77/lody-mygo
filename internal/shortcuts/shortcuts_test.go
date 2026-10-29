package shortcuts

import (
	"encoding/json"
	"testing"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/flock"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{"Review PR": "review-pr", "  --Fix: the BUG!! ": "fix-the-bug", "代码审查": "", "a_b c": "a-b-c"}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBodyAndIndex(t *testing.T) {
	sc := Shortcut{V: 1, ID: "s1", WorkspaceID: "w1", OwnerUserID: "u1", Visibility: "private", Name: "Review", Slug: "review",
		Prompt: "Review the diff.", Mentions: []any{}, Scope: map[string]any{}, Revision: "r1", CreatedAt: 1798804800000, UpdatedAt: 1798804800000}
	if err := sc.Validate(); err != nil {
		t.Fatal(err)
	}
	snap, err := bodySnapshot(sc)
	if err != nil {
		t.Fatal(err)
	}
	doc := loro.NewLoroDoc()
	if _, err := doc.Import(snap); err != nil {
		t.Fatal(err)
	}
	contents, _ := loro.ValueAsAny(doc.GetMap(loro.AsContainerId("contents")).GetDeepValue())
	b, _ := json.Marshal(contents)
	var all map[string]Shortcut
	if err := json.Unmarshal(b, &all); err != nil || all["r1"].Prompt != "Review the diff." || all["r1"].CreatedAt != 1798804800000 {
		t.Fatalf("body = %s, %v", b, err)
	}
	revisions, _ := loro.ValueAsAny(doc.GetMap(loro.AsContainerId("revisions")).GetDeepValue())
	if rb, _ := json.Marshal(revisions); string(rb) != `{"r1":{"parents":[]}}` {
		t.Fatalf("revisions = %s", rb)
	}

	f, err := flock.New("test")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Free()
	entry := sc.index("b1")
	eb, _ := json.Marshal(entry)
	var row map[string]any
	json.Unmarshal(eb, &row)
	if _, ok := row["prompt"]; ok {
		t.Fatal("the index holds the prompt")
	}
	f.Put([]any{"promptShortcut", "s1"}, map[string]any{"kind": "active", "entry": row})
	list, err := entries(f)
	if err != nil || len(list) != 1 || list[0].BodyDocID != "b1" || list[0].Slug != "review" {
		t.Fatalf("entries = %+v, %v", list, err)
	}
	f.Put([]any{"deletedPromptShortcut", "s1"}, "r1")
	if list, _ := entries(f); len(list) != 0 {
		t.Fatalf("a tombstone didn't win: %+v", list)
	}
}
