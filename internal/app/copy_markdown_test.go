package app

import (
	"strings"
	"testing"
)

// Copy context from a reply copies the conversation up to and including
// it, as Lody's conversationCopyRange.
func TestCopyConversationThrough(t *testing.T) {
	a := fixture()
	var copied string
	old := writeClipboard
	writeClipboard = func(s string) { copied = s }
	defer func() { writeClipboard = old }()
	wait := pumpUpdates(t, a)
	a.copyConversationThrough("s1", "r1")
	wait("the copy", func() bool { return copied != "" })
	if !strings.Contains(copied, "Port the Flock catalog runtime") || strings.Contains(copied, "Now run it against the real stream.") {
		t.Fatalf("copied %q", copied)
	}
}
