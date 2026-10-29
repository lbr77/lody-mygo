// Package catalog keeps a workspace's catalog: its machines, projects and
// sessions, replicated from the Flock streams "meta" and "mf:<machine>".
package catalog

import (
	"math"
	"sort"
	"strings"

	"github.com/lbr77/lody-mygo/internal/flock"
)

type Project struct {
	ID           string `json:"id"`
	MachineID    string `json:"machineId"`
	Name         string `json:"name"`
	RootPath     string `json:"rootPath"`
	RepoFullName string `json:"repoFullName,omitempty"`
	// History is what the machine last listed of the agents' own
	// conversations in the project, by provider.
	History map[string]any `json:"-"`
}

type Model struct {
	ModelID string `json:"modelId,omitempty"`
	Name    string `json:"name,omitempty"`
}

type Session struct {
	ID                    string  `json:"id"`
	MachineID             string  `json:"machineId"`
	UserID                string  `json:"userId,omitempty"` // who started it
	Title                 string  `json:"title"`
	Status                string  `json:"status"`
	Archived              bool    `json:"archived"`
	Pinned                bool    `json:"pinned"`
	ProjectID             string  `json:"projectId"`
	CreatedAt             string  `json:"createdAt"`
	CLIType               string  `json:"cliType,omitempty"`
	AgentType             string  `json:"agentType,omitempty"`
	Resume                string  `json:"resume,omitempty"`
	LastModel             *Model  `json:"lastModel,omitempty"`
	ParentSessionID       string  `json:"parentSessionId,omitempty"`
	ScheduleID            string  `json:"scheduleId,omitempty"` // the schedule whose run started it
	OpenedBySessionID     string  `json:"openedBySessionId,omitempty"`
	OpenedByRootSessionID string  `json:"openedByRootSessionId,omitempty"`
	AgentConfigID         string  `json:"agentConfigId,omitempty"`
	LatestUserMsgID       string  `json:"latestUserMsgId,omitempty"`
	LastMessageAt         float64 `json:"lastMessageAt,omitempty"`
	LastReadAt            float64 `json:"lastReadAt,omitempty"`
	LastRunningSeen       float64 `json:"lastRunningSeen,omitempty"`
	AwaitingUserSince     float64 `json:"awaitingUserSince,omitempty"`
	BranchName            string  `json:"branchName,omitempty"`
	// Project is the session's project as its meta has it, which a child
	// conversation takes over; RepoFullName and BaseBranch too.
	Project      map[string]any `json:"project,omitempty"`
	RepoFullName string         `json:"repoFullName,omitempty"`
	BaseBranch   string         `json:"baseBranch,omitempty"`
	// ChildPlacement is where a child conversation shows: "side-panel",
	// or among its parent's tabs.
	ChildPlacement string  `json:"childPlacement,omitempty"`
	DiffAdd        float64 `json:"diffAdd,omitempty"`
	DiffDel        float64 `json:"diffDel,omitempty"`
	// PullRequests are the pull requests the session opened, oldest
	// first; PRState is each one's CI and merge state, by URL.
	PullRequests []PullRequest      `json:"pullRequests,omitempty"`
	PRState      map[string]PRState `json:"prState,omitempty"`
}

// PullRequest is a pull request a session opened.
type PullRequest struct {
	URL        string  `json:"url"`
	Status     string  `json:"status"` // open, draft, merged or closed
	Number     float64 `json:"number,omitempty"`
	ReportedAt string  `json:"reportedAt,omitempty"`
}

// PRState is a pull request's CI and merge state, as the machine's poller
// writes it: CI s(uccess), f(ailure), p(ending), e(rror), x (expected);
// merge c(lean), b(locked), d(irty), h (behind), u(nstable).
type PRState struct {
	CI    string `json:"s,omitempty"`
	Merge string `json:"m,omitempty"`
}

