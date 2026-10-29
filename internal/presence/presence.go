// Package presence follows which machines of a workspace are online: the
// "presence" ephemeral channel of the workspace's meta stream, a Loro
// EphemeralStore delivered over server-sent events.
package presence

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// TTL is how long a machine's last heartbeat keeps it online.
const TTL = 90 * time.Second

// State is what is known of the machines' presence.
type State struct {
	Live   bool     // following the channel; otherwise presence is unknown
	Online []string // machine ids, sorted
}

// IsOnline reports whether a machine is online. Unknown counts as online,
// so nothing is disabled while presence connects.
func (s State) IsOnline(machineID string) bool {
	if !s.Live {
		return true
	}
	for _, id := range s.Online {
		if id == machineID {
			return true
		}
	}
	return false
}

var hostSuffix = regexp.MustCompile(`^[a-z0-9.-]+$`)

func channelURL(g *cloud.Grant, workspace string) (string, error) {
	return roomURL(g, workspace, "presence", true)
}

// roomURL is the URL of an ephemeral room of the workspace's meta stream,
// on the presence host: to follow it, or, without live, to post to it.
func roomURL(g *cloud.Grant, workspace, room string, live bool) (string, error) {
	base, err := url.Parse(strings.TrimSuffix(g.GatewayBaseURL, "/"))
	if err != nil {
		return "", err
	}
	if g.ShardHostSuffix != "" && hostSuffix.MatchString(strings.ToLower(g.ShardHostSuffix)) {
		base.Scheme, base.Host = "https", "presence."+g.ShardHostSuffix
	}
	u := strings.TrimSuffix(base.String(), "/") + "/ds/lody/" + streams.EncodeURIComponent(workspace+":meta") + "?ephemeral=" + room
	if live {
		u += "&live=sse"
	}
	return u, nil
}

// Watch follows presence until ctx ends, calling onChange with each new
// state, from its own goroutine.
func Watch(ctx context.Context, conn *cloud.Conn, onChange func(State)) {
	last := ""
	emit := func(s State) {
		key := strings.Join(s.Online, ",")
		if s.Live {
			key = "live:" + key
		}
		if key != last {
			last = key
			onChange(s)
		}
	}
	for ctx.Err() == nil {
		err := follow(ctx, conn, emit)
		emit(State{})
		if ctx.Err() != nil {
			return
		}
		if streams.IsUnauthorized(err) {
			conn.Invalidate()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func follow(ctx context.Context, conn *cloud.Conn, emit func(State)) error {
	g, err := conn.Grant(ctx)
	if err != nil {
		return err
	}
	target, err := channelURL(g, conn.ID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	client := conn.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &streams.Error{Status: res.StatusCode, Code: "presence"}
	}
	if !strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("presence: not an event stream")
	}
	encoding := strings.ToLower(strings.TrimSpace(res.Header.Get("Stream-SSE-Data-Encoding")))

	store := loro.NewEphemeralStore(TTL.Milliseconds())
	joined := false
	snapshot := func() {
		if joined {
			emit(State{Live: true, Online: online(store, time.Now())})
		}
	}
	// Heartbeats expire without new events: re-read every few seconds.
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				snapshot()
			}
		}
	}()

	events := make(chan sseEvent)
	errc := make(chan error, 1)
	go func() { errc <- readEvents(res.Body, events) }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			if err == nil {
				err = errors.New("presence: stream ended")
			}
			return err
		case ev := <-events:
			if !joined && ev.event != "bootstrap" {
				return errors.New("presence: stream must start with bootstrap")
			}
			data := []byte(ev.data)
			if encoding == "base64" {
				if data, err = base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(ev.data)); err != nil {
					return err
				}
			}
			if len(data) > 0 {
				if err := store.Apply(data); err != nil {
					return err
				}
			}
			joined = true
			snapshot()
		}
	}
}

type sseEvent struct{ event, data string }

// readEvents parses server-sent events until the body ends.
func readEvents(body interface{ Read([]byte) (int, error) }, out chan<- sseEvent) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	event, data := "", []string(nil)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if len(data) > 0 {
				if event == "" {
					event = "message"
				}
				out <- sseEvent{event: event, data: strings.Join(data, "\n")}
			}
			event, data = "", nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	return scanner.Err()
}

// online lists the machines whose heartbeat is recent.
func online(store *loro.EphemeralStore, now time.Time) []string {
	ids := map[string]bool{}
	for _, v := range store.GetAllStates() {
		raw, ok := loro.ValueAsAny(v)
		if !ok {
			continue
		}
		state, _ := raw.(map[string]any)
		if state["kind"] != "machine" {
			continue
		}
		machine, _ := state["machineId"].(string)
		instance, _ := state["instanceId"].(string)
		var updated float64
		switch u := state["updatedAt"].(type) {
		case float64:
			updated = u
		case int64:
			updated = float64(u)
		}
		if machine == "" || instance == "" || updated == 0 {
			continue
		}
		if now.Sub(time.UnixMilli(int64(updated))) < TTL {
			ids[machine] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
