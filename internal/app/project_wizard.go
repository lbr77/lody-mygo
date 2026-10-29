package app

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/flock"
	"github.com/lbr77/lody-mygo/locales"
)

// Lody's new-project wizard (settings/project-create-wizard.tsx), which
// Settings › Projects opens from its add menu: a project of several
// repositories or folders, put together one question at a time. What it
// holds (folders on a machine, or GitHub only), which machine, which
// folders and repositories, then its name and instructions. Answering
// moves on; Back revisits a question, and an answer that changes drops
// what no longer fits. A machine's folders are its daemon's: a folder
// picked from the browser is prepared by it, and registered on its
// stream as the project is saved; the project itself is a row of the
// workspace's stream.

// projectWizard is the wizard's state while it is open.
type projectWizard struct {
	open  bool
	id    string // the project's, from the moment it opens, as Lody's
	step  string // kind, machine, sources or details
	scope groupScope
	draft groupDraft
	// alsoFolder is the folders ticked beside their repository on the
	// GitHub tab, which wins: only noted, so either can be unticked alone.
	alsoFolder map[string]bool
	tab        string // the sources' tab: folders or github
	query      string // the sources' search
	submitting bool
	err        string
	// gen tells this opening from the next, so that a folder the
	// machine prepares for a wizard since closed is dropped.
	gen int
	// editing is the project an edit walks through again, as saved; nil
	// creating one. previous is what it had, marked on the answers.
	editing  *catalog.ProjectGroup
	previous *wizardPrevious
	// onCreated hears of the project once it is saved, so that what asked
	// for it can choose it.
	onCreated func(id string)
	// probes are what members' repositories declare as submodules, by
	// source, kept from one opening to the next as Lody keeps them for
	// the page's life.
	probes map[string]*groupProbe
	// repos are the GitHub repositories folders turned out to be, by
	// source, so that a folder and a GitHub member of one repository match.
	repos map[string]string
}

// wizardPrevious is an edited project's answers before this edit: the
// scope its members implied, and how each was used, by source.
type wizardPrevious struct {
	scope groupScope
	usage map[string]string
}

// openProjectWizard opens the wizard on its first question.
func (a *App) openProjectWizard() {
	// What was read stays; what failed is asked again.
	probes := map[string]*groupProbe{}
	for k, pr := range a.projectWizard.probes {
		if !pr.done || !pr.failed {
			probes[k] = pr
		}
	}
	a.projectWizard = projectWizard{open: true, id: uuid.NewString(), step: "kind", alsoFolder: map[string]bool{}, gen: a.projectWizard.gen + 1, probes: probes}
}

// openProjectWizardFrom opens the wizard on a project it starts from, as
// a project row's Edit project… does: the folder or repository seeded as
// its first member and named after it, the sources open on the scope it
// implies. A folder with a GitHub remote is listed under its repository,
// so its key finds the repository and the seed becomes that.
func (a *App) openProjectWizardFrom(key, name string, created func(id string)) {
	a.openProjectWizard()
	wz := &a.projectWizard
	wz.onCreated = created
	if key == "" {
		return
	}
	f := a.wizardFrame()
	o := f.byKey[key]
	if o == nil {
		return
	}
	if name == "" {
		name = o.label
	}
	wz.draft = groupDraft{name: name}.add(o.source, o.label)
	if scope, ok := scopeOf(wz.draft, f.options, f.current); ok {
		wz.goTo("sources", scope)
	}
}

// openProjectEditor opens a saved project in the wizard, which walks its
// questions again from the first, its earlier answers marked "Previously
// selected", and saves it as its next revision.
func (a *App) openProjectEditor(g catalog.ProjectGroup) {
	a.openProjectWizard()
	wz := &a.projectWizard
	f := a.wizardFrame()
	wz.id, wz.editing, wz.draft = g.ID, &g, draftOf(g)
	prev := &wizardPrevious{usage: map[string]string{}}
	for _, m := range wz.draft.members {
		prev.usage[m.source.key()] = m.usage()
	}
	if scope, ok := scopeOf(wz.draft, f.options, f.current); ok {
		prev.scope, wz.scope = scope, scope
	}
	wz.previous = prev
}

// scopeOf is the scope a draft already implies, as Lody's: a folder
// decides it; a repository that uses a machine's copy names the machine
// holding one, this one first; plain clones run anywhere. An empty draft
// implies none.
func scopeOf(d groupDraft, options []memberOption, current string) (groupScope, bool) {
	if len(d.members) == 0 {
		return groupScope{}, false
	}
	machine := d.localMachine()
	for _, m := range d.members {
		if machine == "" && m.folder != nil {
			machine = m.folder.machine
		}
	}
	if machine != "" {
		return groupScope{kind: "machine", machine: machine}, true
	}
	copies := map[string]bool{}
	for _, m := range d.members {
		if m.usage() == "localCopy" {
			copies[m.source.key()] = true
		}
	}
	var held []memberCopy
	for _, o := range options {
		if copies[o.key] {
			held = append(held, o.copies...)
		}
	}
	for _, cp := range held {
		if cp.machine == current {
			return groupScope{kind: "machine", machine: cp.machine}, true
		}
	}
	if len(held) > 0 {
		return groupScope{kind: "machine", machine: held[0].machine}, true
	}
	return groupScope{kind: "github"}, true
}

// prevUsage is how an edited project used a source before this edit.
func (wz *projectWizard) prevUsage(key string) string {
	if wz.previous == nil {
		return ""
	}
	return wz.previous.usage[key]
}

// wizardSteps are the questions a scope asks: a GitHub-only project has
// no machine to choose.
func (wz *projectWizard) steps() []string {
	if wz.scope.kind == "github" {
		return []string{"kind", "sources", "details"}
	}
	return []string{"kind", "machine", "sources", "details"}
}

// goTo moves to a question with a scope. The sources open on their first
// tab with nothing searched, as Lody's step mounts afresh.
func (wz *projectWizard) goTo(step string, scope groupScope) {
	wz.step, wz.scope = step, scope
	if step == "sources" {
		wz.tab, wz.query = "github", ""
		if scope.kind == "machine" {
			wz.tab = "folders"
		}
	}
}

func (wz *projectWizard) back() {
	switch wz.step {
	case "machine":
		wz.goTo("kind", wz.scope)
	case "sources":
		if wz.scope.kind == "machine" {
			wz.goTo("machine", wz.scope)
		} else {
			wz.goTo("kind", wz.scope)
		}
	case "details":
		wz.goTo("sources", wz.scope)
	}
}

func (wz *projectWizard) setAlso(key string, on bool) {
	next := maps.Clone(wz.alsoFolder)
	if next == nil {
		next = map[string]bool{}
	}
	if on {
		next[key] = true
	} else {
		delete(next, key)
	}
	wz.alsoFolder = next
}

