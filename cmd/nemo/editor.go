package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/x/ansi"
	"github.com/cnabio/cnab-go/secrets/host"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/docker/cli/opts"

	"github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/nemo"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// editMode is what the editor was opened for: it decides where it starts,
// and whether the stack's name is asked and may be taken.
type editMode int

const (
	modeCreate editMode = iota
	modeEdit
	modeImport
)

// step is a group of screens, shown left to right at the top.
type step int

const (
	stepName step = iota
	stepBundles
	stepTarget
	stepParameters
	stepCredentials
	stepReview
)

var stepTitles = map[step]string{
	stepName: "Name", stepBundles: "Bundles", stepTarget: "Target",
	stepParameters: "Parameters", stepCredentials: "Credentials", stepReview: "Review",
}

// screenKind is one question of the editor.
type screenKind int

const (
	screenName screenKind = iota
	screenSource
	screenNamespace
	screenDirectory
	screenNames
	screenLayouts
	screenVersion
	screenDocker
	screenHost
	screenContext
	screenOrchestrator
	screenImages
	screenParameter
	screenCredential
	screenReview
)

type screen struct {
	kind screenKind
	step step
	// name is the parameter's or the credential's.
	name string
}

// keepRemote is the source of bundles located by a remote the editor does not
// write: it keeps it as it is.
const keepRemote = "remote"

// How nemo reaches the target's Docker: as the environment says, at an
// address, or through a docker context.
const (
	dockerEnvironment = "environment"
	dockerHost        = "host"
	dockerContext     = "context"
)

var dockerKinds = []string{dockerEnvironment, dockerHost, dockerContext}

// editor is the interactive editor of a stack: one screen at a time, grouped
// in steps, then a review of the whole stack that saves it.
type editor struct {
	ctx   context.Context
	mode  editMode
	stack stack.Stack
	// taken tells whether a stack of that name exists already.
	taken   func(name string) bool
	askName bool

	from      string
	docker    string
	namespace string
	directory string
	names     string
	version   string
	versions  []string
	found     map[string]cnab.ExtendedBundle
	chosen    map[string]bool
	bundles   map[string]cnab.ExtendedBundle
	kinds     map[string]string
	// loading says what is being read in the background: versions, layouts or bundles.
	loading map[string]string
	failure string
	visited map[step]bool

	at       screen
	reviewed bool
	input    textinput.Model
	cursor   int
	field    int
	problem  string
	quitting bool
	spin     spinner.Model
	width    int
	saved    bool

	load func(ctx context.Context, source string) (cnab.BundleReference, error)
	tags func(ctx context.Context, repository cnab.OCIReference) ([]string, error)
}

func newEditor(ctx context.Context, edited stack.Stack, mode editMode, taken func(string) bool) *editor {
	e := &editor{
		ctx: ctx, mode: mode, stack: edited, taken: taken,
		askName: mode != modeEdit && (edited.Name == "" || taken(edited.Name)),
		found:   map[string]cnab.ExtendedBundle{}, chosen: map[string]bool{}, bundles: map[string]cnab.ExtendedBundle{},
		kinds: map[string]string{}, visited: map[step]bool{}, loading: map[string]string{},
		input: textinput.New(), spin: spinner.New(spinner.WithSpinner(spinner.Dot)), width: 80,
		load: nemo.LoadBundle, tags: bundle.RegistryTags{}.ListTags,
	}
	e.input.Prompt = "> "
	remote := edited.Remote
	switch {
	case remote == "":
		e.from, e.directory = bundle.Layout, workingDirectory()
	case strings.HasPrefix(remote, "docker://") && strings.HasSuffix(remote, "/{{ .Component }}:{{ .Version }}"):
		e.from = bundle.Registry
		e.namespace = strings.TrimSuffix(strings.TrimPrefix(remote, "docker://"), "/{{ .Component }}:{{ .Version }}")
	case strings.HasPrefix(remote, "oci:") && strings.HasSuffix(remote, "/{{ .Component }}.oci"):
		e.from = bundle.Layout
		e.directory = strings.TrimSuffix(strings.TrimPrefix(remote, "oci:"), "/{{ .Component }}.oci")
	default:
		e.from = keepRemote
	}
	switch {
	case edited.Target.Host != "":
		e.docker = dockerHost
	case edited.Target.Context != "":
		e.docker = dockerContext
	default:
		e.docker = dockerEnvironment
	}
	var names []string
	for _, installation := range edited.Installations {
		names = append(names, installation.Name)
		e.chosen[installation.Name] = true
		e.version = cmp.Or(e.version, installation.Bundle.Version)
	}
	e.names = strings.Join(names, " ")
	for _, strategy := range edited.Credentials {
		e.kinds[strategy.Name] = strategy.Source.Key
	}
	if mode != modeCreate {
		for _, s := range []step{stepName, stepBundles, stepTarget, stepParameters, stepCredentials} {
			e.visited[s] = true
		}
		e.at = screen{kind: screenReview, step: stepReview}
	} else {
		e.at = e.screens()[0]
	}
	e.enter()
	return e
}

func workingDirectory() string {
	directory, _ := os.Getwd()
	return directory
}

func (e *editor) Init() tea.Cmd {
	cmds := []tea.Cmd{e.input.Focus(), e.spin.Tick}
	if e.from == bundle.Layout && e.directory != "" {
		cmds = append(cmds, e.scanLayouts())
	}
	if len(e.stack.Installations) > 0 {
		cmds = append(cmds, e.loadBundles())
		if e.from != bundle.Layout {
			cmds = append(cmds, e.listVersions())
		}
	}
	return tea.Batch(cmds...)
}