// LatestPR is the session's pull request to show, as Lody picks it: the
// latest reported, when any was, or else the last.
func (s *Session) LatestPR() *PullRequest {
	if len(s.PullRequests) == 0 {
		return nil
	}
	best, reported := -1, false
	for i, pr := range s.PullRequests {
		if pr.ReportedAt != "" {
			reported = true
		}
		if best < 0 || pr.ReportedAt > s.PullRequests[best].ReportedAt {
			best = i
		}
	}
	if !reported {
		best = len(s.PullRequests) - 1
	}
	return &s.PullRequests[best]
}

// PRNumber is a pull request's number, from its URL when not recorded.
func (pr PullRequest) PRNumber() int {
	if pr.Number > 0 {
		return int(pr.Number)
	}
	if i := strings.LastIndex(pr.URL, "/pull/"); i >= 0 {
		n := 0
		for _, r := range pr.URL[i+6:] {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		return n
	}
	return 0
}

// Ready is whether the pull request merges cleanly with green, or no, CI.
func (st PRState) Ready() bool {
	return st.Merge == "c" && (st.CI == "" || st.CI == "s")
}

// Working reports whether the session's agent is busy, by the statuses the
// iOS outbox queues behind.
func (s *Session) Working() bool {
	switch s.Status {
	case "running", "initializing", "processing", "in_progress", "queued", "requestPermission", "waiting":
		return true
	}
	return false
}

// Waiting reports whether the agent waits for the user.
func (s *Session) Waiting() bool {
	return s.AwaitingUserSince > 0 || s.Status == "requestPermission" || s.Status == "waiting"
}

// Unread reports whether there are messages newer than the last read.
func (s *Session) Unread() bool {
	return s.LastMessageAt > 0 && s.LastMessageAt > s.LastReadAt
}

// AgentConfig is an agent a machine offers for new sessions.
type AgentConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MachineID string `json:"machineId"`
	AgentType string `json:"agentType"`
	CLIType   string `json:"cliType"`
	// Row is the config as its machine's stream stores it, for edits,
	// which rewrite it whole.
	Row map[string]any `json:"-"`
}

// Capability lists what an agent config supports.
type Capability struct {
	Models []Model `json:"models,omitempty"`
	Modes  []Mode  `json:"modes,omitempty"` // permission modes
	// Steer is whether the agent's runtime acknowledges messages given to
	// a running turn.
	Steer bool `json:"steer,omitempty"`
	// Commands are the agent's slash commands.
	Commands []Command `json:"commands,omitempty"`
	// DefaultModel is the name of the model the agent starts with.
	DefaultModel string `json:"defaultModel,omitempty"`
	// Options are the agent's session settings, select or boolean ones,
	// as title generation picks from.
	Options []ConfigOption `json:"options,omitempty"`
	// Efforts are the reasoning efforts each model allows, by model id.
	Efforts map[string][]string `json:"efforts,omitempty"`
	// SessionTitle is whether the agent names its sessions itself.
	SessionTitle bool `json:"sessionTitle,omitempty"`
	// Fork is whether a session can start again from one of its replies.
	Fork bool `json:"fork,omitempty"`
	// GoalActions are the goal commands the agent takes: set, pause,
	// resume, clear.
	GoalActions []string `json:"goalActions,omitempty"`
	// RateLimits is whether the agent reports its subscription windows,
	// and QueryRateLimits whether it reports them when asked.
	RateLimits      bool `json:"rateLimits,omitempty"`
	QueryRateLimits bool `json:"queryRateLimits,omitempty"`
	// CLIType and AgentType are the runtime the agent reported for.
	CLIType   string `json:"cliType,omitempty"`
	AgentType string `json:"agentType,omitempty"`
}

// ConfigOption is a session setting an agent offers.
type ConfigOption struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category string   `json:"category,omitempty"` // mode, model, thought_level or the agent's own
	Type     string   `json:"type"`               // select or boolean
	Current  any      `json:"current"`            // a string, or a bool for a boolean
	Choices  []Choice `json:"choices,omitempty"`
}