// fitScope keeps what a scope can hold; folders from the chosen machine
// bind the project to it.
func fitScope(d groupDraft, scope groupScope, f *wizardFrame) groupDraft {
	d = d.retain(scope, f.labelOf)
	if scope.kind != "machine" {
		return d
	}
	return d.bind(scope.machine, func(repo string) string {
		key := "github:" + strings.ToLower(repo)
		for _, o := range f.options {
			if o.key != key {
				continue
			}
			for _, cp := range o.copies {
				if cp.machine == scope.machine {
					return cp.local
				}
			}
		}
		return ""
	})
}

// wizardFrame is what the wizard's questions read of the workspace in a
// frame: what a project may hold, and how its members are named.
type wizardFrame struct {
	a       *App
	options []memberOption
	byKey   map[string]*memberOption
	current string // this machine
}

func (a *App) wizardFrame() *wizardFrame {
	options := a.memberOptions()
	return &wizardFrame{a: a, options: options, byKey: optionsByKey(options), current: a.localMachine()}
}

// machineLabel is a machine as the wizard says it: this one is "This
// machine".
func (f *wizardFrame) machineLabel(id string) string {
	switch id {
	case "":
		return ""
	case f.current:
		return locales.WizardThisMachine()
	}
	return f.a.machineName(id)
}

// labelOf is a member's name: its picked folder's, its option's, or its
// repository's.
func (f *wizardFrame) labelOf(m groupMember) string {
	if m.folder != nil {
		return m.folder.name
	}
	if o := f.byKey[m.source.key()]; o != nil {
		return o.label
	}
	if m.source.kind == "github" {
		return m.source.repo
	}
	return locales.WizardMissingLocal()
}

// detailOf is what a member's row says under its name: a folder's path,
// or where a repository has copies. A clone is cloned anywhere.
func (f *wizardFrame) detailOf(m groupMember) string {
	if m.folder != nil {
		return m.folder.rootPath
	}
	o := f.byKey[m.source.key()]
	if m.source.kind == "local" {
		if o != nil && len(o.copies) > 0 {
			return o.copies[0].rootPath
		}
		return f.machineLabel(m.source.machine)
	}
	if m.source.preferLocal {
		return f.machinesOf(o)
	}
	return ""
}

// machinesOf names the machines with a copy of a repository, this one
// first, three by name and then a count.
func (f *wizardFrame) machinesOf(o *memberOption) string {
	if o == nil || len(o.copies) == 0 {
		return locales.WizardGithubOnly()
	}
	copies := slices.Clone(o.copies)
	slices.SortStableFunc(copies, func(x, y memberCopy) int {
		switch {
		case x.machine == f.current && y.machine != f.current:
			return -1
		case y.machine == f.current && x.machine != f.current:
			return 1
		}
		return 0
	})
	var names []string
	for _, cp := range copies {
		names = append(names, f.machineLabel(cp.machine))
	}
	if len(names) > 3 {
		return locales.WizardMoreMachines(strings.Join(names[:3], " · "), len(names)-3)
	}
	return strings.Join(names, " · ")
}

// repoOf is the GitHub repository a folder is: as its machine answered
// the editor, or else as it published it.
func (f *wizardFrame) repoOf(s groupSource) string {
	if repo := f.a.projectWizard.repos[s.key()]; repo != "" {
		return repo
	}
	for _, pr := range f.a.catalog.Projects {
		if pr.ID == s.machine+":local:"+s.local {
			return pr.RepoFullName
		}
	}
	return ""
}

// wizardMachine is a machine whose folders a project may hold.
type wizardMachine struct {
	id, name string
	online   bool
}

// wizardMachines are the workspace's machines, this one first, then
// those online, then by name.
func (a *App) wizardMachines() []wizardMachine {
	var out []wizardMachine
	for _, id := range a.catalog.MachineIDs {
		out = append(out, wizardMachine{id: id, name: a.machineName(id), online: a.machineReachable(id)})
	}
	current := a.localMachine()
	slices.SortStableFunc(out, func(x, y wizardMachine) int {
		switch {
		case x.id == current:
			return -1
		case y.id == current:
			return 1
		case x.online != y.online:
			if x.online {
				return -1
			}
			return 1
		case lessFold(x.name, y.name):
			return -1
		case lessFold(y.name, x.name):
			return 1
		}
		return 0
	})
	return out
}

// projectWizardDialog is the wizard: a dialog of Lody's editor size,
// 620 wide and min(680px, 88%) tall, its question and progress above,
// the answers between, and Back and the next step below.
func (a *App) projectWizardDialog(c *ui.Context, p *palette) {
	wz := &a.projectWizard
	if !wz.open {
		if a.addProject.selectOnly {
			a.addProject.open = false
		}
		return
	}
	f := a.wizardFrame()
	if !wz.submitting {
		a.detectSubmodules(f)
	}
	steps := wz.steps()
	index := max(0, slices.Index(steps, wz.step))
	heading, lead := "", ""
	switch wz.step {
	case "kind":
		heading, lead = locales.WizardKindTitle(), locales.WizardKindDescription()
	case "machine":
		heading, lead = locales.WizardMachineTitle(), locales.WizardMachineDescription()
	case "sources":
		heading, lead = locales.WizardSourcesGithubTitle(), locales.WizardSourcesGithubDescription()
		if wz.scope.kind == "machine" {
			heading, lead = locales.WizardSourcesLocalTitle(), locales.WizardSourcesLocalDescription(f.machineLabel(wz.scope.machine))
		}
	case "details":
		heading, lead = locales.WizardDetailsTitle(), locales.WizardDetailsDescription()
	}
	w, h := c.Size()
	ui.DialogBase(c, &wz.open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		backdrop.Background(ui.RGBA(0, 0, 0, 0.22))
		panel.Width(min(620, w-80)).Height(min(680, h*0.88)).Radius(14).Border(0.5, p.CardRim).Background(p.Popover).Clip().
			Shadow(0, 22, 70, 0, ui.RGBA(0, 0, 0, 0.25))
		ui.Column(c).Fill().Children(func() {
			sheetHeader(c, p, heading, lead, a.closeProjectWizard)
			wizardProgress(c, p, index, len(steps))
			body := ui.Scroll(c.Key("wizard/"+wz.step)).FillWidth().Grow(1).MinHeight(0).Padding(8, 20).Gap(12)
			body.Children(func() {
				switch wz.step {
				case "kind":
					a.wizardKind(c, p)
				case "machine":
					a.wizardMachineStep(c, p, f)
				case "sources":
					a.wizardSources(c, p, f)
				case "details":
					a.wizardDetails(c, p, f)
				}
				if wz.err != "" {
					formMessage(c, p, "error", func() {
						ui.Text(c, wz.err).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Danger).Selectable()
					})
				}
			})
			a.wizardFooter(c, p, f)
		})
	})
	// A save in flight keeps it open, as Lody's.
	if !wz.open && wz.submitting {
		wz.open = true
	}
}

