package mention

import (
	"reflect"
	"testing"
)

func TestFiles(t *testing.T) {
	items := Files([]string{"src/app.go", "src/ui/view.go", "my notes.md", "../escape", "/abs"})
	var got []string
	for _, it := range items {
		got = append(got, it.Kind+" "+it.Path+" "+it.Insert)
	}
	want := []string{
		"directory src @src",
		"file src/app.go @src/app.go",
		"directory src/ui @src/ui",
		"file src/ui/view.go @src/ui/view.go",
		`file my notes.md @"my notes.md"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSkills(t *testing.T) {
	abs := "/Users/me/.claude/skills/review/SKILL.md"
	items := Skills([]SkillGroup{
		{Scope: "global", Dir: "~/.claude/skills", Skills: []SkillEntry{
			{Name: "review", Description: "Review a diff", RelativePath: "review/SKILL.md", AbsolutePath: &abs},
			{Name: "Deep Dive", RelativePath: "deep dive/SKILL.md"},
		}},
		{Scope: "project", Dir: ".claude/skills", Skills: []SkillEntry{
			{Name: "review", Description: "The project's own", RelativePath: ".claude/skills/review/SKILL.md"},
			{Name: "broken", RelativePath: ".claude/skills/broken/README.md"},
		}},
	})
	var got []string
	for _, it := range items {
		got = append(got, it.Insert+" "+it.Path+" "+it.Subtitle)
	}
	want := []string{
		"$review .claude/skills/review/SKILL.md The project's own",
		"$deep-dive deep dive/SKILL.md ~/.claude/skills",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestFind(t *testing.T) {
	cases := []struct {
		text  string
		caret int
		ok    bool
		want  Trigger
	}{
		{"@", 1, true, Trigger{0, '@', ""}},
		{"look at @src/a", 14, true, Trigger{8, '@', "src/a"}},
		{"mail@example", 12, false, Trigger{}},
		{"use $rev", 8, true, Trigger{4, '$', "rev"}},
		{"用 ￥审", 4, true, Trigger{2, '$', "审"}},
		{"/compact", 8, true, Trigger{0, '/', "compact"}},
		{"see /compact", 12, false, Trigger{}},
		{"@src now", 4, true, Trigger{0, '@', "src"}},
		{"@src now", 2, false, Trigger{}},
		{"@a b", 4, false, Trigger{}},
	}
	for _, c := range cases {
		got, ok := Find(c.text, c.caret)
		if ok != c.ok || got != c.want {
			t.Errorf("Find(%q, %d) = %+v, %v; want %+v, %v", c.text, c.caret, got, ok, c.want, c.ok)
		}
	}
}

func TestMatch(t *testing.T) {
	items := Files([]string{"internal/app/view.go", "docs/preview.md", "README.md"})
	var got []string
	for _, it := range Match(items, "view", 10) {
		got = append(got, it.Path)
	}
	want := []string{"internal/app/view.go", "docs/preview.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if n := len(Match(items, "", 2)); n != 2 {
		t.Fatalf("an empty query gave %d items, want the limit", n)
	}
}

func TestExpand(t *testing.T) {
	items := []Item{
		{Kind: Skill, Path: "/Users/me/skills/re)view/SKILL.md", Insert: "$review"},
		SessionItem("s1", "Earlier work", "lody"),
	}
	cases := map[string]string{
		"$review this":            "use /review [Skill Path](/Users/me/skills/re\\)view/SKILL.md) this",
		"see @session:s1 first":   "see use lody mcp to query session[id: s1] history first",
		"costs $5 and $HOME":      "costs $5 and $HOME",
		`open @"a $review b" now`: `open @"a $review b" now`,
		"x$review":                "x$review",
		"$review [Skill Path](p)": "$review [Skill Path](p)",
	}
	for in, want := range cases {
		if got := Expand(in, items); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
	if skills, sessions := Tokens("use $review on @session:s1"); !skills || !sessions {
		t.Fatalf("Tokens missed: %v %v", skills, sessions)
	}
}

func TestRoles(t *testing.T) {
	// Control characters go before whitespace collapses, as Lody's.
	if got := RoleSlug("  @Code  Reviewer\t2 "); got != "Code-Reviewer2" {
		t.Errorf("slug %q", got)
	}
	one := RoleItem("r1", "Reviewer", "🧐 Reviewer", "", "")
	two := RoleItem("r2", "Builder", "", "", "m2")
	got := Expand("ask "+one.Insert+" and "+two.Insert+" @role:Gone", []Item{one, two})
	want := "ask use lody mcp to create a session with agent role[id: r1, name: Reviewer] and use lody mcp to create a session with agent role[id: r2, agentRoleMachineId: m2, name: Builder] @role:Gone"
	if got != want {
		t.Errorf("expanded:\n%s\nwant:\n%s", got, want)
	}
}
