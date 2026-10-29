package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/localterm"
	"github.com/lbr77/lody-mygo/internal/presence"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Local-only mode: the app syncs with this machine's daemon over its local
// data plane and asks it over its control socket, never the cloud, so it
// works offline — as Lody's desktop app does through its relay. It needs
// the account once, for the workspace; then the daemon is the only peer.

// dialLocalplane connects to the daemon's data plane; tests swap it.
var dialLocalplane = func(ctx context.Context, workspace string) (*localplane.Conn, error) {
	return localplane.Dial(ctx, localplane.SocketPath(), workspace)
}

// localRPC asks this machine's daemon; tests swap it.
var localRPC = localterm.MachineRPC

// localMachineID is this machine's id; tests swap it.
var localMachineID = localterm.MachineID

// localRedial is how long the app waits to dial the daemon again.
var localRedial = 3 * time.Second

// noCloud reports whether the app asks the cloud nothing: in tests, and
// in local-only mode.
func (a *App) noCloud() bool { return a.offline || a.localOnly }

// connectLocal follows workspace id through the daemon, redialing while it
// is away. The connection's state shows as the catalog's.
func (a *App) connectLocal(id string) {
	a.conn.Local = func(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
		return localRPC(ctx, id, method, params, timeout)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.stopLocal = cancel
	a.presence = presence.State{Live: true}
	go func() {
		for ctx.Err() == nil {
			dial, done := context.WithTimeout(ctx, 5*time.Second)
			lc, err := dialLocalplane(dial, id)
			done()
			if err != nil {
				a.update(func() {
					if ctx.Err() == nil {
						a.syncState = catalog.State{Reason: locales.LocalDaemonAway()}
					}
				})
			} else {
				a.update(func() {
					if ctx.Err() != nil {
						lc.Close()
						return
					}
					a.attachLocal(id, lc)
				})
				select {
				case <-ctx.Done():
					lc.Close()
					return
				case <-lc.Done():
				}
				a.update(func() {
					if a.local == lc {
						a.detachLocal()
					}
				})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(localRedial):
			}
		}
	}()
}

// attachLocal starts syncing over a fresh data plane connection.
func (a *App) attachLocal(id string, lc *localplane.Conn) {
	a.local = lc
	a.presence = presence.State{Live: true, Online: []string{localMachineID()}}
	a.startSync(id, lc)
	if a.selected != "" && a.replica == nil {
		a.openReplica(a.selected)
	}
}

// detachLocal stops what followed a connection that ended; what it showed
// stays until the daemon is back.
func (a *App) detachLocal() {
	a.local = nil
	if a.sync != nil {
		a.sync.Stop()
		a.sync = nil
	}
	if a.replica != nil {
		a.replica.Close()
		a.replica = nil
	}
	a.presence = presence.State{Live: true}
	a.syncState = catalog.State{Reason: locales.LocalDaemonAway()}
}

// openReplica follows a session: through the daemon in local-only mode,
// once connected, else through the cloud.
func (a *App) openReplica(id string) {
	if a.localOnly && a.local == nil {
		return
	}
	var r *session.Replica
	r = session.OpenLocal(a.conn, a.local, id, func(v *session.View) {
		a.update(func() {
			if a.replica == r {
				a.view = v
			}
		})
	})
	a.replica = r
}

// setLocalOnly turns local-only mode on or off and reconnects.
func (a *App) setLocalOnly(on bool) {
	if a.localOnly == on {
		return
	}
	a.localOnly = on
	a.savePrefs()
	if a.account != nil && a.workspaceID != "" {
		selected := a.selected
		a.connect(a.workspaceID)
		if selected != "" {
			a.show(selected)
		}
	}
}