func (a *App) closeProjectWizard() {
	wz := &a.projectWizard
	if wz.submitting {
		return
	}
	wz.open, wz.err = false, ""
	if a.addProject.selectOnly {
		a.addProject.open = false
	}
}

// wizardProgress is how far along the questions are: a bar in the
// recessed well every value holder takes, filled with the accent ink as
// a checked box is, its fill growing to the step over 180ms on
// ease.standard, and the step in words beside it.
func wizardProgress(c *ui.Context, p *palette, index, count int) {
	label := locales.WizardProgress(index+1, count)
	ui.Row(c.Key("progress")).FillWidth().Padding(4, 20, 8, 20).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
		track := ui.Row(c.Key("track")).Grow(1).Height(6).Radius(3).Background(wellGround(p)).Clip().
			Role(ui.RoleProgress).Range(1, float64(count), float64(index+1)).Label(label)
		if p.Dark {
			track.InsetShadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.4)).InsetShadow(0, 0, 0, 1, ui.RGBA(255, 255, 255, 0.06))
		} else {
			track.InsetShadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.08)).InsetShadow(0, 0, 0, 1, ui.RGBA(28, 28, 28, 0.06))
		}
		track.Children(func() {
			ink := ui.Box(c.Key("ink")).FillHeight().Radius(3)
			k := ink.AnimateWith("fill", float32(index+1)/float32(count), durRegular, easeStandard)
			ink.WidthPercent(100 * k)
			if p.Dark {
				ink.Gradient(p.Primary, p.Primary.Mix(ui.Hex("#000000"), 0.08), 180).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.55))
			} else {
				ink.Gradient(p.Primary.Mix(ui.Hex("#FFFFFF"), 0.1), p.Primary, 180).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18))
			}
		})
		ui.Text(c, label).FontSize(sizeFoot).TextColor(p.Secondary).FontFeatures("tnum").Shrink(0)
	})
}

// wizardChoice is an answer: a raised card, lit from above with a contact
// shadow and no outline, that goes down when pressed the way a secondary
// button does, sinking 1px into the inset shadow over 120ms on
// ease.standard as its fill follows; the answer given is ringed in the
// accent. Its glyph stands on a small raised tile of its own, a step off
// the card, and a chevron ends it.
func wizardChoice(c *ui.Context, p *palette, key string, icon *ui.SVG, title string, selected, disabled bool, badges, body func()) ui.Element {
	b := ui.ButtonBase(c.Key("choice/" + key)).FillWidth().Padding(12).Gap(12).Radius(14).AlignItems(ui.Center).Label(title).Disabled(disabled)
	motion := ui.Animation{Duration: fadeTime, Ease: easeStandard}
	fill := raisedGround(p)
	if b.Hovered() && !disabled {
		fill = fill.Mix(p.Text, 0.03)
	}
	fill = b.AnimateColor("fill", fill, motion)
	down := b.AnimateWith("down", b2f(b.Pressed() && !disabled), fadeTime, easeStandard)
	if ring := b.AnimateWith("ring", b2f(selected)*(1-down), fadeTime, easeStandard); ring > 0 {
		b.Shadow(0, 0, 0, 1.5, p.Primary.Alpha(ring))
	}
	up := 1 - down
	if p.Dark {
		b.Gradient(fill.Mix(ui.Hex("#FFFFFF"), 0.04*up), fill, 180).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.08*up)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.7*up)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.5*up)).
			InsetShadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.4*down)).InsetShadow(0, 0, 0, 1, ui.RGBA(255, 255, 255, 0.06*down))
	} else {
		b.Gradient(fill, fill.Mix(ui.Hex("#1C1C1C"), 0.03*up), 180).
			Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.16*up)).Shadow(0, 1, 1, 0, ui.RGBA(28, 28, 28, 0.06*up)).Shadow(0, 2, 4, -1, ui.RGBA(28, 28, 28, 0.07*up)).
			InsetShadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.08*down)).InsetShadow(0, 0, 0, 1, ui.RGBA(28, 28, 28, 0.06*down))
	}
	if disabled {
		b.Opacity(0.5)
	} else {
		b.Cursor(ui.CursorPointer)
		pressable(b, 1)
	}
	b.Children(func() {
		tile := ui.Box(c).Size(38, 38).Radius(10).Shrink(0).Center()
		ground := ui.Hex("#FFFFFF")
		if p.Dark {
			ground = ui.Hex("#101010")
		}
		raisedLook(tile, p, ground)
		tile.Children(func() { ui.Icon(c, icon).Size(18, 18).TextColor(p.Secondary) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine().Shrink(1).MinWidth(0)
				if badges != nil {
					badges()
				}
			})
			if body != nil {
				body()
			}
		})
		ui.Icon(c, iconChevronRight).Size(16, 16).TextColor(p.Tertiary).Shrink(0)
	})
	return b
}

// choiceText is an answer's explanation.
func choiceText(c *ui.Context, p *palette, s string) {
	ui.Text(c, s).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
}

// wizardBadge is @lody/ui's neutral badge: a 20px chip of a fact.
func wizardBadge(c *ui.Context, p *palette, key, s string) {
	ui.Row(c.Key("badge/"+key)).Height(20).Padding(0, 6).Radius(5).Background(p.Text.Alpha(0.08)).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, s).FontSize(sizeCaption).FontWeight(500).TextColor(p.Text).FontFeatures("tnum").SingleLine()
	})
}

// formMessage is Lody's FormMessage: a note tinted 10% with its tone,
// the alert triangle before it.
func formMessage(c *ui.Context, p *palette, tone string, body func()) {
	ink := p.Danger
	if tone == "warning" {
		ink = p.Warning
	}
	ui.Row(c.Key("message/"+tone)).FillWidth().Padding(8, 12).Gap(8).Radius(10).Background(ink.Alpha(0.1)).AlignItems(ui.Start).Role(ui.RoleStatus).Children(func() {
		ui.Icon(c, iconTriangleAlert).Size(14, 14).TextColor(ink).Shrink(0).Margin(2, 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(body)
	})
}

// wizardSection is Lody's form Section: a title, a hint under it, then
// what it holds.
func wizardSection(c *ui.Context, p *palette, key, title, hint string, fn func()) {
	ui.Column(c.Key("section/" + key)).FillWidth().Gap(8).Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, title).FontSize(sizeSub).FontWeight(500).TextColor(p.Text)
			if hint != "" {
				ui.Text(c, hint).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary)
			}
		})
		fn()
	})
}

