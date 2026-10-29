package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	loro "github.com/aholstenson/loro-go"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/internal/flocksync"
	"github.com/lbr77/lody-mygo/internal/localplane"
)

// Registry is the workspace's schedules as its Registry lists them.
type Registry struct {
	Rows    []Row               // live schedules, newest first
	Runtime map[string]*Runtime // what the machine reports of each, when it speaks of the row as it stands
}

// Row is a schedule by id.
func (r Registry) Row(id string) *Row {
	for i := range r.Rows {
		if r.Rows[i].ScheduleID == id {
			return &r.Rows[i]
		}
	}
	return nil
}

func decode(v any, out any) bool {
	b, err := json.Marshal(v)
	return err == nil && json.Unmarshal(b, out) == nil
}

// RegistryOf reads the Registry's rows: a schedule's row unless tombstoned,
// and its machine's runtime row.
func RegistryOf(rows []flock.Row) Registry {
	byID := map[string]Row{}
	deleted := map[string]bool{}
	var runtimes []Runtime
	for _, r := range rows {
		if !r.Live || len(r.Key) < 2 {
			continue
		}
		kind, _ := r.Key[0].(string)
		id, _ := r.Key[1].(string)
		switch {
		case kind == "tombstone" && len(r.Key) == 2:
			var t struct {
				ActorID string `json:"actorId"`
			}
			if decode(r.Value, &t) && t.ActorID != "" {
				deleted[id] = true
			}
		case kind == "schedule" && len(r.Key) == 2:
			var row Row
			if decode(r.Value, &row) && row.ScheduleID == id {
				byID[id] = row
			}
		case kind == "runtime" && len(r.Key) == 3:
			var rt Runtime
			if decode(r.Value, &rt) && rt.ScheduleID == id {
				runtimes = append(runtimes, rt)
			}
		}
	}
	out := Registry{Runtime: map[string]*Runtime{}}
	for id, row := range byID {
		if !deleted[id] {
			out.Rows = append(out.Rows, row)
		}
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt > b.CreatedAt
		}
		return a.ScheduleID < b.ScheduleID
	})
	for i := range runtimes {
		rt := runtimes[i]
		if row, ok := byID[rt.ScheduleID]; ok && rt.Matches(row) {
			out.Runtime[rt.ScheduleID] = &rt
		}
	}
	return out
}

// Store follows a workspace's Schedule Registry and writes schedules as
// Lody's repository does: the document first, then the Registry row.
type Store struct {
	Conn      *cloud.Conn
	Local     *localplane.Conn
	Workspace string
	OnChange  func(Registry)
	OnState   func(live bool, err error)

	room *flocksync.Room
}

// Start follows the Registry until Stop.
func (s *Store) Start() {
	s.room = &flocksync.Room{Conn: s.Conn, Local: s.Local, Stream: "sr", Plane: localplane.FlockRoom(s.Workspace + ":sr"), OnState: s.OnState}
	s.room.OnChange = func(rows []flock.Row) {
		if s.OnChange != nil {
			s.OnChange(RegistryOf(rows))
		}
	}
	s.room.Start()
}

func (s *Store) Stop() {
	if s.room != nil {
		s.room.Stop()
	}
}

// Registry is the Registry as it stands.
func (s *Store) Registry() Registry { return RegistryOf(s.room.Rows()) }

// Open catches up with a schedule's document.
func (s *Store) Open(ctx context.Context, id string) (*Doc, error) {
	return OpenDoc(ctx, s.Conn, s.Local, id, false)
}

// Draft is what the editor saves.
type Draft struct {
	Title, Prompt string
	MachineID     string
	Trigger       Trigger
	Agent         Agent
	Project       *Project
	Destination   Destination
}

// Errors a write can fail with.
var (
	ErrNotOwner   = errors.New("schedule: only the schedule owner can change it")
	ErrDeleted    = errors.New("schedule: schedule was deleted")
	ErrTitle      = errors.New("schedule: name the schedule")
	ErrPrompt     = errors.New("schedule: the prompt must have text and be at most 32 KiB")
	ErrNoAgent    = errors.New("schedule: choose an Agent")
	errNotCreated = errors.New("schedule: not created")
)

// MaxPrompt is a prompt's limit, in UTF-8 bytes.
const MaxPrompt = 32 << 10

func ms(t time.Time) int64 { return t.UnixMilli() }

// Validate checks a draft as Lody does before it is saved.
func (d Draft) Validate() error {
	if t := strings.TrimSpace(d.Title); t == "" || utf8.RuneCountInString(t) > 200 {
		return ErrTitle
	}
	if strings.TrimSpace(d.Prompt) == "" || len(d.Prompt) > MaxPrompt {
		return ErrPrompt
	}
	if d.MachineID == "" || d.Agent.AgentConfigID == "" {
		return ErrNoAgent
	}
	if d.Trigger.Kind == "cron" {
		if _, err := ParseCron(d.Trigger.Expression); err != nil {
			return err
		}
	}
	return nil
}

