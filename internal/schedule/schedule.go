// Package schedule is Lody's scheduled tasks: a schedule's definition, kept
// in its own Loro document, and the workspace's Schedule Registry Flock that
// gates what a machine runs. A client writes the document first, then the
// registry row carrying the definition's fingerprint; the machine runs a row
// only once its copy of the document has that fingerprint.
package schedule

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Trigger is when a schedule runs.
type Trigger struct {
	Kind       string `json:"kind"`                 // manual, once, interval or cron
	At         string `json:"at,omitempty"`         // once: an ISO instant
	EveryMs    int64  `json:"everyMs,omitempty"`    // interval
	AnchorAt   string `json:"anchorAt,omitempty"`   // interval: an ISO instant
	Expression string `json:"expression,omitempty"` // cron: five fields
	TimeZone   string `json:"timeZone,omitempty"`   // cron: an IANA zone
}

// Agent is who runs a schedule, and how.
type Agent struct {
	AgentConfigID      string         `json:"agentConfigId"`
	ModeID             string         `json:"modeId,omitempty"`
	ModelID            string         `json:"modelId,omitempty"`
	ConfigOptionValues map[string]any `json:"configOptionValues,omitempty"` // string or bool values
	Memory             *Memory        `json:"memory,omitempty"`
}

// Memory is the memory a run starts with.
type Memory struct {
	ProviderID string `json:"providerId"`
	MemoryID   string `json:"memoryId"`
}

// Project is where a run works: a GitHub repo's branch, or a local project.
type Project struct {
	Kind               string `json:"kind"` // github or local
	RepoFullName       string `json:"repoFullName,omitempty"`
	Branch             string `json:"branch,omitempty"`
	LocalProjectID     string `json:"localProjectId,omitempty"`
	GitHubRepoFullName string `json:"githubRepoFullName,omitempty"`
	UseWorktree        *bool  `json:"useWorktree,omitempty"`
}

// Destination is where a run's results go.
type Destination struct {
	Kind      string `json:"kind"`                // new_session, own_session or existing_session
	Epoch     *int   `json:"epoch,omitempty"`     // own_session: bumped to start a new chat
	SessionID string `json:"sessionId,omitempty"` // existing_session
}

// Destination kinds.
const (
	NewSession      = "new_session"
	OwnSession      = "own_session"
	ExistingSession = "existing_session"
)

// Policy is a misfire policy: skip, or run_once.
type Policy struct {
	Kind string `json:"kind"`
}

// RetryPolicy bounds a run's dispatch.
type RetryPolicy struct {
	DispatchMaxAttempts int   `json:"dispatchMaxAttempts"`
	DispatchMaxAgeMs    int64 `json:"dispatchMaxAgeMs"`
}

// DefaultRetry is the policy Lody's editor always writes.
var DefaultRetry = RetryPolicy{DispatchMaxAttempts: 5, DispatchMaxAgeMs: 86_400_000}

// Definition is a schedule's definition.
type Definition struct {
	ScheduleID     string      `json:"scheduleId"`
	MessageVersion int         `json:"messageVersion,omitempty"` // 1 for a scheduled message
	Title          string      `json:"title"`
	OwnerID        string      `json:"ownerId"`
	MachineID      string      `json:"machineId"`
	Enabled        bool        `json:"enabled"`
	ActivationID   string      `json:"activationId"`
	ActiveFrom     int64       `json:"activeFrom"`
	Trigger        Trigger     `json:"trigger"`
	MisfirePolicy  Policy      `json:"misfirePolicy"`
	OverlapPolicy  string      `json:"overlapPolicy"` // skip or queue_one
	Agent          Agent       `json:"agent"`
	Project        *Project    `json:"project,omitempty"`
	Destination    Destination `json:"destination"`
	RetryPolicy    RetryPolicy `json:"retryPolicy"`
	CreatedAt      int64       `json:"createdAt"`
	UpdatedAt      int64       `json:"updatedAt"`
	CreatedBy      string      `json:"createdBy"`
}

// Row is a schedule's row in the registry, which a machine runs from.
type Row struct {
	ScheduleID            string      `json:"scheduleId"`
	Title                 string      `json:"title"`
	OwnerID               string      `json:"ownerId"`
	MachineID             string      `json:"machineId"`
	Enabled               bool        `json:"enabled"`
	ActivationID          string      `json:"activationId"`
	ActiveFrom            int64       `json:"activeFrom"`
	Trigger               Trigger     `json:"trigger"`
	Destination           Destination `json:"destination"`
	AgentConfigID         string      `json:"agentConfigId"`
	ProjectKind           string      `json:"projectKind,omitempty"`
	ProjectKey            string      `json:"projectKey,omitempty"`
	ScheduledMessage      bool        `json:"scheduledMessage,omitempty"`
	ElevatedPermissions   bool        `json:"elevatedPermissions"`
	DefinitionFingerprint string      `json:"definitionFingerprint"`
	CreatedAt             int64       `json:"createdAt"`
	UpdatedAt             int64       `json:"updatedAt"`
}

// Dispatch is a run a machine started.
type Dispatch struct {
	ScheduledFor int64  `json:"scheduledFor"` // ms
	DispatchedAt int64  `json:"dispatchedAt"` // ms
	SessionID    string `json:"sessionId"`
}