// wizardKind asks what the project holds: folders on a machine, which
// needs a machine, or GitHub repositories only.
func (a *App) wizardKind(c *ui.Context, p *palette) {
	wz := &a.projectWizard
	hasMachines := len(a.catalog.MachineIDs) > 0
	// An edit marks the answer the project had.
	previous := func(kind string) func() {
		if wz.previous == nil || wz.previous.scope.kind != kind {
			return nil
		}
		return func() { wizardBadge(c, p, "previous", locales.WizardPrevious()) }
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		hint := locales.WizardKindLocalHint()
		if !hasMachines {
			hint = locales.WizardKindNoMachines()
		}
		wizardChoice(c, p, "local", iconLaptop, locales.WizardKindLocal(), wz.scope.kind == "machine", !hasMachines, previous("machine"), func() { choiceText(c, p, hint) }).
			OnClick(func() {
				scope := wz.scope
				if scope.kind != "machine" {
					scope = groupScope{}
				}
				wz.goTo("machine", scope)
			})
		wizardChoice(c, p, "github", iconGithub, locales.WizardKindGithub(), wz.scope.kind == "github", false, previous("github"), func() { choiceText(c, p, locales.WizardKindGithubHint()) }).
			OnClick(func() { a.chooseGroupScope(groupScope{kind: "github"}) })
	})
}

// chooseGroupScope answers where the project runs, dropping what it no
// longer fits, and goes on to its sources.
func (a *App) chooseGroupScope(scope groupScope) {
	wz := &a.projectWizard
	wz.draft = fitScope(wz.draft, scope, a.wizardFrame())
	wz.alsoFolder = map[string]bool{}
	wz.goTo("sources", scope)
}

// wizardMachineStep asks which machine the folders are on: each machine
// with whether it is online and how many folders it has.
func (a *App) wizardMachineStep(c *ui.Context, p *palette, f *wizardFrame) {
	wz := &a.projectWizard
	counts := map[string]int{}
	for _, o := range f.options {
		for _, cp := range o.copies {
			counts[cp.machine]++
		}
	}
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		for _, m := range a.wizardMachines() {
			selected := wz.scope.kind == "machine" && wz.scope.machine == m.id
			badges := func() {
				if m.id == f.current {
					wizardBadge(c, p, "this", locales.WizardThisMachine())
				}
				if prev := wz.previous; prev != nil && prev.scope.kind == "machine" && prev.scope.machine == m.id {
					wizardBadge(c, p, "previous", locales.WizardPrevious())
				}
			}
			body := func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
					dot, status := p.Tertiary, locales.Offline()
					if m.online {
						dot, status = p.Success, locales.Online()
					}
					ui.Box(c).Size(6, 6).Radius(3).Background(dot).Shrink(0)
					choiceText(c, p, status+" · "+locales.WizardMachineFolders(counts[m.id]))
				})
			}
			wizardChoice(c, p, "machine/"+m.id, iconLaptop, m.name, selected, false, badges, body).
				OnClick(func() { a.chooseGroupScope(groupScope{kind: "machine", machine: m.id}) })
		}
	})
}

// pickRow is a row of a picking list: a folder, a machine's copy of a
// repository, or a GitHub repository.
type pickRow struct {
	key, label, detail string
	icon               *ui.SVG
	selected           bool
	previous           bool // part of the project before this edit
	toggle             func()
}

// pickList is a card of ticked rows, or a line that says why there are
// none. The whole row ticks, as Lody's label does.
func pickList(c *ui.Context, p *palette, key string, rows []pickRow, empty string) {
	if len(rows) == 0 {
		ui.Column(c.Key(key)).FillWidth().Radius(10).Border(1, p.Border).Background(p.Canvas).Shrink(0).Children(func() {
			ui.Text(c, empty).FontSize(sizeFoot).LineHeight(1.5).TextColor(p.Secondary).TextAlign(ui.Center).FillWidth().Padding(24, 16)
		})
		return
	}
	ui.Column(c.Key(key)).FillWidth().Radius(10).Border(1, p.Border).Background(p.Canvas).Clip().Dividers(1, p.Border).Shrink(0).Children(func() {
		for _, r := range rows {
			row := ui.ButtonBase(c.Key(r.key)).FillWidth().Padding(8, 12).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Label(r.label).Role(ui.RoleCheckBox).Checked(r.selected).OnClick(r.toggle)
			if row.Hovered() {
				row.Background(p.Hover)
			}
			// The whole path, which the line may cut short, as Lody's title on
			// it; told by the row, as a tip of the line would take its press.
			lodyTooltip(c, p, row, r.detail)
			row.Children(func() {
				shareCheckbox(c, p, r.selected)
				ui.Icon(c, r.icon).Size(14, 14).TextColor(p.Secondary).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, r.label).FontSize(sizeSub).TextColor(p.Text).SingleLine()
					if r.detail != "" {
						ui.Text(c, r.detail).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine()
					}
				})
				if r.previous {
					wizardBadge(c, p, "previous", locales.WizardPrevious())
				}
			})
		}
	})
}

// wizardTab is a tab of the sources: its label and how many it has ticked.
type wizardTab struct {
	id, label string
	count     int
}

// wizardTabs is @lody/ui's small tab strip, stretched: the tabs share the
// width of a flat tray, and the raised pill under the one chosen slides
// and stretches to the next over 180ms on ease.standard, the labels' ink
// fading over 120ms. The pill and the tabs both lie over the tray, the
// tabs after, as what is placed absolutely lies above what flows.
func wizardTabs(c *ui.Context, p *palette, tabs []wizardTab, current string, pick func(string)) {
	tray, raised := ui.RGBA(28, 28, 28, 0.06), ui.Hex("#FFFFFF")
	if p.Dark {
		tray, raised = ui.RGBA(255, 255, 255, 0.06), ui.Hex("#363636")
	}
	ui.Row(c.Key("tabs")).FillWidth().Height(28).Radius(8).Background(tray).Shrink(0).Role(ui.RoleTabList).Children(func() {
		pill := ui.Box(c.Key("pill")).Absolute().Top(2).Height(24).Radius(6)
		raisedLook(pill, p, raised)
		var first, sel ui.Rect
		ui.Row(c.Key("row")).Absolute().Top(2).Left(2).Right(2).Height(24).Gap(2).Children(func() {
			for i, t := range tabs {
				tab := ui.ButtonBase(c.Key(t.id)).Grow(1).Basis(0).MinWidth(0).Height(24).Padding(0, 10).Radius(6).Gap(6).
					AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer).Label(t.label).Role(ui.RoleTab).Checked(t.id == current).
					OnClick(func() { pick(t.id) })
				if b := tab.Bounds(); i == 0 {
					first = b
				}
				if t.id == current {
					sel = tab.Bounds()
				}
				ink := tab.AnimateColor("ink", map[bool]ui.Color{true: p.Text, false: p.Secondary}[t.id == current || tab.Hovered()], ui.Animation{Duration: durFast, Ease: easeStandard})
				tab.Children(func() {
					ui.Text(c, t.label).FontSize(sizeFoot).FontWeight(500).TextColor(ink).SingleLine().Shrink(1).MinWidth(0)
					if t.count > 0 {
						wizardBadge(c, p, t.id, itoa(t.count))
					}
				})
			}
		})
		if sel.W == 0 {
			pill.Width(0).Opacity(0)
			return
		}
		x := pill.AnimateWith("x", sel.X-first.X, durRegular, easeStandard)
		w := pill.AnimateWith("w", sel.W, durRegular, easeStandard)
		pill.Left(2 + x).Width(w)
	})
}