// Save creates schedule id, or edits it, as actor.
func (s *Store) Save(ctx context.Context, id string, create bool, d Draft, actor string, now time.Time) error {
	if err := d.Validate(); err != nil {
		return err
	}
	doc, err := OpenDoc(ctx, s.Conn, s.Local, id, create)
	if err != nil {
		return err
	}
	defer doc.Close()
	prev := doc.Read()
	if !prev.Exists && !create {
		return ErrNoSchedule
	}
	if prev.Exists && prev.Definition.OwnerID != actor {
		return ErrNotOwner
	}
	if d.Destination.Kind == "" {
		d.Destination = Destination{Kind: NewSession}
	}
	if d.Destination.Kind != NewSession {
		d.Project = nil // a project only for a new chat each run
	}
	def := Definition{
		ScheduleID: id, Title: strings.TrimSpace(d.Title), OwnerID: actor, MachineID: d.MachineID, Enabled: true,
		ActivationID: uuid.NewString(), ActiveFrom: ms(now), Trigger: d.Trigger,
		MisfirePolicy: Policy{Kind: "run_once"}, OverlapPolicy: "queue_one", Agent: d.Agent, Project: d.Project,
		Destination: d.Destination, RetryPolicy: DefaultRetry, CreatedAt: ms(now), UpdatedAt: ms(now), CreatedBy: actor,
	}
	kind := "created"
	if prev.Exists {
		p := prev.Definition
		def.Enabled, def.CreatedAt, def.CreatedBy = p.Enabled, p.CreatedAt, p.CreatedBy
		def.MisfirePolicy, def.OverlapPolicy, def.RetryPolicy = p.MisfirePolicy, p.OverlapPolicy, p.RetryPolicy
		kind = "edited"
	}
	activity := &Activity{ID: uuid.NewString(), Kind: kind, ActorID: actor, CreatedAt: ms(now)}
	return s.commit(ctx, doc, def, d.Prompt, nil, activity)
}

// commit writes the document, then the Registry row that lets it run.
func (s *Store) commit(ctx context.Context, doc *Doc, def Definition, prompt string, message map[string]any, activity *Activity) error {
	if err := doc.Write(ctx, func(ld *loro.LoroDoc) error { return writeDocument(ld, def, prompt, message, activity) }); err != nil {
		return err
	}
	row := rowOf(def, prompt, message)
	return s.room.Write(ctx, func(f *flock.Flock) error {
		if tombstoned(f, def.ScheduleID) {
			return ErrDeleted
		}
		return f.Put([]any{"schedule", def.ScheduleID}, loroValue(plain(row)))
	})
}

func tombstoned(f *flock.Flock, id string) bool {
	rows, _ := f.Scan()
	for _, r := range rows {
		if r.Live && len(r.Key) == 2 && r.Key[0] == "tombstone" && r.Key[1] == id {
			return true
		}
	}
	return false
}

// owned opens a schedule its owner is changing.
func (s *Store) owned(ctx context.Context, id, actor string) (*Doc, Document, error) {
	row := s.Registry().Row(id)
	if row == nil {
		return nil, Document{}, ErrNoSchedule
	}
	if row.OwnerID != actor {
		return nil, Document{}, ErrNotOwner
	}
	doc, err := OpenDoc(ctx, s.Conn, s.Local, id, false)
	if err != nil {
		return nil, Document{}, err
	}
	cur := doc.Read()
	if !cur.Exists {
		doc.Close()
		return nil, Document{}, errNotCreated
	}
	if cur.Definition.OwnerID != actor {
		doc.Close()
		return nil, Document{}, ErrNotOwner
	}
	return doc, cur, nil
}

// SetEnabled pauses or resumes a schedule; resuming starts a new
// activation, so slots missed while paused don't run.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool, actor string, now time.Time) error {
	doc, cur, err := s.owned(ctx, id, actor)
	if err != nil {
		return err
	}
	defer doc.Close()
	def := cur.Definition
	if def.Enabled == enabled {
		return nil
	}
	def.Enabled, def.UpdatedAt = enabled, ms(now)
	kind := "paused"
	if enabled {
		def.ActivationID, def.ActiveFrom, kind = uuid.NewString(), ms(now), "resumed"
	}
	return s.commit(ctx, doc, def, cur.Prompt, cur.Message, &Activity{ID: uuid.NewString(), Kind: kind, ActorID: actor, CreatedAt: ms(now)})
}

