// Package flocksync follows one Flock — over its cloud stream, or its room
// on this machine's data plane — and writes to it.
package flocksync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// Room is a Flock being followed. OnChange runs with its rows each time it
// catches up or changes, OnState when it goes live or fails; both on the
// room's goroutines.
type Room struct {
	Conn     *cloud.Conn
	Local    *localplane.Conn // when set, the data plane rather than the cloud
	Stream   string           // the cloud stream's name, as "sr"
	Plane    localplane.Room  // the data plane room
	OnChange func(rows []flock.Row)
	OnState  func(live bool, err error)

	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}
	once   sync.Once

	mu     sync.Mutex
	f      *flock.Flock
	client *streams.Client // nil until the stream exists
	joined bool            // in the data plane room
}

// Start follows the Flock until Stop.
func (r *Room) Start() {
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.ready = make(chan struct{})
	go r.run()
}

func (r *Room) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// WaitReady waits until the room first caught up.
func (r *Room) WaitReady(ctx context.Context) error {
	select {
	case <-r.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return errors.New("flocksync: stopped")
	}
}

// Rows are the Flock's rows as they stand.
func (r *Room) Rows() []flock.Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	rows, _ := r.f.Scan()
	return rows
}

func (r *Room) run() {
	backoff := time.Second
	for r.ctx.Err() == nil {
		var err error
		if r.Local != nil {
			err = r.followLocal()
		} else {
			err = r.follow()
		}
		if r.ctx.Err() != nil {
			return
		}
		if r.OnState != nil {
			r.OnState(false, err)
		}
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// caughtUp publishes the rows once the Flock is current.
func (r *Room) caughtUp() {
	r.once.Do(func() { close(r.ready) })
	if r.OnState != nil {
		r.OnState(true, nil)
	}
	r.changed()
}

func (r *Room) changed() {
	if r.OnChange != nil {
		r.OnChange(r.Rows())
	}
}

func newFlock() (*flock.Flock, error) { return flock.New("lody-mygo-" + uuid.NewString()) }

func (r *Room) follow() error {
	ctx := r.ctx
	client, err := r.Conn.Stream(ctx, r.Stream)
	if err != nil {
		return err
	}
	f, err := newFlock()
	if err != nil {
		return err
	}
	boot, err := client.Bootstrap(ctx)
	if streams.IsNotFound(err) {
		// Nothing written yet: an empty Flock, until a write makes the
		// stream or a later look finds it.
		r.mu.Lock()
		r.f, r.client = f, nil
		r.mu.Unlock()
		r.caughtUp()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			return nil
		}
	}
	if err != nil {
		return err
	}
	if boot.Snapshot != nil {
		snapshot, err := streams.Unpack(boot.Snapshot)
		if err != nil {
			return err
		}
		if err := f.ImportFile(snapshot); err != nil {
			return err
		}
	}
	for _, u := range boot.Updates {
		if err := apply(f, u); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.f, r.client = f, client
	r.mu.Unlock()
	offset, cursor, upToDate := boot.NextOffset, boot.Cursor, boot.UpToDate
	for {
		if upToDate {
			r.caughtUp()
		}
		res, err := client.Read(ctx, offset, cursor, upToDate)
		if err != nil {
			return err
		}
		if len(res.Payload) > 0 {
			r.mu.Lock()
			err := apply(f, res.Payload)
			r.mu.Unlock()
			if err != nil {
				return err
			}
			if upToDate {
				r.changed()
			}
		}
		if res.NextOffset == offset && !res.UpToDate {
			return errors.New("flocksync: stalled cursor")
		}
		offset, cursor, upToDate = res.NextOffset, res.Cursor, res.UpToDate
		if res.Closed {
			return errors.New("flocksync: stream closed")
		}
		if len(res.Payload) == 0 && upToDate {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func apply(f *flock.Flock, body []byte) error {
	frames, err := streams.DecodeFrames(body)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		var bundle map[string]any
		if err := json.Unmarshal(frame, &bundle); err != nil {
			return fmt.Errorf("flocksync: bad update: %w", err)
		}
		if err := f.ImportJSON(bundle); err != nil {
			return err
		}
	}
	return nil
}

func (r *Room) followLocal() error {
	ctx := r.ctx
	f, err := newFlock()
	if err != nil {
		return err
	}
	pushes := make(chan localplane.Payload, 256)
	joined, err := r.Local.Join(ctx, r.Plane, "", func(p localplane.Payload) {
		select {
		case pushes <- p:
		case <-ctx.Done():
		}
	}, nil)
	if err != nil {
		return err
	}
	defer r.Local.Leave(r.Plane)
	take := func(p *localplane.Payload) error {
		if p == nil || p.Kind != "flock-json" || len(p.Bundle) == 0 {
			return nil
		}
		var bundle map[string]any
		if err := json.Unmarshal(p.Bundle, &bundle); err != nil {
			return err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		return f.ImportJSON(bundle)
	}
	if err := take(joined.Payload); err != nil {
		return err
	}
	r.mu.Lock()
	r.f, r.joined = f, true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.joined = false
		r.mu.Unlock()
	}()
	r.caughtUp()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.Local.Done():
			return r.Local.Err()
		case p := <-pushes:
			if err := take(&p); err != nil {
				return err
			}
			r.changed()
		}
	}
}

// ErrNotReady is a write before the room caught up.
var ErrNotReady = errors.New("flocksync: not caught up")

// Write applies edit and sends what it changed.
func (r *Room) Write(ctx context.Context, edit func(f *flock.Flock) error) error {
	r.mu.Lock()
	f, client, joined := r.f, r.client, r.joined
	if f == nil {
		r.mu.Unlock()
		return ErrNotReady
	}
	before, err := f.Version()
	if err == nil {
		err = edit(f)
	}
	var delta map[string]any
	if err == nil {
		delta, err = f.ExportJSON(before)
	}
	after, verr := f.Version()
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if verr != nil {
		return verr
	}
	body, err := json.Marshal(delta)
	if err != nil {
		return err
	}
	switch {
	case r.Local != nil:
		if !joined {
			return ErrNotReady
		}
		err = r.Local.Update(r.Plane, PlaneVersion(after), localplane.Payload{Kind: "flock-json", Bundle: body})
	default:
		if client == nil {
			// The first write makes the stream.
			if client, err = r.Conn.Stream(ctx, r.Stream); err != nil {
				return err
			}
			if err = client.Create(ctx, "application/octet-stream", 0); err != nil {
				return err
			}
			r.mu.Lock()
			if r.f == f {
				r.client = client
			}
			r.mu.Unlock()
		}
		_, err = client.Append(ctx, "application/octet-stream", streams.EncodeFrame(body))
	}
	if err != nil {
		return err
	}
	r.changed()
	return nil
}

// PlaneVersion is a Flock version as the data plane spells it: each peer's
// {physicalTime, logicalCounter}, where the replica has [time, counter].
func PlaneVersion(v flock.Version) string {
	out := map[string]any{}
	for peer, at := range v {
		if pair, ok := at.([]any); ok && len(pair) == 2 {
			out[peer] = map[string]any{"physicalTime": pair[0], "logicalCounter": pair[1]}
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}
