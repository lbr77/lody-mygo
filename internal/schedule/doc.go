package schedule

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"

	loro "github.com/aholstenson/loro-go"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/localplane"
	"github.com/lbr77/lody-mygo/internal/streams"
)

// Activity is an entry in a schedule's timeline.
type Activity struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"` // created, edited, paused or resumed
	ActorID            string `json:"actorId"`
	RequesterSessionID string `json:"requesterSessionId,omitempty"`
	CreatedAt          int64  `json:"createdAt"`
}

// Document is what a schedule's document holds.
type Document struct {
	Exists     bool
	Definition Definition
	Prompt     string
	Timeline   []Activity
}

// Doc is a schedule's document: caught up when opened, then written to.
// Over the data plane it takes in what the daemon pushes while open.
type Doc struct {
	ID string

	mu     sync.Mutex
	doc    *loro.LoroDoc
	client *streams.Client
	local  *localplane.Conn
	room   localplane.Room
}

// ErrNoSchedule is a schedule with no document.
var ErrNoSchedule = errors.New("schedule: no such schedule")

// DocRoom is a schedule's document's room on the data plane.
func DocRoom(id string) localplane.Room { return localplane.DocRoom("schedule-" + id) }

// OpenDoc catches up with a schedule's document, making its stream when
// create.
func OpenDoc(ctx context.Context, conn *cloud.Conn, local *localplane.Conn, id string, create bool) (*Doc, error) {
	d := &Doc{ID: id, doc: loro.NewLoroDoc(), local: local, room: DocRoom(id)}
	if local != nil {
		joined, err := local.Join(ctx, d.room, "", func(p localplane.Payload) {
			if data, err := p.Bytes(); err == nil {
				d.mu.Lock()
				d.doc.Import(data)
				d.mu.Unlock()
			}
		}, nil)
		if err != nil {
			return nil, err
		}
		if joined.Payload != nil {
			data, err := joined.Payload.Bytes()
			if err != nil {
				return nil, err
			}
			if _, err := d.doc.Import(data); err != nil {
				return nil, err
			}
		}
		return d, nil
	}
	client, err := conn.Stream(ctx, "schedule:"+id)
	if err != nil {
		return nil, err
	}
	if create {
		if err := client.Create(ctx, "application/octet-stream", 0); err != nil {
			return nil, err
		}
	}
	boot, err := client.Bootstrap(ctx)
	if streams.IsNotFound(err) {
		return nil, ErrNoSchedule
	}
	if err != nil {
		return nil, err
	}
	if boot.Snapshot != nil {
		snapshot, err := streams.Unpack(boot.Snapshot)
		if err != nil {
			return nil, err
		}
		if _, err := d.doc.Import(snapshot); err != nil {
			return nil, err
		}
	}
	for _, u := range boot.Updates {
		if err := d.importBody(u); err != nil {
			return nil, err
		}
	}
	offset, cursor, upToDate := boot.NextOffset, boot.Cursor, boot.UpToDate
	for pages := 0; !upToDate; pages++ {
		res, err := client.Read(ctx, offset, cursor, false)
		if err != nil {
			return nil, err
		}
		if err := d.importBody(res.Payload); err != nil {
			return nil, err
		}
		if res.NextOffset == offset && !res.UpToDate || pages > 100 {
			return nil, errors.New("schedule: stalled stream")
		}
		offset, cursor, upToDate = res.NextOffset, res.Cursor, res.UpToDate
	}
	d.client = client
	return d, nil
}

func (d *Doc) importBody(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	frames, err := streams.DecodeFrames(body)
	if err != nil {
		return err
	}
	for _, f := range frames {
		if _, err := d.doc.Import(f); err != nil {
			return err
		}
	}
	return nil
}

// Close stops taking in the daemon's updates.
func (d *Doc) Close() {
	if d.local != nil {
		d.local.Leave(d.room)
	}
}

// Read is the document as it stands.
func (d *Doc) Read() Document {
	d.mu.Lock()
	defer d.mu.Unlock()
	return read(d.doc)
}

func read(doc *loro.LoroDoc) Document {
	var out Document
	def, _ := loro.ValueAsAny(doc.GetMap(loro.AsContainerId("definition")).GetDeepValue())
	if m, ok := def.(map[string]any); ok && len(m) > 0 {
		b, _ := json.Marshal(m)
		json.Unmarshal(b, &out.Definition)
		out.Exists = out.Definition.ScheduleID != ""
	}
	out.Prompt = doc.GetText(loro.AsContainerId("prompt")).String()
	timeline, _ := loro.ValueAsAny(doc.GetList(loro.AsContainerId("timeline")).GetDeepValue())
	b, _ := json.Marshal(timeline)
	json.Unmarshal(b, &out.Timeline)
	return out
}

// Write changes the document and sends what changed.
func (d *Doc) Write(ctx context.Context, edit func(doc *loro.LoroDoc) error) error {
	d.mu.Lock()
	before := d.doc.OplogVv()
	if err := edit(d.doc); err != nil {
		d.mu.Unlock()
		return err
	}
	d.doc.Commit()
	update, err := d.doc.Export(loro.UpdatesMode(before))
	have := base64.StdEncoding.EncodeToString(d.doc.OplogVv().Encode())
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if d.local != nil {
		return d.local.Update(d.room, have, localplane.DocUpdate(update))
	}
	_, err = d.client.Append(ctx, "application/octet-stream", streams.EncodeFrame(update))
	return err
}

// writeDocument sets the definition and prompt, appending activity to the
// timeline when given.
func writeDocument(doc *loro.LoroDoc, def Definition, prompt string, activity *Activity) error {
	if err := setMap(doc.GetMap(loro.AsContainerId("definition")), plain(def).(map[string]any)); err != nil {
		return err
	}
	text := doc.GetText(loro.AsContainerId("prompt"))
	if text.String() != prompt {
		if err := text.Update(prompt, loro.UpdateOptions{}); err != nil {
			return err
		}
	}
	if activity != nil {
		timeline := doc.GetList(loro.AsContainerId("timeline"))
		entry, err := timeline.InsertMapContainer(timeline.Len(), loro.NewLoroMap())
		if err != nil {
			return err
		}
		if err := setMap(entry, plain(activity).(map[string]any)); err != nil {
			return err
		}
	}
	return nil
}

// setMap makes m hold value: objects as child maps, as loro-mirror writes
// them, numbers as doubles, and keys value lacks removed.
func setMap(m *loro.LoroMap, value map[string]any) error {
	for _, k := range m.Keys() {
		if _, ok := value[k]; !ok {
			if err := m.Delete(k); err != nil {
				return err
			}
		}
	}
	for k, v := range value {
		if obj, ok := v.(map[string]any); ok {
			child, isMap := m.GetLoroMap(k)
			if !isMap {
				var err error
				if child, err = m.InsertMapContainer(k, loro.NewLoroMap()); err != nil {
					return err
				}
			}
			if err := setMap(child, obj); err != nil {
				return err
			}
			continue
		}
		v = loroValue(v)
		if old, ok := m.GetAny(k); ok && equalValue(old, v) {
			continue
		}
		if err := m.InsertAny(k, v); err != nil {
			return err
		}
	}
	return nil
}

func loroValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = loroValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = loroValue(e)
		}
		return out
	}
	return v
}

func equalValue(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