// MessageDraft is a message to send into a chat once, later.
type MessageDraft struct {
	SessionID, MachineID string
	Agent                Agent
	Payload              map[string]any // inputBlocks, and mcpServerIds when chosen
	At                   time.Time
}

// ErrTimePassed is a scheduled message's time that has come.
var ErrTimePassed = errors.New("schedule: choose a time in the future")

// MessagePrompt is a message's prompt: its text blocks, a line each.
func MessagePrompt(payload map[string]any) string {
	var lines []string
	blocks, _ := plain(payload["inputBlocks"]).([]any)
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok && m["type"] == "text" {
			text, _ := m["text"].(string)
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

// SaveMessage schedules a message into a chat, as the composer's Schedule
// send does: once, to that chat, the title its first line.
func (s *Store) SaveMessage(ctx context.Context, id string, d MessageDraft, actor, fallbackTitle string, now time.Time) error {
	if !d.At.After(now) {
		return ErrTimePassed
	}
	prompt := MessagePrompt(d.Payload)
	title := strings.TrimSpace(prompt)
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = title[:i]
	}
	if r := []rune(title); len(r) > 200 {
		title = string(r[:200])
	}
	title = firstNonEmpty(title, fallbackTitle)
	doc, err := OpenDoc(ctx, s.Conn, s.Local, id, true)
	if err != nil {
		return err
	}
	defer doc.Close()
	def := Definition{
		ScheduleID: id, MessageVersion: 1, Title: title, OwnerID: actor, MachineID: d.MachineID, Enabled: true,
		ActivationID: uuid.NewString(), ActiveFrom: ms(d.At), Trigger: Trigger{Kind: "once", At: ISO(d.At)},
		MisfirePolicy: Policy{Kind: "run_once"}, OverlapPolicy: "queue_one", Agent: d.Agent,
		Destination: Destination{Kind: ExistingSession, SessionID: d.SessionID}, RetryPolicy: DefaultRetry,
		CreatedAt: ms(now), UpdatedAt: ms(now), CreatedBy: actor,
	}
	payload := plain(d.Payload).(map[string]any)
	return s.commit(ctx, doc, def, prompt, loroValue(payload).(map[string]any), &Activity{ID: uuid.NewString(), Kind: "created", ActorID: actor, CreatedAt: ms(now)})
}

// Reschedule moves a scheduled message to another time.
func (s *Store) Reschedule(ctx context.Context, id string, at time.Time, actor string, now time.Time) error {
	if !at.After(now) {
		return ErrTimePassed
	}
	doc, cur, err := s.owned(ctx, id, actor)
	if err != nil {
		return err
	}
	defer doc.Close()
	def := cur.Definition
	def.Trigger = Trigger{Kind: "once", At: ISO(at)}
	def.ActivationID, def.ActiveFrom, def.UpdatedAt, def.Enabled = uuid.NewString(), ms(at), ms(now), true
	return s.commit(ctx, doc, def, cur.Prompt, cur.Message, &Activity{ID: uuid.NewString(), Kind: "edited", ActorID: actor, CreatedAt: ms(now)})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// RunNow asks the schedule's machine for a run with what was last saved.
func (s *Store) RunNow(ctx context.Context, id, actor string, now time.Time) error {
	row := s.Registry().Row(id)
	if row == nil {
		return ErrNoSchedule
	}
	if row.OwnerID != actor {
		return ErrNotOwner
	}
	run := uuid.NewString()
	return s.room.Write(ctx, func(f *flock.Flock) error {
		return f.Put([]any{"manual", id, run}, map[string]any{
			"scheduleId": id, "actorId": actor, "manualRunId": run, "activationId": row.ActivationID, "requestedAt": float64(ms(now)),
		})
	})
}

// Delete tombstones a schedule: it runs no more; its document, row and
// the chats it started stay.
func (s *Store) Delete(ctx context.Context, id, actor string, now time.Time) error {
	return s.room.Write(ctx, func(f *flock.Flock) error {
		rows, _ := f.Scan()
		var row Row
		found := false
		for _, r := range rows {
			if !r.Live || len(r.Key) != 2 || r.Key[1] != id {
				continue
			}
			switch r.Key[0] {
			case "tombstone":
				var t struct {
					ActorID string `json:"actorId"`
				}
				if decode(r.Value, &t) && t.ActorID == actor {
					return nil
				}
				return ErrDeleted
			case "schedule":
				found = decode(r.Value, &row)
			}
		}
		if !found {
			return ErrNoSchedule
		}
		if row.OwnerID != actor {
			return ErrNotOwner
		}
		return f.Put([]any{"tombstone", id}, map[string]any{"deletedAt": float64(ms(now)), "actorId": actor})
	})
}
