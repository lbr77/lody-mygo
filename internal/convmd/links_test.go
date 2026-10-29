package convmd

import (
	"strings"
	"testing"
)

// Lody's session-link.test.ts, as far as it covers export.

func TestExportSessionLinksProse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"See session://abc.", "See lody://session/abc?workspace=ws."},
		{"**session://abc**", "**lody://session/abc?workspace=ws**"},
		{"_session://abc_", "_lody://session/abc?workspace=ws_"},
		{"session://abc，后续", "lody://session/abc?workspace=ws，后续"},
		{"https://example.test/session://abc", "https://example.test/session://abc"},
		{"No links or only https://example.test", "No links or only https://example.test"},
	} {
		if got := exportSessionLinks(c.in, "ws"); got != c.want {
			t.Errorf("exportSessionLinks(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExportSessionLinksLeavesCode(t *testing.T) {
	source := strings.Join([]string{
		"[@Old](session://Child_A)",
		"[Other](lody-oss://session/B?workspace=foreign)",
		"`session://inline` and ``session://double``",
		"`multiline\nsession://example`",
		"> ```md\n> [Example](session://quoted)\n> ```",
		"<!-- session://comment -->",
		"[External](https://example.test/?next=session://keep)",
		"```md",
		"[Example](session://example)",
		"```",
		"    session://indented",
		"session://invalid/path",
	}, "\n")
	want := strings.Replace(strings.Replace(source, "session://Child_A", "lody://session/Child_A?workspace=ws_1", 1), "lody-oss://", "lody://", 1)
	if got := exportSessionLinks(source, "ws_1"); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := exportSessionLinks("[Old](session://A)", ""); got != "[Old](lody://session/A)" {
		t.Errorf("without a workspace: %q", got)
	}
}

func TestSessionLinkCanonical(t *testing.T) {
	canonical := "lody://session/Child_B?workspace=lw_One"
	if got := (sessionLink{session: "Root_A", workspace: "lw_One", tab: "Child_B"}).String(); got != canonical {
		t.Errorf("String = %q", got)
	}
	if got, ok := parseSessionLink(canonical); !ok || got != (sessionLink{session: "Child_B", workspace: "lw_One"}) {
		t.Errorf("parse canonical = %+v, %v", got, ok)
	}
	legacy := "lody://session/Root_A?workspace=lw_One&tab=Child_B"
	if got, ok := parseSessionLink(legacy); !ok || got != (sessionLink{session: "Root_A", workspace: "lw_One", tab: "Child_B"}) {
		t.Errorf("parse legacy = %+v, %v", got, ok)
	}
	if got := exportSessionLinks("[Conversation]("+legacy+")", ""); got != "[Conversation]("+canonical+")" {
		t.Errorf("export legacy = %q", got)
	}
	if got := (sessionLink{session: "Root_A", workspace: "lw_One"}).String(); got != "lody://session/Root_A?workspace=lw_One" {
		t.Errorf("String = %q", got)
	}
	for _, scheme := range []string{"lody", "lody-oss", "ai.lody.nightly"} {
		link, ok := parseSessionLink(scheme + "://session/Ab_c?workspace=ws_1")
		if !ok || link != (sessionLink{session: "Ab_c", workspace: "ws_1"}) || link.String() != "lody://session/Ab_c?workspace=ws_1" {
			t.Errorf("%s: %+v, %v", scheme, link, ok)
		}
	}
	if link, ok := parseSessionLink("session://Ab_c/"); !ok || link != (sessionLink{session: "Ab_c"}) {
		t.Errorf("legacy session:// = %+v, %v", link, ok)
	}
}

func TestParseSessionLinkRejects(t *testing.T) {
	for _, raw := range []string{
		"lody://session/a/../b",
		"lody://session/a%2fb",
		"lody://session/a%252fb",
		"lody://session/id?workspace=a&workspace=b",
		"lody://session/id?workspace=",
		"lody://session/id?workspace=a+space",
		"lody://session/id?command=delete",
		"lody://user@session/id",
		"lody://session:12/id",
		"lody://session/id#token=secret",
		"https://session/id",
		"lody://auth/callback",
		`lody://session/id\other`,
		"prefix lody://session/id",
		"lody://session/" + strings.Repeat("a", 8192),
	} {
		if link, ok := parseSessionLink(raw); ok {
			t.Errorf("parseSessionLink(%q) = %+v", raw, link)
		}
	}
}

// More of CommonMark than Lody's tests reach, where a link is or is not
// in code.
func TestExportSessionLinksMarkdown(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"- item session://a\n\n      session://b", "- item lody://session/a?workspace=w\n\n      session://b"},
		{"1. step\n\n    more session://a", "1. step\n\n    more lody://session/a?workspace=w"},
		{"> quote\nlazy session://a", "> quote\nlazy lody://session/a?workspace=w"},
		{"![shot](session://a) and [session://b](x)", "![shot](session://a) and [lody://session/b?workspace=w](x)"},
		{"<span>session://a</span> <session://b>", "<span>lody://session/a?workspace=w</span> <lody://session/b?workspace=w>"},
		{"<div>\nsession://a\n</div>\n\nsession://b", "<div>\nsession://a\n</div>\n\nlody://session/b?workspace=w"},
		{"<!-- a -->session://a", "<!-- a -->session://a"},
		{"~~~\nsession://a\n~~~\nsession://b", "~~~\nsession://a\n~~~\nlody://session/b?workspace=w"},
		{"# Title session://a #", "# Title lody://session/a?workspace=w #"},
		{"`unclosed session://a", "`unclosed lody://session/a?workspace=w"},
		{"snake_session://a", "snake_session://a"},
		{"__session://a__", "__lody://session/a?workspace=w__"},
		{"see session://a\\\nnext", "see lody://session/a?workspace=w\\\nnext"},
	} {
		if got := exportSessionLinks(c.in, "w"); got != c.want {
			t.Errorf("exportSessionLinks(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}