// screens are the editor's questions, as the stack is so far: those of the
// bundles depend on where they are, those of the values on what they declare.
func (e *editor) screens() []screen {
	var screens []screen
	if e.askName {
		screens = append(screens, screen{kind: screenName, step: stepName})
	}
	screens = append(screens, screen{kind: screenSource, step: stepBundles})
	switch e.from {
	case bundle.Registry:
		screens = append(screens, screen{kind: screenNamespace, step: stepBundles}, screen{kind: screenNames, step: stepBundles}, screen{kind: screenVersion, step: stepBundles})
	case bundle.Layout:
		screens = append(screens, screen{kind: screenDirectory, step: stepBundles}, screen{kind: screenLayouts, step: stepBundles})
	default:
		screens = append(screens, screen{kind: screenNames, step: stepBundles}, screen{kind: screenVersion, step: stepBundles})
	}
	screens = append(screens, screen{kind: screenDocker, step: stepTarget})
	switch e.docker {
	case dockerHost:
		screens = append(screens, screen{kind: screenHost, step: stepTarget})
	case dockerContext:
		screens = append(screens, screen{kind: screenContext, step: stepTarget})
	}
	screens = append(screens, screen{kind: screenOrchestrator, step: stepTarget}, screen{kind: screenImages, step: stepTarget})
	for _, name := range e.parameterNames() {
		screens = append(screens, screen{kind: screenParameter, step: stepParameters, name: name})
	}
	for _, name := range e.credentialNames() {
		screens = append(screens, screen{kind: screenCredential, step: stepCredentials, name: name})
	}
	return append(screens, screen{kind: screenReview, step: stepReview})
}

// screen is the screen the editor is at; when the stack no longer has it, the review.
func (e *editor) screen() screen {
	if e.index() < 0 {
		e.at = screen{kind: screenReview, step: stepReview}
	}
	return e.at
}

func (e *editor) index() int {
	return slices.Index(e.screens(), e.at)
}

// move goes to the screen offset from the current one.
func (e *editor) move(offset int) {
	screens := e.screens()
	e.at = screens[min(max(0, e.index()+offset), len(screens)-1)]
	e.enter()
}

// loaded are the bundles read of the stack's installations, in their order.
func (e *editor) loaded() []cnab.ExtendedBundle {
	var result []cnab.ExtendedBundle
	for _, installation := range e.stack.Installations {
		if definition, ok := e.bundles[installation.Name]; ok {
			result = append(result, definition)
		}
	}
	return result
}