// Choice is a value a select option takes.
type Choice struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// RateLimit is a provider's quota: the windows its subscription meters.
type RateLimit struct {
	ConfigID, CLIType, LimitID, Label string
	Windows                           []RateWindow
}

// RateWindow is a quota window: how much of it is used, how long it is,
// and when it starts over (Unix seconds; zero when unknown).
type RateWindow struct {
	Label       string
	UsedPercent float64
	Seconds     float64
	ResetsAt    float64
}

// Command is an agent's slash command.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Mode is a permission mode of an agent.
type Mode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Lody's built-in modes, for agents that haven't reported theirs.
var (
	claudeModes = []Mode{{"auto", "Auto"}, {"default", "Manual"}, {"acceptEdits", "Accept Edits"}, {"plan", "Plan Mode"}, {"dontAsk", "Don't Ask"}, {"bypassPermissions", "Bypass Permissions"}}
	codexModes  = []Mode{{"read-only", "Read-only"}, {"agent", "Agent"}, {"agent-auto-review", "Auto review"}, {"agent-full-access", "Full access"}}
)

// ModesFor returns an agent's permission modes: those it reported, or
// Lody's built-in ones for Claude Code and Codex.
func (c *Catalog) ModesFor(agentConfigID, agentType string) []Mode {
	if modes := c.Capabilities[agentConfigID].Modes; len(modes) > 0 {
		return modes
	}
	switch strings.ToLower(agentType) {
	case "claude", "claude-code":
		return claudeModes
	case "codex":
		return codexModes
	}
	return nil
}

type Catalog struct {
	Projects     []Project                     `json:"projects"`
	Sessions     []Session                     `json:"sessions"`
	MachineIDs   []string                      `json:"machineIds"`
	MachineNames map[string]string             `json:"machineNames,omitempty"`
	MachineOS    map[string]string             `json:"machineOs,omitempty"`
	MachineCLI   map[string]string             `json:"machineCli,omitempty"`   // the Lody CLI's version
	MachineCaps  map[string]map[string]float64 `json:"machineCaps,omitempty"`  // protocol capabilities, by machine
	MachineOwner map[string]string             `json:"machineOwner,omitempty"` // the user who owns it, by machine
	MachineZone  map[string]string             `json:"machineZone,omitempty"`  // its IANA time zone, by machine
	Agents       map[string][]AgentConfig      `json:"agents,omitempty"`       // by machine
	Capabilities map[string]Capability         `json:"capabilities,omitempty"`
	MCPServers   []MCPServer                   `json:"-"`
	Roles        []AgentRole                   `json:"-"`
	ProjectMCP   map[string][]string           `json:"-"` // default MCP servers by project key
	Memories     map[string][]Memory           `json:"-"` // by machine
	RateLimits   map[string][]RateLimit        `json:"-"` // providers' quotas, by agent config
	Removing     map[string]bool               `json:"-"` // projects their machines are removing
}

// Session returns the session with id.
func (c *Catalog) Session(id string) *Session {
	for i := range c.Sessions {
		if c.Sessions[i].ID == id {
			return &c.Sessions[i]
		}
	}
	return nil
}

