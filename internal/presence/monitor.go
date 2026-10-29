package presence

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	loro "github.com/aholstenson/loro-go"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// A machine's resource use comes over another ephemeral room of the meta
// stream, "machine-monitor": its Lody CLI samples itself, its agents'
// sessions and the device while someone observes it, and publishes each
// sample there. An observer is an entry of the room it renews.

// Resource is what a part of the machine uses: cpuCores is busy CPU
// seconds a second, so 1 is a whole core.
type Resource struct {
	MemoryBytes  *float64 `json:"memoryBytes"`
	CPUCores     *float64 `json:"cpuCores"`
	ProcessCount *float64 `json:"processCount"`
}

// Sample is a machine's resource use at a moment.
type Sample struct {
	MachineID            string   `json:"machineId"`
	UpdatedAtMs          float64  `json:"updatedAtMs"`
	Platform             string   `json:"platform"`
	CPULogicalCores      float64  `json:"cpuLogicalCores"`
	DeviceCPUCores       *float64 `json:"deviceCpuCores"`
	EffectiveMemoryBytes float64  `json:"effectiveMemoryBytes"`
	AvailableMemoryBytes float64  `json:"availableMemoryBytes"`
	CLI                  Resource `json:"cliControlPlane"`
	Sessions             Resource `json:"sessionsAggregate"`
}

const (
	observerTTL   = 30 * time.Second
	observerRenew = 15 * time.Second
)

// Monitor observes a machine until ctx ends, calling onSample with each
// newer sample, from its own goroutine.
func Monitor(ctx context.Context, conn *cloud.Conn, machineID string, onSample func(*Sample)) {
	observer := uuid.NewString()
	for ctx.Err() == nil {
		err := monitor(ctx, conn, machineID, observer, onSample)
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

func monitor(ctx context.Context, conn *cloud.Conn, machineID, observer string, onSample func(*Sample)) error {
	g, err := conn.Grant(ctx)
	if err != nil {
		return err
	}
	follow, err := roomURL(g, conn.ID, "machine-monitor", true)
	if err != nil {
		return err
	}
	post, err := roomURL(g, conn.ID, "machine-monitor", false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := conn.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, follow, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &streams.Error{Status: res.StatusCode, Code: "machine-monitor"}
	}
	encoding := strings.ToLower(strings.TrimSpace(res.Header.Get("Stream-SSE-Data-Encoding")))

	store := loro.NewEphemeralStore(observerTTL.Milliseconds() * 2)
	key := "observer:" + streams.EncodeURIComponent(machineID) + ":" + streams.EncodeURIComponent(observer)
	// Observe: the first time asking for a sample at once.
	observe := func(force bool) error {
		now := time.Now().UnixMilli()
		state := map[string]any{
			"kind": "observer", "protocolVersion": int64(1), "machineId": machineID, "observerId": observer,
			"updatedAtMs": float64(now), "expiresAtMs": float64(now + observerTTL.Milliseconds()), "forceSampleAtMs": nil,
		}
		if force {
			state["forceSampleAtMs"] = float64(now)
		}
		value, err := loro.AsValue(state)
		if err != nil {
			return err
		}
		store.Set(key, value)
		body := store.Encode(key)
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, post, bytes.NewReader(body))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Authorization", "Bearer "+g.Token)
		resp, err := client.Do(r)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			return &streams.Error{Status: resp.StatusCode, Code: "machine-monitor"}
		}
		return nil
	}
	if err := observe(true); err != nil {
		return err
	}
	renew := time.NewTicker(observerRenew)
	defer renew.Stop()

	var last float64
	emit := func() {
		if s := latestSample(store, machineID); s != nil && s.UpdatedAtMs > last {
			last = s.UpdatedAtMs
			onSample(s)
		}
	}
	events := make(chan sseEvent)
	errc := make(chan error, 1)
	go func() { errc <- readEvents(res.Body, events) }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			if err == nil {
				err = errors.New("machine-monitor: stream ended")
			}
			return err
		case <-renew.C:
			if err := observe(false); err != nil {
				return err
			}
		case ev := <-events:
			data := []byte(ev.data)
			if encoding == "base64" {
				var err error
				if data, err = base64.StdEncoding.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(ev.data)); err != nil {
					return err
				}
			}
			if len(data) > 0 {
				if err := store.Apply(data); err != nil {
					return err
				}
			}
			emit()
		}
	}
}

// latestSample is the newest sample of a machine in the room.
func latestSample(store *loro.EphemeralStore, machineID string) *Sample {
	var best *Sample
	for _, v := range store.GetAllStates() {
		raw, ok := loro.ValueAsAny(v)
		if !ok {
			continue
		}
		state, _ := raw.(map[string]any)
		if state["kind"] != "snapshot" || state["machineId"] != machineID {
			continue
		}
		b, err := json.Marshal(state)
		if err != nil {
			continue
		}
		var s Sample
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if best == nil || s.UpdatedAtMs > best.UpdatedAtMs {
			best = &s
		}
	}
	return best
}