// wizardSources asks for the folders and repositories: on a machine, its
// folders on one tab and GitHub's repositories on the other; for GitHub
// only, the repositories alone. A folder joins as its machine's folder,
// even with a GitHub remote, so the project runs there only; the GitHub
// tab is for clones, which win over the same repository's folder.
func (a *App) wizardSources(c *ui.Context, p *palette, f *wizardFrame) {
	wz := &a.projectWizard
	scope := wz.scope
	members := map[string]groupMember{}
	for _, m := range wz.draft.members {
		members[m.source.key()] = m
	}
	swap := func(from groupMember, source groupSource, label string) {
		wz.draft = wz.draft.remove(from.id, f.labelOf).add(source, label)
	}
	type folderOf struct {
		key    string
		source *groupSource // none for a folder picked from the browser
		label  string
	}
	folderOfRepo := map[string]folderOf{}
	var folderRows []pickRow
	if scope.kind == "machine" {
		for _, o := range f.options {
			i := slices.IndexFunc(o.copies, func(cp memberCopy) bool { return cp.machine == scope.machine })
			if i < 0 {
				continue
			}
			cp := o.copies[i]
			source, name, detail, icon, repoKey := o.source, o.label, cp.rootPath, iconFolder, ""
			if o.kind == "repository" {
				source = localSource(cp.machine, cp.local)
				parts := strings.Split(o.label, "/")
				name, icon, repoKey = parts[len(parts)-1], iconFolderGit2, o.key
				detail = o.label
				if cp.rootPath != "" {
					detail += " · " + cp.rootPath
				}
			}
			folderKey := source.key()
			folder, hasFolder := members[folderKey]
			repo, hasRepo := members[repoKey]
			if repoKey == "" {
				hasRepo = false
			}
			// A copy-preferring repository member from before is this folder.
			legacy := hasRepo && repo.source.kind == "github" && repo.source.preferLocal
			both := wz.alsoFolder[folderKey] && hasRepo
			if repoKey != "" {
				src := source
				folderOfRepo[repoKey] = folderOf{key: folderKey, source: &src, label: name}
			}
			// Before this edit it was this folder, or its repository from a
			// copy of it.
			previous := wz.prevUsage(folderKey) != "" || o.kind == "repository" && wz.prevUsage(o.key) == "localCopy"
			folderRows = append(folderRows, pickRow{key: folderKey, label: name, detail: detail, icon: icon, selected: hasFolder || legacy || both, previous: previous, toggle: func() {
				switch {
				case both:
					wz.setAlso(folderKey, false)
				case hasFolder:
					wz.draft = wz.draft.remove(folder.id, f.labelOf)
				case legacy:
					wz.draft = wz.draft.remove(repo.id, f.labelOf)
				case hasRepo:
					// Ticked as a GitHub repository already, which wins: only noted.
					wz.setAlso(folderKey, true)
				default:
					wz.draft = wz.draft.add(source, name)
				}
			}})
		}
		// A folder picked from the browser is not a listed project yet: it
		// shows, ticked, until saving registers it.
		listed := map[string]bool{}
		for _, r := range folderRows {
			listed[r.key] = true
		}
		for _, m := range wz.draft.members {
			key := m.source.key()
			if m.folder == nil || m.folder.machine != scope.machine || listed[key] {
				continue
			}
			icon := iconFolder
			if m.folder.repo != "" {
				icon = iconFolderGit2
				folderOfRepo["github:"+strings.ToLower(m.folder.repo)] = folderOf{key: key, label: f.labelOf(m)}
			}
			folderRows = append([]pickRow{{key: key, label: f.labelOf(m), detail: m.folder.rootPath, icon: icon, selected: true, toggle: func() {
				wz.draft = wz.draft.remove(m.id, f.labelOf)
			}}}, folderRows...)
		}
	}
	// Every repository GitHub can clone, and a member cloned from one it no
	// longer lists, so it can still be unticked.
	var repoRows []pickRow
	for _, o := range f.options {
		member, has := members[o.key]
		if o.kind != "repository" || !o.onGithub && !(has && member.source.kind == "github") {
			continue
		}
		fo, hasFO := folderOfRepo[o.key]
		var folder groupMember
		hasFolder := false
		if hasFO {
			folder, hasFolder = members[fo.key]
		}
		// A copy-preferring member from before on this machine is the folder.
		selected := has && member.source.kind == "github" && !(member.source.preferLocal && hasFO)
		label := o.label
		repoRows = append(repoRows, pickRow{key: o.key, label: label, icon: iconGithub, selected: selected, previous: wz.prevUsage(o.key) == "worktree", toggle: func() {
			switch {
			case selected:
				// Still ticked as a folder: it goes back to being that folder.
				if hasFO && fo.source != nil && wz.alsoFolder[fo.key] {
					swap(member, *fo.source, fo.label)
					wz.setAlso(fo.key, false)
				} else {
					wz.draft = wz.draft.remove(member.id, f.labelOf)
				}
			case hasFolder:
				// Ticked as a folder too: GitHub wins, so it becomes a clone.
				swap(folder, githubSource(label), label)
				if fo.source != nil {
					wz.setAlso(fo.key, true)
				}
			case has:
				swap(member, githubSource(label), label)
				if hasFO && fo.source != nil {
					wz.setAlso(fo.key, true)
				}
			default:
				wz.draft = wz.draft.add(githubSource(label), label)
			}
		}})
	}
	needle := strings.ToLower(strings.TrimSpace(wz.query))
	shown := func(rows []pickRow) []pickRow {
		if needle == "" {
			return rows
		}
		var out []pickRow
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.label), needle) || strings.Contains(strings.ToLower(r.detail), needle) {
				out = append(out, r)
			}
		}
		return out
	}
	machine := f.machineLabel(scope.machine)
	canBrowse := scope.kind == "machine" && a.ownsMachine(scope.machine) && a.machineReachable(scope.machine)
	search := func() {
		ui.Row(c.Key("toolbar")).FillWidth().Gap(8).AlignItems(ui.Center).MinWidth(0).Shrink(0).Children(func() {
			field := ui.Row(c.Key("search")).Grow(1).MinWidth(0).Height(30).Padding(0, 10).Gap(8).Radius(8).Background(p.Text.Alpha(0.05)).AlignItems(ui.Center)
			field.Children(func() {
				ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.Tertiary).Shrink(0)
				ui.TextInputBase(c, &wz.query).Placeholder(locales.Search()).Label(locales.Search()).FontSize(sizeSub).Grow(1)
			})
			if scope.kind == "machine" && wz.tab == "folders" {
				b := outlineButton(c, p, iconFolderSearch, locales.WizardBrowse()).Disabled(!canBrowse)
				if canBrowse {
					b.OnClick(func() { a.openGroupFolderPicker(scope.machine) })
				} else {
					b.Opacity(0.45)
					tip(c, p, b, locales.WizardBrowseUnavailable())
				}
			}
		})
	}
	emptyRepos := locales.WizardNoRepositories()
	if needle != "" {
		emptyRepos = locales.WizardNoMatches()
	}
	if scope.kind != "machine" {
		search()
		pickList(c, p, "repositories", shown(repoRows), emptyRepos)
		return
	}
	count := func(rows []pickRow) int {
		n := 0
		for _, r := range rows {
			if r.selected {
				n++
			}
		}
		return n
	}
	wizardTabs(c, p, []wizardTab{
		{id: "folders", label: locales.WizardFoldersTab(machine), count: count(folderRows)},
		{id: "github", label: locales.WizardGithubTab(), count: count(repoRows)},
	}, wz.tab, func(id string) { wz.tab = id })
	search()
	a.wizardBothNotice(c, p, f)
	note := func(s string) { ui.Text(c, s).FontSize(sizeFoot).LineHeight(1.45).TextColor(p.Secondary) }
	if wz.tab == "github" {
		pickList(c, p, "repositories", shown(repoRows), emptyRepos)
		note(locales.WizardGithubNote())
		return
	}
	empty := locales.WizardNoFolders(machine)
	switch {
	case needle != "":
		empty = locales.WizardNoMatches()
	case canBrowse:
		empty = locales.WizardNoFoldersBrowse(machine)
	}
	pickList(c, p, "folders", shown(folderRows), empty)
	note(locales.WizardFoldersNote())
}

