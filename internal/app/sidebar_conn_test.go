package app

import (
	"testing"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// The sidebar's workspace badge says how the connection stands, as Lody's:
// connecting, reconnecting after a drop, then syncing; nothing once synced.
func TestConnectionBadge(t *testing.T) {
	a := fixture()
	a.sync = &catalog.Sync{}
	for _, tc := range []struct {
		state  catalog.State
		synced bool
		want   string
	}{
		{catalog.State{}, false, "Connecting…"},
		{catalog.State{Reason: "dial failed"}, false, "Reconnecting…"},
		{catalog.State{Live: true}, false, "Syncing workspace…"},
		{catalog.State{Live: true}, true, ""},
	} {
		a.syncState, a.synced = tc.state, tc.synced
		tt := snap(t, a, "conn-badge", false, nil)
		for _, label := range []string{"Connecting…", "Reconnecting…", "Syncing workspace…"} {
			if tt.HasText(label) != (label == tc.want) {
				t.Errorf("%+v synced %v: %q shown %v", tc.state, tc.synced, label, tt.HasText(label))
			}
		}
	}
}
