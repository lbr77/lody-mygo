package app

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// A chat's folder is where Lody puts it: its project's folder, its
// worktree under the machine's .lody, or a chat folder there.
func TestSessionWorkspacePath(t *testing.T) {
	a := fixture()
	a.catalog.DotLody = map[string]string{"m1": "/Users/ada/.lody", "m2": `C:\Users\ada\.lody`}
	sum := sha256.Sum256([]byte("/src/lody-mygo"))
	local := "local---" + hex.EncodeToString(sum[:])[:12]
	for _, tc := range []struct {
		s    catalog.Session
		want string
	}{
		{catalog.Session{ID: "s1", MachineID: "m1", ProjectID: "m1:local:p1"}, "/src/lody-mygo"},
		{catalog.Session{ID: "s1", MachineID: "m1", ProjectID: "m1:local:p1", IsWorktree: true}, "/Users/ada/.lody/repos/" + local + "/worktrees/s1"},
		{catalog.Session{ID: "c2", ParentSessionID: "s1", MachineID: "m1", ProjectID: "m1:local:p1", IsWorktree: true}, "/Users/ada/.lody/repos/" + local + "/worktrees/s1"},
		{catalog.Session{ID: "s9", MachineID: "m1", ProjectID: "github:Acme/App", IsWorktree: true}, "/Users/ada/.lody/repos/github---acme---app/worktrees/s9"},
		{catalog.Session{ID: "s9", MachineID: "m1", ProjectID: "m1:unassigned"}, "/Users/ada/.lody/chats/s9"},
		{catalog.Session{ID: "s9", MachineID: "m2", ProjectID: "m2:unassigned"}, `C:\Users\ada\.lody\chats\s9`},
		{catalog.Session{ID: "s9", MachineID: "m3", IsWorktree: true}, ""},
	} {
		if got := a.sessionWorkspacePath(&tc.s); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.s, got, tc.want)
		}
	}
}