// wizardBothNotice notes the repositories ticked both as clones and as
// their folder on the machine: one copy is kept, GitHub's.
func (a *App) wizardBothNotice(c *ui.Context, p *palette, f *wizardFrame) {
	wz := &a.projectWizard
	if wz.scope.kind != "machine" {
		return
	}
	var names []string
	for _, m := range wz.draft.members {
		if m.usage() != "worktree" {
			continue
		}
		o := f.byKey[m.source.key()]
		if o == nil {
			continue
		}
		if slices.ContainsFunc(o.copies, func(cp memberCopy) bool {
			return cp.machine == wz.scope.machine && wz.alsoFolder[localSource(cp.machine, cp.local).key()]
		}) {
			names = append(names, f.labelOf(m))
		}
	}
	if len(names) == 0 {
		return
	}
	formMessage(c, p, "warning", func() {
		ui.Text(c, locales.WizardBothSelected(strings.Join(names, "、"), len(names))).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Text)
	})
}

// wizardDetails names the project and shows what it includes, each
// member removable, then the instructions the agent gets first.
func (a *App) wizardDetails(c *ui.Context, p *palette, f *wizardFrame) {
	wz := &a.projectWizard
	v := wz.draft.validate(f.repoOf, a.takenGroupNames())
	wizardSection(c, p, "name", locales.WizardName(), "", func() {
		name := input(c, p, &wz.draft.name, locales.WizardNamePlaceholder(), locales.WizardName()).AutoFocus().OnSubmit(a.saveProjectGroup)
		if v.nameRequired || v.nameTaken {
			name.Border(1, p.Danger.Alpha(0.6))
		}
		if v.nameTaken {
			formMessage(c, p, "error", func() {
				ui.Text(c, locales.WizardNameTaken()).FontSize(sizeSub).TextColor(p.Danger)
			})
		}
	})
	wizardSection(c, p, "members", locales.WizardMembers(len(wz.draft.members)), "", func() {
		card(c, p, func() {
			for _, m := range wz.draft.members {
				icon := map[string]*ui.SVG{"inPlace": iconFolder, "localCopy": iconFolderGit2, "worktree": iconGithub}[m.usage()]
				label, detail := f.labelOf(m), f.detailOf(m)
				ui.Row(c.Key(m.id)).FillWidth().Padding(8, 8, 8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icon).Size(14, 14).TextColor(p.Secondary).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, label).FontSize(sizeSub).TextColor(p.Text).SingleLine()
						shown := detail
						if code := v.memberErrors[m.id]; code != "" {
							shown = groupMemberError(code)
						}
						if shown != "" {
							tip(c, p, ui.Text(c, shown).FontSize(sizeFoot).TextColor(p.Tertiary).SingleLine(), detail)
						}
					})
					remove := locales.WizardRemove(label)
					b := ui.ButtonBase(c.Key("remove")).Size(28, 28).Radius(8).Center().Shrink(0).Cursor(ui.CursorPointer).Label(remove).Transition(colorFade)
					ink := p.Secondary
					if b.Hovered() {
						b.Background(p.Hover)
						ink = p.Text
					}
					b.OnClick(func() {
						wz.draft = wz.draft.remove(m.id, f.labelOf)
						if key := m.source.key(); wz.alsoFolder[key] {
							wz.setAlso(key, false)
						}
					})
					b.Children(func() { ui.Icon(c, iconX).Size(14, 14).TextColor(ink) })
				})
			}
		})
		a.wizardBothNotice(c, p, f)
		if len(v.memberErrors) > 0 {
			formMessage(c, p, "error", func() {
				for _, m := range wz.draft.members {
					if code := v.memberErrors[m.id]; code != "" {
						ui.Text(c, "• "+locales.WizardMemberErrorLine(f.labelOf(m), groupMemberError(code))).FontSize(sizeSub).LineHeight(1.45).TextColor(p.Danger)
					}
				}
			})
		}
	})
	wizardSection(c, p, "instructions", locales.WizardInstructions(), locales.WizardInstructionsHint(), func() {
		area(c, p, &wz.draft.instructions, locales.WizardInstructionsPlaceholder(), locales.WizardInstructionsLabel(), 4)
	})
}

