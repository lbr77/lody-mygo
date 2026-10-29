package app

import (
	"maps"
	"slices"

	"github.com/lbr77/lody-mygo/internal/catalog"
)

// Model memory, as lody-ios's create screen keeps it: what New chat was
// set to, remembered per project and per agent and model, on this device,
// and given back when the user returns to that project or picks that
// model again — only what the agent still offers.

type modelChoice struct {
	Machine string         `json:"machineId,omitempty"`
	Agent   string         `json:"agentKey,omitempty"`
	Model   string         `json:"modelId,omitempty"`
	Mode    string         `json:"modeId,omitempty"`
	Values  map[string]any `json:"configOptionValues,omitempty"`
}

type createMemory struct {
	Projects map[string]modelChoice `json:"projects,omitempty"`     // by project, "chat" for none
	Models   map[string]modelChoice `json:"modelChoices,omitempty"` // by agent|model
}

// createKey is whose memory: lody-ios's create:<user>:<workspace>.
func (a *App) createKey() string {
	if a.account == nil {
		return ""
	}
	return "create:" + a.account.User.ID + ":" + a.workspaceID
}

func newChatProjectKey(project string) string {
	if project == "" {
		return "chat"
	}
	return project
}

// rememberNewChat writes what New chat is set to, when it changed.
func (a *App) rememberNewChat() {
	key := a.createKey()
	nc := a.newChat
	if key == "" || nc.agent == "" {
		return
	}
	if a.createMem == nil {
		a.createMem = map[string]*createMemory{}
	}
	mem := a.createMem[key]
	if mem == nil {
		mem = &createMemory{}
		a.createMem[key] = mem
	}
	choice := modelChoice{Machine: nc.machine, Agent: nc.agent, Model: a.models[""], Mode: a.modes[""], Values: maps.Clone(a.runValues[""])}
	project := newChatProjectKey(nc.project)
	if same(mem.Projects[project], choice) {
		return
	}
	if mem.Projects == nil {
		mem.Projects = map[string]modelChoice{}
	}
	if mem.Models == nil {
		mem.Models = map[string]modelChoice{}
	}
	mem.Projects[project] = choice
	mem.Models[nc.agent+"|"+choice.Model] = choice
	a.savePrefs()
}

func same(x, y modelChoice) bool {
	return x.Machine == y.Machine && x.Agent == y.Agent && x.Model == y.Model && x.Mode == y.Mode && maps.EqualFunc(x.Values, y.Values, func(a, b any) bool { return a == b })
}

// restoreNewChat gives New chat back what was remembered for its project,
// as the agent still offers it.
func (a *App) restoreNewChat() {
	mem := a.createMem[a.createKey()]
	if mem == nil {
		return
	}
	choice, ok := mem.Projects[newChatProjectKey(a.newChat.project)]
	if !ok {
		return
	}
	if a.newChat.project == "" && choice.Machine != "" && slices.Contains(a.catalog.MachineIDs, choice.Machine) {
		a.newChat.machine = choice.Machine
	}
	if a.agent(a.newChat.machine, choice.Agent) == nil {
		return
	}
	a.newChat.agent = choice.Agent
	a.applyModelChoice(choice)
}

// applyModelChoice sets New chat's model, mode and settings from a
// remembered choice, keeping each only if the agent offers it: a model it
// lists, a mode it has, an effort the model allows, a switch's boolean, a
// select's known value.
func (a *App) applyModelChoice(choice modelChoice) {
	agent := a.agent(a.newChat.machine, a.newChat.agent)
	if agent == nil {
		return
	}
	cap := a.catalog.Capabilities[agent.ID]
	delete(a.models, "")
	delete(a.modes, "")
	delete(a.runValues, "")
	if choice.Model != "" && (len(cap.Models) == 0 || slices.ContainsFunc(cap.Models, func(m catalog.Model) bool { return m.ModelID == choice.Model })) {
		a.models[""] = choice.Model
	}
	if choice.Mode != "" && slices.ContainsFunc(a.catalog.ModesFor(agent.ID, agent.AgentType), func(m catalog.Mode) bool { return m.ID == choice.Mode }) {
		a.modes[""] = choice.Mode
	}
	effort, hasEffort := effortOption(cap, a.models[""])
	for id, v := range choice.Values {
		valid := false
		for _, o := range cap.Options {
			if o.ID != id || o.Category == "model" {
				continue
			}
			switch {
			case o.Type == "boolean":
				_, valid = v.(bool)
			case hasEffort && o.ID == effort.ID:
				valid = slices.ContainsFunc(effort.Choices, func(ch catalog.Choice) bool { return ch.Value == v })
			case o.Type == "select":
				valid = slices.ContainsFunc(o.Choices, func(ch catalog.Choice) bool { return ch.Value == v })
			}
		}
		if valid {
			a.setRunValue("", id, v)
		}
	}
}

// syncNewChatMemory, each frame New chat shows, restores on a change of
// project and remembers what the user set.
func (a *App) syncNewChatMemory() {
	project := newChatProjectKey(a.newChat.project) + "|" + a.workspaceID
	if a.memProject != project {
		a.memProject = project
		a.restoreNewChat()
		a.memAgentModel = a.newChat.agent + "|" + a.models[""]
		return
	}
	// Another agent or model picked: what was set with it before.
	if am := a.newChat.agent + "|" + a.models[""]; a.memAgentModel != am {
		a.memAgentModel = am
		if mem := a.createMem[a.createKey()]; mem != nil {
			if choice, ok := mem.Models[am]; ok {
				a.applyModelChoice(choice)
			}
		}
	}
	a.rememberNewChat()
}
