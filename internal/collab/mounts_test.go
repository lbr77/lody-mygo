package collab

import (
	"reflect"
	"testing"
)

// A member of a project of several used in place is a root of its own:
// its files' synced keys show at its directory, a root no longer mounted
// under a neutral name, and requests name the root and the path within
// it, a main path going as it always did.
func TestMounts(t *testing.T) {
	s := Session{ID: "s1", Mounts: []Mount{{Root: MemberRoot("docs1"), Path: "docs", LocalProject: "p2"}}}
	for key, want := range map[string]string{
		"src/main.go":                           "src/main.go",
		"\x00lody-root:member:docs1/guide.md":   "docs/guide.md",
		"\x00lody-root:member:gone/a/b.txt":     "member-gone/a/b.txt",
		"\x00lody-root:member:docs1/../escape":  "\x00lody-root:member:docs1/../escape",
		"\x00lody-root:main/x":                  "\x00lody-root:main/x",
		"\x00lody-root:member:__proto__/x":      "\x00lody-root:member:__proto__/x",
		"\x00lody-root:member:docs1/sub/dir.md": "docs/sub/dir.md",
	} {
		if got := s.Display(key); got != want {
			t.Errorf("Display(%q) = %q, want %q", key, got, want)
		}
	}
	for path, want := range map[string][2]string{
		"docs/guide.md":                       {"member:docs1", "guide.md"},
		"docs":                                {"", "docs"},
		`docs\sub\x.md`:                       {"member:docs1", "sub/x.md"},
		"./docs/a":                            {"member:docs1", "a"},
		"src/main.go":                         {"", "src/main.go"},
		"\x00lody-root:member:other/readme":   {"member:other", "readme"},
		"\x00lody-root:member:docs1/../../up": {"", "\x00lody-root:member:docs1/../../up"},
	} {
		if root, rel := s.request(path); root != want[0] || rel != want[1] {
			t.Errorf("request(%q) = %q, %q, want %q", path, root, rel, want)
		}
	}
	if got := params(s, map[string]any{"path": "docs/guide.md", "turnId": "t"}); !reflect.DeepEqual(got, map[string]any{"sessionId": "s1", "path": "guide.md", "rootId": "member:docs1", "turnId": "t"}) {
		t.Errorf("params %v", got)
	}
	if got := params(s, map[string]any{"path": "src/main.go"}); !reflect.DeepEqual(got, map[string]any{"sessionId": "s1", "path": "src/main.go"}) {
		t.Errorf("main params %v", got)
	}
	if m, rel := s.MountOf("docs/a/b"); m == nil || m.LocalProject != "p2" || rel != "a/b" {
		t.Errorf("MountOf %+v %q", m, rel)
	}
	if m, rel := s.MountOf("docs"); m == nil || rel != "" {
		t.Errorf("MountOf the mount %+v %q", m, rel)
	}
	if m, _ := s.MountOf("docsy/x"); m != nil {
		t.Errorf("MountOf a neighbour %+v", m)
	}
	// Without mounts, nothing changes.
	if root, rel := (Session{}).request("docs/guide.md"); root != "" || rel != "docs/guide.md" {
		t.Errorf("unmounted %q %q", root, rel)
	}
	if MemberRoot("bad id") != "" || MemberRoot("constructor") != "" || MemberRoot("a_1-b") != "member:a_1-b" {
		t.Error("MemberRoot")
	}
}