// wizardFooter is the wizard's buttons: Cancel on the first question,
// Back after it, and the way on. With one folder or repository the
// sources finish there, as an ordinary project; the count of what is
// ticked stands at the start.
func (a *App) wizardFooter(c *ui.Context, p *palette, f *wizardFrame) {
	wz := &a.projectWizard
	// Only creating finishes with one: an edit stays a project of several.
	single := len(wz.draft.members) == 1 && wz.editing == nil
	ui.Row(c.Key("footer")).FillWidth().Padding(12, 20, 16, 20).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		if wz.step == "sources" {
			count := locales.WizardSelectedCount(len(wz.draft.members))
			if single {
				count = locales.WizardSingleHint()
			}
			ui.Text(c, count).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1).MinWidth(0).Role(ui.RoleStatus)
		} else {
			ui.Spacer(c)
		}
		if wz.step == "kind" {
			outlineButton(c, p, nil, locales.Cancel()).Disabled(wz.submitting).OnClick(a.closeProjectWizard)
		} else {
			outlineButton(c, p, iconArrowLeft, locales.WizardBack()).Disabled(wz.submitting).OnClick(wz.back)
		}
		if wz.submitting {
			spinner(c).Size(14, 14)
		}
		switch {
		case wz.step == "sources" && single:
			primaryButton(c, p, locales.Done(), !wz.submitting).OnClick(a.finishSingleProject)
		case wz.step == "sources":
			primaryButton(c, p, locales.WizardNext(), len(wz.draft.members) > 0).OnClick(func() {
				draft := wz.draft
				if wz.scope.kind != "" {
					draft = fitScope(draft, wz.scope, f)
				}
				draft.name = draft.suggestName(f.labelOf, a.takenGroupNames())
				wz.draft = draft
				wz.goTo("details", wz.scope)
			})
		case wz.step == "details":
			valid := wz.draft.validate(f.repoOf, a.takenGroupNames()).valid
			label := locales.WizardCreate()
			if wz.editing != nil {
				label = locales.Save()
			}
			primaryButton(c, p, label, valid && !wz.submitting).OnClick(a.saveProjectGroup)
		}
	})
}

// The wizard's errors that Lody words itself.
var (
	errFolderChanged           = errors.New("folder changed")
	errFolderUpdateRequired    = errors.New("folder selection unsupported")
	errLocalProjectUnavailable = errors.New("Local project is unavailable")
)

// wizardErrorText is what an error says in the wizard.
func wizardErrorText(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return locales.WizardTimeout()
	case errors.Is(err, errFolderChanged):
		return locales.WizardFolderChanged()
	case errors.Is(err, errFolderUpdateRequired):
		return locales.WizardFolderUpdateRequired()
	}
	return err.Error()
}

// wizardControl asks a machine's daemon over local-project/control, and
// wizardWrite edits the workspace's stream, or a machine's when one is
// named: the app's connection and streams, which tests stand in for.
var (
	wizardControl = func(conn *cloud.Conn, ctx context.Context, machine string, request map[string]any) (json.RawMessage, error) {
		if conn == nil {
			return nil, catalog.ErrNotReady
		}
		return conn.ProjectControl(ctx, machine, request, 30*time.Second)
	}
	wizardWrite = func(sync *catalog.Sync, ctx context.Context, machine string, edit func(f *flock.Flock) error) error {
		if sync == nil {
			return catalog.ErrNotReady
		}
		if machine == "" {
			return sync.WriteWorkspace(ctx, edit)
		}
		return sync.WriteMachine(ctx, machine, edit)
	}
)

// preparedFolder is a machine's answer to local-project/prepare-add: the
// local project a folder is, or would be, and, when asked to inspect it,
// whether it is a Git repository, its GitHub remote and its submodules.
type preparedFolder struct {
	LocalProjectID    string `json:"localProjectId"`
	Name              string `json:"name"`
	RootPath          string `json:"rootPath"`
	AlreadyRegistered bool   `json:"alreadyRegistered"`
	Inspection        *struct {
		IsGit        *bool  `json:"isGit"`
		RepoFullName string `json:"repoFullName"`
		Submodules   []struct {
			Path         string `json:"path"`
			URL          string `json:"url"`
			RepoFullName string `json:"repoFullName"`
		} `json:"submodules"`
	} `json:"inspection"`
}

// submodules are those the machine inspected, each with the repository
// it points at.
func (pf preparedFolder) submodules() []detectedSubmodule {
	if pf.Inspection == nil {
		return nil
	}
	var out []detectedSubmodule
	for _, s := range pf.Inspection.Submodules {
		out = append(out, detectedSubmodule{path: s.Path, repo: firstNonEmpty(s.RepoFullName, submoduleRepo(s.URL, pf.Inspection.RepoFullName))})
	}
	return out
}

// git is whether the machine found a Git repository: older machines
// inspected Git repositories only, and did not say.
func (pf preparedFolder) git() bool {
	return pf.Inspection != nil && (pf.Inspection.IsGit == nil || *pf.Inspection.IsGit)
}

// prepareFolder asks a machine to prepare a folder as a local project,
// without registering it.
func prepareFolder(ctx context.Context, conn *cloud.Conn, workspace, machine, root string, inspect bool) (preparedFolder, error) {
	request := map[string]any{"type": "local-project/prepare-add", "workspaceId": workspace, "rootPath": root}
	if inspect {
		request["inspect"] = true
	}
	var prep preparedFolder
	raw, err := wizardControl(conn, ctx, machine, request)
	if err == nil {
		err = json.Unmarshal(raw, &prep)
	}
	return prep, err
}

// putLocalProject registers a prepared folder on its machine's stream, as
// Lody's prepareAndWriteLocalProject: a ['localProject', id] row unless
// there is one, the GitHub remote the machine inspected, and shown among
// the single projects unless it is only a project's member.
func putLocalProject(f *flock.Flock, prep preparedFolder, visible bool, now float64) error {
	key := []any{"localProject", prep.LocalProjectID}
	cur, ok, err := f.Get(key)
	if err != nil {
		return err
	}
	row, _ := cur.(map[string]any)
	switch {
	case ok && row != nil:
		row = maps.Clone(row)
	case prep.AlreadyRegistered:
		return errLocalProjectUnavailable
	default:
		row = map[string]any{"id": prep.LocalProjectID, "name": prep.Name, "rootPath": prep.RootPath, "createdAtMs": now, "lastOpenedAtMs": now}
		if !visible {
			row["standaloneVisible"] = false
		}
	}
	if prep.Inspection != nil {
		if repo := prep.Inspection.RepoFullName; repo != "" {
			row["githubRepoFullName"] = repo
		} else {
			delete(row, "githubRepoFullName")
		}
	}
	if visible && row["standaloneVisible"] == false {
		row["standaloneVisible"] = true
	}
	return f.Put(key, row)
}