// Runtime is what the machine running a schedule reports of it.
type Runtime struct {
	ScheduleID                    string    `json:"scheduleId"`
	MachineID                     string    `json:"machineId"`
	ActivationID                  string    `json:"activationId"`
	ObservedDefinitionFingerprint string    `json:"observedDefinitionFingerprint"`
	NextScheduledAt               int64     `json:"nextScheduledAt,omitempty"` // ms
	QueueState                    string    `json:"queueState,omitempty"`      // due, waiting_for_agent, retrying or blocked
	BlockedCode                   string    `json:"blockedCode,omitempty"`
	MessageState                  string    `json:"messageState,omitempty"` // pending, sent, cancelled or failed
	LastDispatch                  *Dispatch `json:"lastDispatch,omitempty"`
	UpdatedAt                     int64     `json:"updatedAt"`
}

// Matches reports whether a runtime row speaks of a row as it now stands.
func (rt *Runtime) Matches(r Row) bool {
	return rt != nil && rt.ScheduleID == r.ScheduleID && rt.MachineID == r.MachineID &&
		rt.ActivationID == r.ActivationID && rt.ObservedDefinitionFingerprint == r.DefinitionFingerprint
}

// elevatedModes are the permission modes that skip approval, which a row
// flags.
var elevatedModes = map[string]bool{
	"agent-full-access": true, "danger-full-access": true, "dontAsk": true,
	"bypassPermissions": true, "yolo": true, "always-approve": true,
}

// Elevated reports whether an agent runs without asking for permission.
func (a Agent) Elevated() bool {
	if elevatedModes[a.ModeID] {
		return true
	}
	for _, v := range a.ConfigOptionValues {
		if s, ok := v.(string); ok && elevatedModes[s] {
			return true
		}
	}
	return false
}

// plain is v as JSON values, numbers kept exact.
func plain(v any) any {
	b, _ := json.Marshal(v)
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var out any
	d.Decode(&out)
	return out
}

// Fingerprint is a definition's fingerprint with its prompt: the SHA-256 of
// the canonical JSON of what decides a run, which leaves out the title and
// the times it was made and changed.
func Fingerprint(d Definition, prompt string) string { return fingerprint(d, prompt, nil) }

// fingerprint takes a scheduled message's payload in too.
func fingerprint(d Definition, prompt string, message map[string]any) string {
	def := plain(d).(map[string]any)
	for _, k := range []string{"title", "createdAt", "updatedAt", "createdBy"} {
		delete(def, k)
	}
	doc := map[string]any{"definition": def, "prompt": prompt}
	if message != nil {
		doc["message"] = message
	}
	sum := sha256.Sum256(Canonical(doc))
	return hex.EncodeToString(sum[:])
}

// RowOf is the registry row of a definition.
func RowOf(d Definition, prompt string) Row { return rowOf(d, prompt, nil) }

func rowOf(d Definition, prompt string, message map[string]any) Row {
	r := Row{
		ScheduleID: d.ScheduleID, Title: d.Title, OwnerID: d.OwnerID, MachineID: d.MachineID, Enabled: d.Enabled,
		ActivationID: d.ActivationID, ActiveFrom: d.ActiveFrom, Trigger: d.Trigger, Destination: d.Destination,
		AgentConfigID: d.Agent.AgentConfigID, ScheduledMessage: d.MessageVersion == 1,
		ElevatedPermissions: d.Agent.Elevated(), DefinitionFingerprint: fingerprint(d, prompt, message),
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
	if p := d.Project; p != nil {
		r.ProjectKind = p.Kind
		r.ProjectKey = p.RepoFullName
		if p.Kind == "local" {
			r.ProjectKey = p.LocalProjectID
		}
	}
	return r
}

// OwnSessionID is the chat a schedule owns at an epoch, which is never
// stored: a UUID v8 from the hash of what names it.
func OwnSessionID(scheduleID string, epoch int) string {
	sum := sha256.Sum256(Canonical([]any{"lody.schedule.v1", "own-session", scheduleID, epoch}))
	h := []byte(hex.EncodeToString(sum[:])[:32])
	h[12] = '8'
	c, _ := strconv.ParseUint(string(h[16]), 16, 8)
	h[16] = "0123456789abcdef"[(c&3)|8]
	s := string(h)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// Canonical is v as JavaScript's JSON.stringify writes it with its object
// keys sorted, and nil members left out as undefined ones are.
func Canonical(v any) []byte {
	var b bytes.Buffer
	canonical(&b, plain(v))
	return b.Bytes()
}

func canonical(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			b.WriteString(x.String())
			return
		}
		b.WriteString(jsNumber(f))
	case string:
		jsString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			canonical(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k, e := range x {
			if e != nil {
				keys = append(keys, k)
			}
		}
		// JavaScript compares strings by UTF-16 code unit.
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			jsString(b, k)
			b.WriteByte(':')
			canonical(b, x[k])
		}
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("schedule: can't write %T", v))
	}
}

func utf16Less(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// jsNumber writes a number as JavaScript does.
func jsNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if i := strings.IndexByte(s, 'e'); i >= 0 {
		exp := s[i+1:]
		if exp[0] == '+' {
			exp = exp[1:]
		}
		exp = strings.TrimLeft(exp, "0")
		if strings.HasPrefix(s[i+1:], "-") {
			exp = "-" + strings.TrimLeft(s[i+2:], "0")
		} else {
			exp = "+" + exp
		}
		s = s[:i] + "e" + exp
	}
	return s
}

// jsString writes a string as JSON.stringify does: only quotes,
// backslashes and control characters escaped.
func jsString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