// parameterNames are the parameters the bundles declare, nemo's own aside.
func (e *editor) parameterNames() []string {
	names := map[string]bool{}
	for _, definition := range e.loaded() {
		for name := range definition.Parameters {
			if !strings.HasPrefix(name, "nemo-") {
				names[name] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(names))
}

func (e *editor) credentialNames() []string {
	names := map[string]bool{}
	for _, definition := range e.loaded() {
		for name := range definition.Credentials {
			names[name] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// parameter is what the bundles say of a parameter: its description, its
// default, and whether one of them requires it.
func (e *editor) parameter(name string) (description, fallback string, required bool) {
	for _, definition := range e.loaded() {
		parameter, ok := definition.Parameters[name]
		if !ok {
			continue
		}
		description = cmp.Or(description, parameter.Description)
		if schema, ok := definition.Definitions[parameter.Definition]; ok && schema.Default != nil && fallback == "" {
			fallback = fmt.Sprint(schema.Default)
		}
		required = required || parameter.Required
	}
	return description, fallback, required
}

func (e *editor) credential(name string) (description string, required bool) {
	for _, definition := range e.loaded() {
		if credential, ok := definition.Credentials[name]; ok {
			description = cmp.Or(description, credential.Description)
			required = required || credential.Required
		}
	}
	return description, required
}

func (e *editor) strategy(name string) (valuesource.Strategy, bool) {
	for _, strategy := range e.stack.Credentials {
		if strategy.Name == name {
			return strategy, true
		}
	}
	return valuesource.Strategy{}, false
}

// problems are what keeps a step from being complete.
func (e *editor) problems(s step) []string {
	var problems []string
	switch s {
	case stepName:
		if err := e.nameProblem(e.stack.Name); err != nil {
			problems = append(problems, err.Error())
		}
	case stepBundles:
		switch {
		case e.stack.Remote == "":
			problems = append(problems, "where the bundles are is not given")
		case len(e.stack.Installations) == 0:
			problems = append(problems, "no bundle is chosen")
		case e.failure != "":
			problems = append(problems, e.failure)
		case e.loading["bundles"] == "" && e.loading["layouts"] == "" && len(e.loaded()) < len(e.stack.Installations):
			problems = append(problems, "the bundles are not read yet")
		}
	case stepTarget:
		switch {
		case e.docker == dockerHost && e.stack.Target.Host == "":
			problems = append(problems, "the address of the Docker is not given")
		case e.docker == dockerContext && e.stack.Target.Context == "":
			problems = append(problems, "the docker context is not given")
		}
		if !slices.Contains([]string{target.Swarm, target.Compose}, e.stack.Target.Orchestrator) {
			problems = append(problems, "no orchestrator")
		}
	case stepParameters:
		for _, name := range e.parameterNames() {
			if _, fallback, required := e.parameter(name); required && e.stack.Parameters[name] == "" && fallback == "" {
				problems = append(problems, name+" is required")
			}
		}
	case stepCredentials:
		for _, name := range e.credentialNames() {
			if _, required := e.credential(name); required {
				if _, given := e.strategy(name); !given {
					problems = append(problems, name+" is required")
				}
			}
		}
	case stepReview:
		if e.loading["bundles"] != "" || e.loading["layouts"] != "" {
			problems = append(problems, "the bundles are being read")
		}
		for _, previous := range e.steps() {
			if previous != stepReview && len(e.problems(previous)) > 0 {
				problems = append(problems, stepTitles[previous]+" is not complete")
			}
		}
	}
	return problems
}

func (e *editor) nameProblem(name string) error {
	switch {
	case !projectName.MatchString(name):
		return errors.New("the name: lowercase letters, digits, dashes and underscores")
	case e.mode != modeEdit && e.taken(name):
		return fmt.Errorf("%s exists: nemo stack edit %s changes it", name, name)
	}
	return nil
}

func (e *editor) steps() []step {
	var steps []step
	for _, screen := range e.screens() {
		if !slices.Contains(steps, screen.step) {
			steps = append(steps, screen.step)
		}
	}
	return steps
}

// enter prepares the current screen: its input holds what the stack has.
func (e *editor) enter() {
	screen := e.screen()
	e.visited[screen.step] = true
	e.problem, e.field, e.cursor = "", 0, 0
	e.input.EchoMode = textinput.EchoNormal
	e.input.Placeholder = ""
	value := ""
	switch screen.kind {
	case screenName:
		value = e.stack.Name
	case screenSource:
		e.cursor = max(0, slices.Index(e.sources(), e.from))
	case screenNamespace:
		value, e.input.Placeholder = e.namespace, "registry.example.com/shop"
	case screenDirectory:
		value = e.directory
	case screenNames:
		value, e.input.Placeholder = e.names, "web database"
	case screenVersion:
		value, e.cursor = e.version, max(0, slices.Index(e.versions, e.version))
	case screenDocker:
		e.cursor = max(0, slices.Index(dockerKinds, e.docker))
	case screenHost:
		value, e.input.Placeholder = e.stack.Target.Host, "ssh://deploy@vm1"
	case screenContext:
		value, e.input.Placeholder = e.stack.Target.Context, "production"
	case screenOrchestrator:
		e.cursor = max(0, slices.Index([]string{target.Swarm, target.Compose}, e.stack.Target.Orchestrator))
	case screenImages:
		e.cursor = max(0, slices.Index([]string{target.ImagesRegistry, target.ImagesLoad}, cmp.Or(e.stack.Target.Images, e.defaultImages())))
	case screenParameter:
		_, fallback, _ := e.parameter(screen.name)
		value = cmp.Or(e.stack.Parameters[screen.name], fallback)
	case screenCredential:
		if strategy, given := e.strategy(screen.name); given {
			value = strategy.Source.Value
		}
		if e.kinds[screen.name] == "" {
			e.kinds[screen.name] = host.SourcePath
		}
		e.echo(screen.name)
	case screenReview:
		e.cursor = len(e.reviewLines()) - 1
		for i, line := range e.reviewLines() {
			if line.problem && line.opens {
				e.cursor = i
				break
			}
		}
	}
	e.input.SetValue(value)
	e.input.CursorEnd()
}

func (e *editor) defaultImages() string {
	if e.from == bundle.Layout {
		return target.ImagesLoad
	}
	return target.ImagesRegistry
}

func (e *editor) sources() []string {
	sources := []string{bundle.Registry, bundle.Layout}
	if e.from == keepRemote {
		sources = append(sources, keepRemote)
	}
	return sources
}

// Messages of the reads the editor runs in the background.
type (
	versionsMsg struct {
		versions []string
		err      error
	}
	layoutsMsg struct {
		directory string
		found     map[string]cnab.ExtendedBundle
		err       error
	}
	bundlesMsg struct {
		bundles map[string]cnab.ExtendedBundle
		err     error
	}
)

func (e *editor) listVersions() tea.Cmd {
	installations := e.stack.Installations
	if len(installations) == 0 {
		return nil
	}
	source, err := e.stack.Source(storage.InstallationSpec{Name: installations[0].Name, Bundle: storage.BundleReference{Version: "0.0.0"}})
	location, locationErr := bundle.ParseLocation(source)
	if err != nil || locationErr != nil || location.Transport != bundle.Registry {
		return nil
	}
	e.loading["versions"] = "Reading the versions of " + installations[0].Name
	return func() tea.Msg {
		reference, err := cnab.ParseOCIReference(location.Reference)
		if err != nil {
			return versionsMsg{err: err}
		}
		repository, err := cnab.ParseOCIReference(reference.Repository())
		if err != nil {
			return versionsMsg{err: err}
		}
		tags, err := e.tags(e.ctx, repository)
		var versions []string
		for _, tag := range tags {
			if _, err := semver.NewVersion(tag); err == nil {
				versions = append(versions, tag)
			}
		}
		slices.SortFunc(versions, func(a, b string) int { return semver.MustParse(b).Compare(semver.MustParse(a)) })
		return versionsMsg{versions: versions, err: err}
	}
}

func (e *editor) scanLayouts() tea.Cmd {
	directory := e.directory
	e.loading["layouts"] = "Reading the bundles of " + directory
	return func() tea.Msg {
		found, err := layoutBundles(e.ctx, directory)
		return layoutsMsg{directory: directory, found: found, err: err}
	}
}

func (e *editor) loadBundles() tea.Cmd {
	edited := e.stack
	e.loading["bundles"], e.failure = "Reading the bundles", ""
	return func() tea.Msg {
		bundles := map[string]cnab.ExtendedBundle{}
		for _, installation := range edited.Installations {
			source, err := edited.Source(installation)
			if err != nil {
				return bundlesMsg{err: err}
			}
			loaded, err := e.load(e.ctx, source)
			if err != nil {
				return bundlesMsg{err: fmt.Errorf("%s: %w", installation.Name, err)}
			}
			bundles[installation.Name] = loaded.Definition
		}
		return bundlesMsg{bundles: bundles}
	}
}

// read records the bundles read, and gives the volume types they use a
// local volume on the target, to change in the manifest for another driver.
func (e *editor) read(bundles map[string]cnab.ExtendedBundle) {
	e.bundles = bundles
	for _, volumeType := range volumeTypes(e.loaded()) {
		if e.stack.Target.Volumes == nil {
			e.stack.Target.Volumes = map[string]target.VolumeType{}
		}
		if _, set := e.stack.Target.Volumes[volumeType]; !set {
			e.stack.Target.Volumes[volumeType] = target.VolumeType{}
		}
	}
}

func (e *editor) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		e.width = msg.Width
		e.input.SetWidth(max(20, msg.Width-6))
		return e, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		e.spin, cmd = e.spin.Update(msg)
		return e, cmd
	case versionsMsg:
		delete(e.loading, "versions")
		e.versions = msg.versions
		if msg.err != nil {
			e.failure = "reading the versions: " + msg.err.Error()
		}
		if e.screen().kind == screenVersion {
			e.cursor = max(0, slices.Index(e.versions, e.version))
		}
		return e, nil
	case layoutsMsg:
		if msg.directory != e.directory {
			return e, nil
		}
		delete(e.loading, "layouts")
		e.found = msg.found
		if msg.err != nil {
			e.failure = msg.err.Error()
		}
		return e, nil
	case bundlesMsg:
		delete(e.loading, "bundles")
		if msg.err != nil {
			e.failure = msg.err.Error()
			return e, nil
		}
		e.read(msg.bundles)
		if e.screen().kind == screenReview {
			e.enter()
		}
		return e, nil
	case tea.KeyPressMsg:
		return e.key(msg)
	case tea.PasteMsg:
		return e.paste(msg)
	}
	return e, nil
}

// paste hands what was pasted to the screen's input: a credential's value,
// even with its source chosen.
func (e *editor) paste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	switch screen := e.screen(); screen.kind {
	case screenSource, screenDocker, screenOrchestrator, screenImages, screenLayouts, screenReview:
		return e, nil
	case screenVersion:
		if len(e.versions) > 0 {
			return e, nil
		}
	case screenCredential:
		e.field = 1
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	e.problem = ""
	return e, cmd
}

func (e *editor) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if e.quitting {
		if key == "y" {
			return e, tea.Quit
		}
		e.quitting = false
		return e, nil
	}
	switch key {
	case "ctrl+c":
		return e, tea.Quit
	case "esc":
		e.quitting = true
		return e, nil
	case "shift+tab":
		e.keep()
		e.move(-1)
		return e, nil
	}

	screen := e.screen()
	switch screen.kind {
	case screenSource, screenDocker, screenOrchestrator, screenImages:
		return e.choose(key, len(e.options(screen)))
	case screenVersion:
		if len(e.versions) > 0 {
			return e.choose(key, len(e.versions))
		}
	case screenLayouts:
		names := slices.Sorted(maps.Keys(e.found))
		if key == "space" && e.cursor < len(names) {
			e.chosen[names[e.cursor]] = !e.chosen[names[e.cursor]]
			return e, nil
		}
		return e.choose(key, len(names))
	case screenCredential:
		switch {
		case key == "up" || key == "down":
			e.field = 1 - e.field
			return e, nil
		case e.field == 0 && (key == "left" || key == "right"):
			kinds := sourceKinds()
			index := slices.Index(kinds, e.kinds[screen.name])
			if key == "left" {
				index = (index + len(kinds) - 1) % len(kinds)
			} else {
				index = (index + 1) % len(kinds)
			}
			e.kinds[screen.name] = kinds[index]
			e.echo(screen.name)
			return e, nil
		case e.field == 0 && key == "enter":
			e.field = 1
			return e, nil
		case e.field == 0:
			return e, nil
		}
	case screenReview:
		lines := e.reviewLines()
		step := map[string]int{"up": -1, "k": -1, "down": 1, "j": 1}[key]
		for next := e.cursor + step; step != 0 && next >= 0 && next < len(lines); next += step {
			if lines[next].opens || lines[next].save {
				e.cursor = next
				break
			}
		}
		if key == "enter" {
			return e.submit()
		}
		return e, nil
	}
	if key == "enter" {
		return e.submit()
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	e.problem = ""
	return e, cmd
}

// choose moves among count options, and submits the screen on enter.
func (e *editor) choose(key string, count int) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		e.cursor = max(0, e.cursor-1)
	case "down", "j":
		e.cursor = min(count-1, e.cursor+1)
	case "enter":
		return e.submit()
	}
	return e, nil
}

// keep records what the screen's input holds, unchecked, before going back.
func (e *editor) keep() {
	value := strings.TrimSpace(e.input.Value())
	switch screen := e.screen(); screen.kind {
	case screenNamespace:
		e.namespace = value
	case screenDirectory:
		e.directory = value
	case screenNames:
		e.names = value
	case screenParameter:
		e.setParameter(screen.name, value)
	}
}

// submit checks the screen's answer, records it in the stack, and goes on.
// On the review, it opens the step of the line, or saves.
func (e *editor) submit() (tea.Model, tea.Cmd) {
	if e.screen().kind == screenReview {
		return e.submitReview()
	}
	cmd, err := e.accept(strings.TrimSpace(e.input.Value()))
	if err != nil {
		e.problem = err.Error()
		return e, nil
	}
	e.advance()
	return e, cmd
}

// submitReview opens the step of the review's line, or saves the stack
// once no step is incomplete.
func (e *editor) submitReview() (tea.Model, tea.Cmd) {
	line := e.reviewLines()[e.cursor]
	switch {
	case line.save && len(e.problems(stepReview)) > 0:
		e.problem = "complete the steps marked ! first"
	case line.save:
		e.saved = true
		return e, tea.Quit
	case line.opens:
		e.at, e.reviewed = line.screen, true
		e.enter()
	}
	return e, nil
}

// accept checks the answer of the screen and records it in the stack. It
// returns what the answer starts: listing versions, scanning the layouts,
// loading the bundles.
func (e *editor) accept(value string) (tea.Cmd, error) {
	switch screen := e.screen(); screen.kind {
	case screenName:
		if err := e.nameProblem(value); err != nil {
			return nil, err
		}
		e.stack.Name = value
		for i := range e.stack.Installations {
			e.stack.Installations[i].Stack = value
		}
	case screenSource:
		e.from = e.sources()[e.cursor]
		if e.from == bundle.Layout && e.directory == "" {
			e.directory = workingDirectory()
		}
	case screenNamespace:
		if err := registryNamespace(value); err != nil {
			return nil, err
		}
		e.namespace = strings.TrimSuffix(value, "/")
		e.stack.Remote = "docker://" + e.namespace + "/{{ .Component }}:{{ .Version }}"
		return e.listVersions(), nil
	case screenDirectory:
		if value == "" {
			return nil, errors.New("required")
		}
		directory, err := filepath.Abs(value)
		if err != nil {
			return nil, err
		}
		e.directory = directory
		e.stack.Remote = "oci:" + filepath.Join(e.directory, "{{ .Component }}.oci")
		return e.scanLayouts(), nil
	case screenNames:
		if err := e.setNames(value); err != nil {
			return nil, err
		}
		return e.listVersions(), nil
	case screenLayouts:
		return nil, e.setLayouts()
	case screenVersion:
		return e.acceptVersion(value)
	case screenDocker:
		e.docker = dockerKinds[e.cursor]
		if e.docker == dockerEnvironment {
			e.stack.Target.Host, e.stack.Target.Context = "", ""
		}
	case screenHost:
		if value == "" {
			return nil, errors.New("required")
		}
		host, err := opts.ParseHost(false, value)
		if err != nil {
			return nil, errors.New("unix://, tcp:// or ssh://user@host")
		}
		e.stack.Target.Host, e.stack.Target.Context = host, ""
	case screenContext:
		if value == "" {
			return nil, errors.New("required")
		}
		e.stack.Target.Host, e.stack.Target.Context = "", value
	case screenOrchestrator:
		e.stack.Target.Orchestrator = []string{target.Swarm, target.Compose}[e.cursor]
	case screenImages:
		e.stack.Target.Images = []string{target.ImagesRegistry, target.ImagesLoad}[e.cursor]
	case screenParameter:
		if _, _, required := e.parameter(screen.name); required && value == "" {
			return nil, errors.New("required")
		}
		e.setParameter(screen.name, value)
	case screenCredential:
		return nil, e.setCredential(screen.name, value)
	}
	return nil, nil
}

// acceptVersion takes the version chosen, or typed when the remote lists
// none, for every bundle not given by its file or repository, and loads them.
func (e *editor) acceptVersion(value string) (tea.Cmd, error) {
	version := value
	if len(e.versions) > 0 {
		version = e.versions[e.cursor]
	}
	if version == "" {
		return nil, errors.New("required")
	}
	e.version = version
	for i := range e.stack.Installations {
		if reference := &e.stack.Installations[i].Bundle; reference.File == "" && reference.Repository == "" {
			reference.Version = version
		}
	}
	return e.loadBundles(), nil
}

// advance goes to the next screen: back to the review once a step opened
// from it is done.
func (e *editor) advance() {
	if screens := e.screens(); e.reviewed && e.index()+1 < len(screens) && screens[e.index()+1].step != e.at.step {
		e.at, e.reviewed = screen{kind: screenReview, step: stepReview}, false
		e.enter()
		return
	}
	e.move(1)
}

// setNames keeps the installations of the bundles named, as they were, and
// adds those of the others at the version chosen.
func (e *editor) setNames(value string) error {
	names := strings.Fields(value)
	if len(names) == 0 {
		return errors.New("required")
	}
	if e.stack.Remote == "" {
		return errors.New("say first where the bundles are")
	}
	for _, name := range names {
		if _, err := cnab.ParseOCIReference(strings.TrimSuffix(e.namespace, "/") + "/" + name); e.from == bundle.Registry && err != nil {
			return fmt.Errorf("%s: lowercase letters, digits, dots, dashes and underscores", name)
		}
	}
	e.names = strings.Join(names, " ")
	e.setInstallations(names, func(string) string { return e.version })
	return nil
}

func (e *editor) setLayouts() error {
	if e.loading["layouts"] != "" {
		return errors.New("the bundles are being read")
	}
	var names []string
	for _, name := range slices.Sorted(maps.Keys(e.found)) {
		if e.chosen[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return errors.New("choose at least one, with space")
	}
	e.setInstallations(names, func(name string) string { return e.found[name].Version })
	bundles := map[string]cnab.ExtendedBundle{}
	for _, name := range names {
		bundles[name] = e.found[name]
	}
	e.failure = ""
	e.read(bundles)
	return nil
}

func (e *editor) setInstallations(names []string, version func(string) string) {
	var installations []storage.InstallationSpec
	for _, name := range names {
		index := slices.IndexFunc(e.stack.Installations, func(installation storage.InstallationSpec) bool { return installation.Name == name })
		installation := storage.InstallationSpec{Name: name, Stack: e.stack.Name}
		if index >= 0 {
			installation = e.stack.Installations[index]
		}
		if installation.Bundle.File == "" && installation.Bundle.Repository == "" {
			installation.Bundle.Version = version(name)
		}
		installations = append(installations, installation)
	}
	e.stack.Installations = installations
}

func (e *editor) setParameter(name, value string) {
	if value == "" {
		delete(e.stack.Parameters, name)
		return
	}
	if e.stack.Parameters == nil {
		e.stack.Parameters = map[string]string{}
	}
	e.stack.Parameters[name] = value
}

func (e *editor) setCredential(name, value string) error {
	e.stack.Credentials = slices.DeleteFunc(e.stack.Credentials, func(given valuesource.Strategy) bool { return given.Name == name })
	if value == "" {
		if _, required := e.credential(name); required {
			return errors.New("required")
		}
		return nil
	}
	strategy, err := credentials.Parse(name, e.kinds[name]+":"+value)
	if err != nil {
		return err
	}
	e.stack.Credentials = append(e.stack.Credentials, credentials.Absolute(strategy, workingDirectory()))
	return nil
}

// echo hides what is typed of a credential given as its value.
func (e *editor) echo(name string) {
	e.input.EchoMode = textinput.EchoNormal
	if e.kinds[name] == host.SourceValue {
		e.input.EchoMode = textinput.EchoPassword
	}
}

func sourceKinds() []string {
	return []string{host.SourcePath, host.SourceEnv, host.SourceCommand, host.SourceValue}
}

// sourceTitles say what the value of each kind of source of a credential is.
var sourceTitles = map[string]string{
	host.SourcePath:    "File",
	host.SourceEnv:     "Variable",
	host.SourceCommand: "Command",
	host.SourceValue:   "Value",
}

type option struct{ label, help string }

func (e *editor) options(screen screen) []option {
	switch screen.kind {
	case screenSource:
		options := []option{
			{"A registry", "pushed there; docker login for a private one"},
			{"A directory of OCI layouts", "built here: nemo bundle build -o oci:DIRECTORY/NAME.oci"},
		}
		if e.from == keepRemote {
			options = append(options, option{"The manifest's remote", e.stack.Remote})
		}
		return options
	case screenDocker:
		return []option{
			{"The environment's", "DOCKER_HOST, the current context, or the socket"},
			{"An address", "unix://, tcp:// or ssh://user@host"},
			{"A docker context", "one of docker context ls"},
		}
	case screenOrchestrator:
		return []option{{target.Swarm, "services on a swarm, created if there is none"}, {target.Compose, "containers, as docker compose runs them"}}
	case screenImages:
		return []option{{target.ImagesRegistry, "each machine pulls them from their registry"}, {target.ImagesLoad, "nemo loads them into its Docker: one machine, no registry"}}
	}
	return nil
}

// reviewLine is a line of the review: one that opens a screen, or saves.
type reviewLine struct {
	label, value string
	indent       bool
	problem      bool
	opens        bool
	screen       screen
	save         bool
}

func (e *editor) reviewLines() []reviewLine {
	screens := e.screens()
	at := func(kind screenKind, name string) (screen, bool) {
		index := slices.IndexFunc(screens, func(screen screen) bool { return screen.kind == kind && screen.name == name })
		if index < 0 {
			return screen{}, false
		}
		return screens[index], true
	}
	opening := func(line reviewLine, kind screenKind, name string) reviewLine {
		line.screen, line.opens = at(kind, name)
		return line
	}
	var lines []reviewLine
	if e.askName {
		lines = append(lines, opening(reviewLine{label: "Name", value: e.stack.Name, problem: len(e.problems(stepName)) > 0}, screenName, ""))
	}
	bundles := e.problems(stepBundles)
	lines = append(lines, opening(reviewLine{label: "Bundles", value: cmp.Or(e.stack.Remote, "not given"), problem: len(bundles) > 0}, screenSource, ""))
	for _, installation := range e.stack.Installations {
		reference := cmp.Or(installation.Bundle.File, installation.Bundle.Repository, "")
		lines = append(lines, reviewLine{label: installation.Name, value: strings.TrimSpace(reference + " " + installation.Bundle.Version), indent: true})
	}
	if loading := cmp.Or(e.loading["bundles"], e.loading["layouts"]); loading != "" {
		lines = append(lines, reviewLine{label: e.spin.View() + " " + loading, indent: true})
	}
	for _, problem := range bundles {
		lines = append(lines, reviewLine{label: problem, indent: true, problem: true})
	}
	docker := "the environment's Docker"
	switch {
	case e.stack.Target.Host != "":
		docker = e.stack.Target.Host
	case e.stack.Target.Context != "":
		docker = "docker context " + e.stack.Target.Context
	}
	lines = append(lines, opening(reviewLine{
		label: "Target", value: cmp.Or(e.stack.Target.Orchestrator, "no orchestrator") + " · images " + cmp.Or(e.stack.Target.Images, target.ImagesRegistry) + " · " + docker,
		problem: len(e.problems(stepTarget)) > 0,
	}, screenDocker, ""))
	for _, volumeType := range slices.Sorted(maps.Keys(e.stack.Target.Volumes)) {
		lines = append(lines, reviewLine{label: "volume " + volumeType, value: cmp.Or(e.stack.Target.Volumes[volumeType].Driver, "local"), indent: true})
	}
	if names := e.parameterNames(); len(names) > 0 {
		lines = append(lines, reviewLine{label: "Parameters"})
		for _, name := range names {
			_, fallback, required := e.parameter(name)
			value, missing := cmp.Or(e.stack.Parameters[name], fallback), false
			if value == "" {
				value, missing = "not given", required
				if missing {
					value = "required, missing"
				}
			}
			lines = append(lines, opening(reviewLine{label: name, value: value, indent: true, problem: missing}, screenParameter, name))
		}
	}
	if names := e.credentialNames(); len(names) > 0 {
		lines = append(lines, reviewLine{label: "Credentials"})
		for _, name := range names {
			_, required := e.credential(name)
			value, missing := "not given", required
			if strategy, given := e.strategy(name); given {
				value, missing = strategy.Source.Key+" "+strategy.Source.Value, false
				if strategy.Source.Key == host.SourceValue {
					value = host.SourceValue + " " + strings.Repeat("•", 8)
				}
			} else if required {
				value = "required, missing"
			}
			lines = append(lines, opening(reviewLine{label: name, value: value, indent: true, problem: missing}, screenCredential, name))
		}
	}
	verb := map[editMode]string{modeCreate: "Create", modeEdit: "Save", modeImport: "Import"}[e.mode]
	return append(lines, reviewLine{label: verb + " " + cmp.Or(e.stack.Name, "the stack"), save: true})
}

var (
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	title  = lipgloss.NewStyle().Bold(true)
)

func (e *editor) View() tea.View {
	var out strings.Builder
	fmt.Fprintf(&out, " %s%s\n", bold.Render("nemo"), faint.Render(" · stack "+cmp.Or(e.stack.Name, "new")))
	out.WriteString(" " + e.stepBar() + "\n\n")

	screen := e.screen()
	heading, description := e.heading(screen)
	out.WriteString(" " + title.Render(heading) + "\n")
	if description != "" {
		out.WriteString(lipgloss.NewStyle().Width(max(20, e.width-2)).PaddingLeft(1).Render(faint.Render(description)) + "\n")
	}
	out.WriteString("\n" + e.body(screen) + "\n")
	if e.problem != "" {
		out.WriteString("\n " + red.Render("! "+e.problem) + "\n")
	}
	out.WriteString("\n " + faint.Render(e.help(screen)) + "\n")
	view := tea.NewView(out.String())
	view.AltScreen = true
	return view
}

func (e *editor) stepBar() string {
	current := e.screen().step
	var parts []string
	for _, s := range e.steps() {
		label := stepTitles[s]
		if s == stepParameters || s == stepCredentials {
			if count := e.countIn(s); count.total > 0 && s == current {
				label += fmt.Sprintf(" %d/%d", count.at, count.total)
			}
		}
		switch {
		case s == current:
			parts = append(parts, accent.Render("● "+label))
		case s == stepReview:
			parts = append(parts, faint.Render("○ "+label))
		case len(e.problems(s)) == 0 && e.visited[s]:
			parts = append(parts, green.Render("✓")+" "+label)
		case e.visited[s]:
			parts = append(parts, red.Render("!")+" "+label)
		default:
			parts = append(parts, faint.Render("○ "+label))
		}
	}
	return strings.Join(parts, faint.Render("  ›  "))
}

type count struct{ at, total int }

func (e *editor) countIn(s step) count {
	var result count
	for i, screen := range e.screens() {
		if screen.step == s {
			result.total++
			if i <= e.index() {
				result.at = result.total
			}
		}
	}
	return result
}

func (e *editor) heading(screen screen) (string, string) {
	switch screen.kind {
	case screenName:
		return "Name of the stack", "lowercase letters, digits, dashes and underscores; short, as Swarm names services STACK_SERVICE in 63 characters"
	case screenSource:
		return "Where are the bundles?", ""
	case screenNamespace:
		return "Registry and namespace of the bundles", "REGISTRY/NAMESPACE, where they were pushed: with registry.example.com/shop, the stack's remote is docker://registry.example.com/shop/{{ .Component }}:{{ .Version }}"
	case screenDirectory:
		return "Directory of the OCI layouts", "where nemo bundle build -o oci:DIRECTORY/NAME.oci wrote them"
	case screenNames:
		return "Bundles", "their names, separated by spaces"
	case screenLayouts:
		return "Bundles to install", "those the directory holds"
	case screenVersion:
		return "Version of the bundles", strings.Join(installationNames(e.stack.Installations), " ")
	case screenDocker:
		return "Which Docker is the target?", "everything nemo deploys goes through it"
	case screenHost:
		return "Address of the target's Docker", "unix://, tcp:// or ssh://user@host"
	case screenContext:
		return "Docker context of the target", "a context of docker context ls"
	case screenOrchestrator:
		return "Orchestrator", ""
	case screenImages:
		return "How do the images reach the target?", ""
	case screenParameter:
		description, fallback, required := e.parameter(screen.name)
		var notes []string
		if required {
			notes = append(notes, "required")
		}
		if fallback != "" {
			notes = append(notes, "default "+fallback)
		}
		return "Parameter " + screen.name, strings.TrimSpace(description + "  " + strings.Join(notes, " · "))
	case screenCredential:
		description, required := e.credential(screen.name)
		if required {
			description = strings.TrimSpace(description + "  required")
		}
		return "Credential " + screen.name, description
	case screenReview:
		return "Review", ""
	}
	return "", ""
}

func installationNames(installations []storage.InstallationSpec) []string {
	var names []string
	for _, installation := range installations {
		names = append(names, installation.Name)
	}
	return names
}

func (e *editor) body(screen screen) string {
	if loading := e.loading[map[screenKind]string{screenLayouts: "layouts", screenVersion: "versions", screenParameter: "bundles", screenCredential: "bundles"}[screen.kind]]; loading != "" {
		return " " + e.spin.View() + " " + loading
	}
	switch screen.kind {
	case screenSource, screenDocker, screenOrchestrator, screenImages:
		var lines []string
		for i, option := range e.options(screen) {
			lines = append(lines, e.optionLine(i, fmt.Sprintf("%-28s", option.label)+faint.Render(option.help)))
		}
		return strings.Join(lines, "\n")
	case screenLayouts:
		if e.failure != "" && len(e.found) == 0 {
			return " " + red.Render(e.failure)
		}
		var lines []string
		for i, name := range slices.Sorted(maps.Keys(e.found)) {
			check := "[ ]"
			if e.chosen[name] {
				check = "[" + green.Render("x") + "]"
			}
			lines = append(lines, e.optionLine(i, check+" "+name+" "+faint.Render(e.found[name].Version)))
		}
		return strings.Join(lines, "\n")
	case screenVersion:
		if len(e.versions) == 0 {
			return " " + e.input.View()
		}
		var lines []string
		for i, version := range e.versions[:min(len(e.versions), 10)] {
			lines = append(lines, e.optionLine(i, version))
		}
		return strings.Join(lines, "\n")
	case screenCredential:
		var kinds []string
		for _, kind := range sourceKinds() {
			if kind == e.kinds[screen.name] {
				kind = accent.Render("[ " + kind + " ]")
			} else {
				kind = faint.Render("  " + kind + "  ")
			}
			kinds = append(kinds, kind)
		}
		marker := func(field int) string {
			if e.field == field {
				return accent.Render("›")
			}
			return " "
		}
		input := e.input.View()
		if e.field == 0 {
			input = faint.Render("  " + cmp.Or(e.input.Value(), "…"))
		}
		return fmt.Sprintf(" %s Source    %s\n %s %-9s %s", marker(0), strings.Join(kinds, ""), marker(1), sourceTitles[e.kinds[screen.name]], input)
	case screenReview:
		return e.review()
	}
	return " " + e.input.View()
}

func (e *editor) optionLine(index int, text string) string {
	if index == e.cursor {
		return " " + accent.Render("> ") + text
	}
	return "   " + text
}

func (e *editor) review() string {
	var lines []string
	for i, line := range e.reviewLines() {
		cursor, mark := "  ", "  "
		if i == e.cursor {
			cursor = accent.Render("> ")
		}
		if line.problem {
			mark = red.Render("! ")
		}
		label := line.label
		width := 14
		if line.indent {
			label, width = "  "+label, 34
		}
		value := line.value
		switch {
		case line.save:
			label = title.Render(label)
			lines = append(lines, "")
		case !line.indent:
			label = title.Render(fmt.Sprintf("%-*s", width, label))
		default:
			label = fmt.Sprintf("%-*s", width, label)
		}
		value = ansi.Truncate(value, max(10, e.width-6-lipgloss.Width(label)), "…")
		if line.problem {
			value = red.Render(value)
		}
		lines = append(lines, " "+cursor+mark+label+" "+value)
	}
	return strings.Join(lines, "\n")
}

func (e *editor) help(screen screen) string {
	if e.quitting {
		return "Quit without saving? y to quit, any other key to stay"
	}
	back := " · shift+tab back · esc quit"
	switch screen.kind {
	case screenSource, screenDocker, screenOrchestrator, screenImages, screenVersion:
		if screen.kind == screenVersion && len(e.versions) == 0 {
			break
		}
		return "↑/↓ choose · enter next" + back
	case screenLayouts:
		return "↑/↓ move · space choose · enter next" + back
	case screenCredential:
		return "←/→ source · ↑/↓ source or value · enter next" + back
	case screenReview:
		return "↑/↓ move · enter opens a line, or saves on the last · esc quit"
	}
	return "enter next" + back
}

// editStack runs the editor on the stack, and returns it as the editor left
// it, and whether it was saved there.
func editStack(ctx context.Context, store *stack.Store, edited stack.Stack, mode editMode) (stack.Stack, bool, error) {
	taken := func(name string) bool {
		_, err := store.Path(name)
		return err == nil
	}
	final, err := tea.NewProgram(newEditor(ctx, edited, mode, taken), tea.WithContext(ctx)).Run()
	if err != nil {
		return edited, false, err
	}
	result := final.(*editor)
	return result.stack, result.saved, nil
}