// showLocalProject lists a machine's folder among the single projects
// again, one only projects held.
func showLocalProject(f *flock.Flock, local string) error {
	key := []any{"localProject", local}
	cur, ok, err := f.Get(key)
	row, _ := cur.(map[string]any)
	switch {
	case err != nil:
		return err
	case !ok || row == nil:
		return errLocalProjectUnavailable
	case row["standaloneVisible"] != false:
		return nil
	}
	row = maps.Clone(row)
	row["standaloneVisible"] = true
	return f.Put(key, row)
}

// saveProjectGroup creates the project: each folder picked from the
// browser registered on its machine first, as its machine prepares it
// again, then the project's row on the workspace's stream.
func (a *App) saveProjectGroup() {
	wz := &a.projectWizard
	f := a.wizardFrame()
	if wz.submitting || wz.step != "details" || a.account == nil || !wz.draft.validate(f.repoOf, a.takenGroupNames()).valid {
		return
	}
	conn, sync, ws, gen := a.conn, a.sync, a.workspaceID, wz.gen
	draft, row := wz.draft, wz.draft.row(wz.id, a.account.User.ID, nowMs(), wz.editing)
	created, id := wz.onCreated, wz.id
	if wz.editing != nil {
		created = nil
	}
	wz.submitting, wz.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := func() error {
			for _, m := range draft.members {
				folder := m.folder
				if folder == nil {
					continue
				}
				prep, err := prepareFolder(ctx, conn, ws, folder.machine, folder.rootPath, true)
				if err != nil {
					return err
				}
				if prep.LocalProjectID != folder.local || prep.Inspection == nil || prep.git() != folder.isGit || prep.Inspection.RepoFullName != folder.repo {
					return errFolderChanged
				}
				if err := wizardWrite(sync, ctx, folder.machine, func(fl *flock.Flock) error { return putLocalProject(fl, prep, false, nowMs()) }); err != nil {
					return err
				}
			}
			return wizardWrite(sync, ctx, "", func(fl *flock.Flock) error { return fl.Put([]any{"project", row["id"]}, row) })
		}()
		a.update(func() {
			if wz.gen != gen {
				return
			}
			wz.submitting = false
			if err != nil {
				wz.err = wizardErrorText(err)
				return
			}
			a.closeProjectWizard()
			if created != nil {
				created(id)
			}
		})
	}()
}

// finishSingleProject ends the wizard with one folder or repository, which
// stays an ordinary project: a picked folder is registered and listed, a
// folder only projects held is listed again, and a GitHub repository
// needs nothing at all.
func (a *App) finishSingleProject() {
	wz := &a.projectWizard
	if wz.submitting || len(wz.draft.members) != 1 {
		return
	}
	f := a.wizardFrame()
	draft := wz.draft
	if wz.scope.kind != "" {
		draft = fitScope(draft, wz.scope, f)
	}
	if len(draft.members) != 1 {
		return
	}
	m := draft.members[0]
	label := f.labelOf(m)
	done := func() {
		if m.source.kind == "github" && m.folder == nil {
			a.toast("success", locales.WizardSingleRepository(label))
		} else {
			a.toast("success", locales.WizardSingleFolder(label))
		}
		a.closeProjectWizard()
	}
	if m.folder == nil && m.source.kind == "github" {
		done()
		return
	}
	conn, sync, ws, gen := a.conn, a.sync, a.workspaceID, wz.gen
	wz.submitting, wz.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		if folder := m.folder; folder != nil {
			var prep preparedFolder
			if prep, err = prepareFolder(ctx, conn, ws, folder.machine, folder.rootPath, false); err == nil {
				err = wizardWrite(sync, ctx, folder.machine, func(fl *flock.Flock) error { return putLocalProject(fl, prep, true, nowMs()) })
			}
		} else {
			err = wizardWrite(sync, ctx, m.source.machine, func(fl *flock.Flock) error { return showLocalProject(fl, m.source.local) })
		}
		a.update(func() {
			if wz.gen != gen {
				return
			}
			wz.submitting = false
			if err != nil {
				wz.err = wizardErrorText(err)
				return
			}
			done()
		})
	}()
}

// openGroupFolderPicker browses the chosen machine, and only it, for a
// folder to put in the project, so a picked folder cannot bind the
// project to another machine.
func (a *App) openGroupFolderPicker(machine string) {
	a.addProject = addProjectForm{open: true, machine: machine, selectOnly: true}
	a.browseFolder("")
}

// pickGroupFolder takes the folder the browser shows for the project. A
// machine that can inspect a folder without registering it leaves it a
// draft until the project is saved; an older one registers it now,
// hidden from the single projects as a save would, its GitHub remote
// coming when the machine publishes it.
func (a *App) pickGroupFolder() {
	fm, wz := &a.addProject, &a.projectWizard
	machine, root := fm.machine, fm.path
	conn, sync, ws, gen := a.conn, a.sync, a.workspaceID, wz.gen
	canInspect := a.machineCap(machine, "projectFolderSelection") >= 1
	fm.busy, fm.err = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		prep, err := prepareFolder(ctx, conn, ws, machine, root, canInspect)
		switch {
		case err != nil:
		case !canInspect:
			err = wizardWrite(sync, ctx, machine, func(fl *flock.Flock) error { return putLocalProject(fl, prep, false, nowMs()) })
		case prep.Inspection == nil:
			err = errFolderUpdateRequired
		}
		a.update(func() {
			fm.busy = false
			if !wz.open || wz.gen != gen {
				fm.err = locales.WizardInvalid()
				return
			}
			if err != nil {
				fm.err = wizardErrorText(err)
				return
			}
			var next groupDraft
			if canInspect {
				next = wz.draft.addFolder(groupFolder{machine: machine, local: prep.LocalProjectID, name: prep.Name, rootPath: prep.RootPath,
					isGit: prep.git(), repo: prep.Inspection.RepoFullName, submodules: prep.submodules()})
			} else {
				next = wz.draft.add(localSource(machine, prep.LocalProjectID), prep.Name)
			}
			for _, code := range next.validate(nil, nil).memberErrors {
				if code == "machine_mismatch" {
					fm.err = locales.WizardErrMachineMismatch()
					return
				}
			}
			wz.draft, fm.open = next, false
		})
	}()
}