// Project returns the project with id.
func (c *Catalog) Project(id string) *Project {
	for i := range c.Projects {
		if c.Projects[i].ID == id {
			return &c.Projects[i]
		}
	}
	return nil
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func text(v any) string {
	s, _ := v.(string)
	return s
}

func stamp(v any) float64 {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

func modelOf(v any) *Model {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return &Model{ModelID: strings.TrimSpace(text(m["modelId"])), Name: strings.TrimSpace(text(m["name"]))}
}

// projectMachine projects the rows of a machine stream "mf:<machine>".
func projectMachine(rows []flock.Row, machineID string) Catalog {
	c := Catalog{Agents: map[string][]AgentConfig{}, Capabilities: map[string]Capability{}}
	for _, row := range rows {
		if !row.Live || len(row.Key) < 2 {
			continue
		}
		id := text(row.Key[1])
		value := object(row.Value)
		switch row.Key[0] {
		case "rateLimit":
			// ['rateLimit', configId, cliType, limitId], as the machine
			// reports a provider's subscription windows.
			if len(row.Key) != 4 {
				continue
			}
			rl := RateLimit{ConfigID: id, CLIType: text(row.Key[2]), LimitID: firstText(text(value["limitId"]), text(row.Key[3])), Label: text(value["label"])}
			for _, w := range anyList(value["windows"]) {
				win := object(w)
				used, ok := win["usedPercent"].(float64)
				if !ok {
					continue
				}
				rl.Windows = append(rl.Windows, RateWindow{Label: strings.TrimSpace(text(win["label"])), UsedPercent: math.Max(0, math.Min(100, used)),
					Seconds: stamp(win["windowDurationSeconds"]), ResetsAt: stamp(win["resetsAtEpochSeconds"])})
			}
			if id == "" || len(rl.Windows) == 0 {
				continue
			}
			if c.RateLimits == nil {
				c.RateLimits = map[string][]RateLimit{}
			}
			c.RateLimits[id] = append(c.RateLimits[id], rl)
		case "memory":
			if len(row.Key) != 3 || value == nil {
				continue
			}
			m := Memory{ProviderID: id, MemoryID: text(row.Key[2]), MachineID: text(value["machineId"]), Name: strings.TrimSpace(text(value["name"])), Description: text(value["description"])}
			if m.MemoryID == "" || m.Name == "" || text(value["providerId"]) != m.ProviderID || text(value["memoryId"]) != m.MemoryID || m.MachineID != machineID {
				continue
			}
			if c.Memories == nil {
				c.Memories = map[string][]Memory{}
			}
			c.Memories[machineID] = append(c.Memories[machineID], m)
		case "cmd":
			if id == "deleteLocalProject" && len(row.Key) == 3 && text(object(row.Value)["status"]) != "completed" {
				if c.Removing == nil {
					c.Removing = map[string]bool{}
				}
				c.Removing[machineID+":local:"+text(row.Key[2])] = true
			}
		case "localProject":
			if id == "" {
				continue
			}
			name := text(value["name"])
			if name == "" {
				name = id
			}
			c.Projects = append(c.Projects, Project{ID: machineID + ":local:" + id, MachineID: machineID, Name: name, RootPath: text(value["rootPath"]), History: object(value["history"])})
		case "agentConfig":
			cfg := AgentConfig{ID: text(value["id"]), Name: text(value["name"]), MachineID: text(value["machineId"]), AgentType: text(value["agentType"]), CLIType: text(value["cliType"]), Row: value}
			if cfg.ID == "" {
				cfg.ID = id
			}
			if cfg.MachineID == "" {
				cfg.MachineID = machineID
			}
			if cfg.Name == "" {
				cfg.Name = cfg.AgentType
			}
			c.Agents[machineID] = append(c.Agents[machineID], cfg)
		case "acpCapability":
			var capability Capability
			for _, m := range anyList(value["models"]) {
				if model := modelOf(m); model != nil && model.ModelID != "" {
					capability.Models = append(capability.Models, *model)
				}
			}
			for _, m := range anyList(value["modes"]) {
				mode := object(m)
				if id := text(mode["id"]); id != "" {
					name := text(mode["name"])
					if name == "" {
						name = id
					}
					capability.Modes = append(capability.Modes, Mode{ID: id, Name: name})
				}
			}
			for _, m := range anyList(value["availableCommands"]) {
				cmd := object(m)
				if name := text(cmd["name"]); name != "" {
					capability.Commands = append(capability.Commands, Command{Name: name, Description: text(cmd["description"])})
				}
			}
			capability.Steer = value["acknowledgedSteer"] == true && value["provenance"] == "runtime"
			capability.SessionTitle = value["sessionTitle"] == true
			if rl := object(value["rateLimits"]); len(rl) > 0 {
				capability.RateLimits, capability.QueryRateLimits = true, rl["query"] == true
			}
			capability.Fork = value["sessionFork"] == true && value["provenance"] == "runtime"
			for _, a := range anyList(value["goalActions"]) {
				capability.GoalActions = append(capability.GoalActions, text(a))
			}
			capability.CLIType, capability.AgentType = text(value["cliType"]), text(value["agentType"])
			for _, o := range anyList(value["configOptions"]) {
				opt := object(o)
				option := ConfigOption{ID: text(opt["id"]), Name: text(opt["name"]), Category: text(opt["category"]), Type: text(opt["type"]), Current: opt["currentValue"]}
				if option.ID == "" || (option.Type != "select" && option.Type != "boolean") {
					continue
				}
				for _, choice := range anyList(opt["options"]) {
					ch := object(choice)
					if v := text(ch["value"]); v != "" {
						option.Choices = append(option.Choices, Choice{Value: v, Name: firstText(text(ch["name"]), v)})
					}
				}
				capability.Options = append(capability.Options, option)
			}
			for model, efforts := range object(value["modelReasoningEfforts"]) {
				if capability.Efforts == nil {
					capability.Efforts = map[string][]string{}
				}
				for _, e := range anyList(efforts) {
					capability.Efforts[model] = append(capability.Efforts[model], text(e))
				}
			}
			// The model option's current value names the default, as
			// Lody's provider rows show it.
			for _, o := range anyList(value["configOptions"]) {
				opt := object(o)
				if text(opt["category"]) != "model" {
					continue
				}
				current := text(opt["currentValue"])
				for _, choice := range anyList(opt["options"]) {
					ch := object(choice)
					if current != "" && current != "default" && text(ch["value"]) == current {
						capability.DefaultModel = firstText(text(ch["name"]), current)
					}
				}
			}
			if capability.DefaultModel == "" && len(capability.Models) > 0 {
				capability.DefaultModel = firstText(capability.Models[0].Name, capability.Models[0].ModelID)
			}
			c.Capabilities[id] = capability
		}
	}
	return c
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

// projectMeta projects the rows of the workspace stream "meta". Rows come in
// key order, so a room's whole-object row precedes its field overrides.
func projectMeta(rows []flock.Row) Catalog {
	var c Catalog
	c.MachineNames = map[string]string{}
	c.MachineOS = map[string]string{}
	c.MachineCLI = map[string]string{}
	c.MachineCaps = map[string]map[string]float64{}
	c.MachineOwner, c.MachineZone = map[string]string{}, map[string]string{}
	active := map[string]bool{}
	metadata := map[string]map[string]any{}
	var order []string
	for _, row := range rows {
		if len(row.Key) < 2 {
			continue
		}
		id := text(row.Key[1])
		if row.Key[0] == "e" && row.Live && row.Value == true {
			active[id] = true
		}
		if row.Key[0] != "m" || id == "" {
			continue
		}
		fields, ok := metadata[id]
		if !ok {
			fields = map[string]any{}
			metadata[id] = fields
			order = append(order, id)
		}
		if len(row.Key) == 2 {
			if row.Live {
				for k, v := range object(row.Value) {
					fields[k] = v
				}
			}
		} else if field, ok := row.Key[2].(string); ok {
			if !row.Live {
				delete(fields, field)
			} else {
				fields[field] = row.Value
			}
		}
	}
	for _, id := range order {
		if !active[id] {
			continue
		}
		value := metadata[id]
		if machineID, ok := strings.CutPrefix(id, "machine-"); ok {
			c.MachineIDs = append(c.MachineIDs, machineID)
			if name := text(value["name"]); name != "" {
				c.MachineNames[machineID] = name
			}
			if os := text(value["os"]); os != "" {
				c.MachineOS[machineID] = os
			}
			if v := text(value["cliVersion"]); v != "" {
				c.MachineCLI[machineID] = v
			}
			if v := text(value["ownerUserId"]); v != "" {
				c.MachineOwner[machineID] = v
			}
			if v := text(value["timeZone"]); v != "" {
				c.MachineZone[machineID] = v
			}
			if caps := object(value["protocolCapabilities"]); len(caps) > 0 {
				versions := map[string]float64{}
				for k, v := range caps {
					versions[k] = stamp(v)
				}
				c.MachineCaps[machineID] = versions
			}
			local := object(value["localProjects"])
			keys := make([]string, 0, len(local))
			for k := range local {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, localID := range keys {
				p := object(local[localID])
				name := text(p["name"])
				if name == "" {
					name = localID
				}
				c.Projects = append(c.Projects, Project{ID: machineID + ":local:" + localID, MachineID: machineID, Name: name, RootPath: text(p["rootPath"])})
			}
			continue
		}
		if !strings.HasPrefix(id, "session-") || strings.HasPrefix(id, "session-comment-") {
			continue
		}
		machineID := text(value["machineId"])
		project := object(value["project"])
		localID := text(project["localProjectId"])
		repo := text(project["repoFullName"])
		if repo == "" {
			repo = text(value["repoFullName"])
		}
		projectID := machineID + ":unassigned"
		switch {
		case project["kind"] == "local" && localID != "":
			projectID = machineID + ":local:" + localID
		case repo != "":
			projectID = "github:" + repo
		}
		status := text(value["status"])
		if status == "" {
			status = text(object(value["status"])["type"])
		}
		if status == "" {
			status = "unknown"
		}
		s := Session{
			ID:                    text(value["id"]),
			MachineID:             machineID,
			UserID:                text(value["userId"]),
			Title:                 text(value["title"]),
			Status:                status,
			Archived:              value["isArchived"] == true,
			Pinned:                value["isPinned"] == true,
			ProjectID:             projectID,
			CreatedAt:             text(value["createdAt"]),
			CLIType:               text(value["cliType"]),
			AgentType:             text(value["agentType"]),
			Resume:                text(value["acpSessionId"]),
			LastModel:             modelOf(value["lastModel"]),
			ParentSessionID:       strings.TrimSpace(text(value["parentSessionId"])),
			ScheduleID:            text(value["scheduleId"]),
			OpenedBySessionID:     strings.TrimSpace(text(value["openedBySessionId"])),
			OpenedByRootSessionID: strings.TrimSpace(text(value["openedByRootSessionId"])),
			AgentConfigID:         text(value["agentConfigId"]),
			LatestUserMsgID:       text(value["latestUserMsgId"]),
			LastMessageAt:         stamp(value["lastMessageAt"]),
			LastReadAt:            stamp(value["lastReadAt"]),
			LastRunningSeen:       stamp(value["lastRunningSeen"]),
			AwaitingUserSince:     stamp(value["awaitingUserSince"]),
			BranchName:            text(value["branchName"]),
			Project:               object(value["project"]),
			RepoFullName:          text(value["repoFullName"]),
			BaseBranch:            text(value["baseBranch"]),
			ChildPlacement:        text(value["childSessionPlacement"]),
		}
		if s.ID == "" {
			s.ID = strings.TrimPrefix(id, "session-")
		}
		if s.Title == "" {
			s.Title = "Untitled session"
		}
		change := object(object(value["diffStats"])["allChange"])
		s.DiffAdd, s.DiffDel = stamp(change["add"]), stamp(change["del"])
		for _, raw := range anyList(value["pullRequests"]) {
			pr := object(raw)
			url, status := strings.TrimSpace(text(pr["url"])), text(pr["status"])
			switch status {
			case "open", "draft", "merged", "closed":
			default:
				continue
			}
			if url != "" {
				s.PullRequests = append(s.PullRequests, PullRequest{URL: url, Status: status, Number: stamp(pr["number"]), ReportedAt: text(pr["reportedAt"])})
			}
		}
		for url, raw := range object(value["pullRequestState"]) {
			st := object(raw)
			if s.PRState == nil {
				s.PRState = map[string]PRState{}
			}
			s.PRState[url] = PRState{CI: text(st["s"]), Merge: text(st["m"])}
		}
		c.Sessions = append(c.Sessions, s)
		if localID != "" || repo != "" {
			name := repo
			if name == "" {
				name = "Local project"
			}
			githubRepo := repo
			if githubRepo == "" {
				githubRepo = text(project["githubRepoFullName"])
			}
			c.Projects = append(c.Projects, Project{ID: projectID, MachineID: machineID, Name: name, RepoFullName: githubRepo})
		}
	}
	c.Projects = uniqueProjects(c.Projects)
	return c
}

// uniqueProjects keeps the first project of each id, filling in a repo a
// later one knows.
func uniqueProjects(projects []Project) []Project {
	index := map[string]int{}
	var out []Project
	for _, p := range projects {
		if i, ok := index[p.ID]; ok {
			if out[i].RepoFullName == "" && p.RepoFullName != "" {
				out[i].RepoFullName = p.RepoFullName
			}
			continue
		}
		index[p.ID] = len(out)
		out = append(out, p)
	}
	return out
}

// merge combines the meta catalog with the machine catalogs: machine
// projects overlay meta's by id. Projects sort by name, sessions newest
// first.
func merge(meta Catalog, machines map[string]Catalog) Catalog {
	out := Catalog{
		MachineIDs:   meta.MachineIDs,
		MachineNames: meta.MachineNames,
		MachineOS:    meta.MachineOS,
		MachineCLI:   meta.MachineCLI,
		MachineCaps:  meta.MachineCaps,
		MachineOwner: meta.MachineOwner,
		MachineZone:  meta.MachineZone,
		Agents:       map[string][]AgentConfig{},
		Capabilities: map[string]Capability{},
	}
	projects := map[string]Project{}
	var order []string
	add := func(p Project) {
		prev, ok := projects[p.ID]
		if !ok {
			order = append(order, p.ID)
		} else if p.RepoFullName == "" {
			p.RepoFullName = prev.RepoFullName
		}
		projects[p.ID] = p
	}
	for _, p := range meta.Projects {
		add(p)
	}
	for _, id := range meta.MachineIDs {
		m := machines[id]
		for _, p := range m.Projects {
			add(p)
		}
		out.Agents[id] = m.Agents[id]
		for k, v := range m.Capabilities {
			out.Capabilities[k] = v
		}
	}
	for _, id := range order {
		out.Projects = append(out.Projects, projects[id])
	}
	ws := machines[workspaceMode]
	out.MCPServers, out.Roles, out.ProjectMCP = ws.MCPServers, ws.Roles, ws.ProjectMCP
	for _, id := range meta.MachineIDs {
		m := machines[id]
		for _, mem := range m.Memories[id] {
			if out.Memories == nil {
				out.Memories = map[string][]Memory{}
			}
			out.Memories[id] = append(out.Memories[id], mem)
		}
		for cfg, limits := range m.RateLimits {
			if out.RateLimits == nil {
				out.RateLimits = map[string][]RateLimit{}
			}
			out.RateLimits[cfg] = append(out.RateLimits[cfg], limits...)
		}
		for p := range m.Removing {
			if out.Removing == nil {
				out.Removing = map[string]bool{}
			}
			out.Removing[p] = true
		}
	}
	sort.SliceStable(out.Projects, func(i, j int) bool {
		return strings.ToLower(out.Projects[i].Name) < strings.ToLower(out.Projects[j].Name)
	})
	out.Sessions = append([]Session(nil), meta.Sessions...)
	sort.SliceStable(out.Sessions, func(i, j int) bool { return out.Sessions[i].CreatedAt > out.Sessions[j].CreatedAt })
	return out
}

func firstText(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
