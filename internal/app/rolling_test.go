package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A figure rolls only the digits that changed, the old leaving as the new
// come in, and settles after 900ms; it does not roll as it first shows.
func TestRollingNumber(t *testing.T) {
	a := New()
	text, value := "128", 128.0
	tt := ui.NewTester(func(c *ui.Context) {
		a.rollingNumber(c, "n", text, value, func(e ui.Element) ui.Element { return e.FontSize(20) })
	}, 200, 80)
	// The characters drawn, without the row's label.
	chars := func() string {
		var out []string
		for _, s := range tt.Texts() {
			if len([]rune(s)) == 1 {
				out = append(out, s)
			}
		}
		return strings.Join(out, "")
	}
	tt.Frame()
	if got := chars(); got != "128" {
		t.Fatalf("first show rolled: %q", got)
	}
	text, value = "135", 135
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if got := chars(); strings.Count(got, "1") != 1 || !strings.ContainsAny(got, "28") || !strings.Contains(got, "3") || !strings.Contains(got, "5") {
		t.Fatalf("mid-roll %q: the unchanged 1 once, the old 2 and 8 leaving as 3 and 5 come", got)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if got := chars(); got != "135" {
		t.Fatalf("after 900ms %q", got)
	}
}
