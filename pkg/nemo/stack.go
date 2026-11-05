package nemo

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cnabio/cnab-go/valuesource"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/progress"
	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
)

// UpRequest brings a stack to its file.
type UpRequest struct {
	Stack stack.Stack
	// Only names the installations to act on, with their dependencies not
	// installed yet; empty, all of the stack's.
	// Up then leaves alone the others, and what the stack no longer has.
	Only []string
	// Purge also removes the volumes of the bundles the stack no longer has.
	Purge bool
	// Progress receives the events of the actions: first each installation
	// pending, then what each action does.
	Progress progress.Sink
}

// DownRequest removes a stack, by its name.
type DownRequest struct {
	Name string
	// Manifest is the stack's, when nemo does not keep it: it says where
	// the records of its installations are.
	Manifest *stack.Stack
	// Purge also removes the volumes.
	Purge    bool
	Progress progress.Sink
}

// StackResult is the runs of an up or a down, in the order they ran.
type StackResult struct {
	Runs []Run `json:"runs"`
}

// Up brings the stack to its manifest. It plans before it touches anything:
// it reads every installation's credentials, renders its compose project to
// know what it declares on the target, and refuses what two installations
// can't share. It then checks every bundle that has a validate action,
// installs the bundles not installed and upgrades the others, and
// uninstalls the installations the stack no longer has. Each starts as soon
// as those it depends on are done and the objects it shares with others are
// there, beside the others that can; on the way down, dependants first, and
// the creator of a shared object after its other users. After an action
// fails it starts no other, and waits for those under way.
func (nemo *Nemo) Up(ctx context.Context, request UpRequest) (_ StackResult, err error) {
	done, err := nemo.open(ctx, &request.Stack, request.Stack.Name, true)
	if err != nil {
		return StackResult{}, err
	}
	defer release(done, &err)
	plan, err := nemo.planUp(ctx, request)
	if err != nil {
		return StackResult{}, err
	}
	events := progressOf(request.Progress)
	warnings, err := nemo.prepare(ctx, plan)
	if err != nil {
		return StackResult{}, err
	}
	plan.sharing.announce(events, plan.stack.Name)
	plan.announce(events)
	for _, warning := range warnings {
		events.Emit(warning)
	}

	var checks []step
	for _, bundle := range plan.validated() {
		checks = append(checks, step{name: bundle.Name})
	}
	err = schedule(checks, plan.limit, func(current step, _ func(object)) error {
		_, err := nemo.actOn(ctx, plan, plan.entries[current.name].loaded.Definition, events, true)
		return err
	})
	if err != nil {
		return StackResult{}, err
	}

	var result StackResult
	var mutex sync.Mutex
	err = schedule(plan.steps(), plan.limit, func(current step, announce func(object)) error {
		bundle := plan.entries[current.name].loaded.Definition
		request := plan.actionRequest(bundle, plan.sharing.eventsOf(events, plan.stack.Name, current.name, announce), false)
		run, err := nemo.perform(ctx, plan.action(bundle), request, plan.prepared[current.name])
		if run.Revision != "" {
			mutex.Lock()
			result.Runs = append(result.Runs, run)
			mutex.Unlock()
		}
		if err != nil {
			err = fmt.Errorf("%s/%s: %w", plan.stack.Name, bundle.Name, err)
		}
		return err
	})
	if err != nil {
		return result, err
	}
	if len(request.Only) > 0 {
		return result, nil
	}
	runs, err := nemo.uninstall(ctx, plan.stack.Name, plan.dropped(), request.Purge, events, plan.inventories)
	result.Runs = append(result.Runs, runs...)
	return result, err
}

// prepare plans the action of each installation, reads its credentials and
// renders what it declares, then what the installations share: all before
// anything is touched. An installation nemo can't render makes the stack go
// one installation at a time, as nemo can't tell what it shares.
func (nemo *Nemo) prepare(ctx context.Context, plan *upPlan) ([]progress.Event, error) {
	var warnings []progress.Event
	plan.prepared, plan.inventories, plan.limit = map[string]actionPlan{}, map[string]*inventory{}, parallelActions
	for _, bundle := range plan.ordered {
		action := plan.action(bundle)
		entry := plan.entries[bundle.Name]
		prepared, err := nemo.plan(ctx, action, plan.actionRequest(bundle, nil, false), &entry.loaded)
		if err == nil {
			err = prepared.readCredentials(action)
		}
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", plan.stack.Name, bundle.Name, err)
		}
		plan.prepared[bundle.Name] = prepared
		declared, err := inventoryOf(ctx, runtime.Request{Bundle: prepared.bundle, Stack: plan.stack.Name, Action: action, Target: nemo.reach(plan.stack.Name, prepared.target), Parameters: prepared.parameters}, prepared.values)
		if err != nil {
			plan.limit = 1
			warnings = append(warnings, unreadable(plan.stack.Name, bundle.Name, err))
			continue
		}
		plan.inventories[bundle.Name] = declared
	}
	shared, err := sharing(orderedNames(plan.ordered), plan.inventories)
	if err != nil {
		return nil, fmt.Errorf("stack %s: %w", plan.stack.Name, err)
	}
	if plan.limit == 1 {
		shared = nil
	}
	plan.sharing = newSharingPlan(orderedNames(plan.ordered), shared)
	return warnings, nil
}

// unreadable warns that nemo can't render an installation's project: it
// can't tell what the installation shares, so the stack goes one
// installation at a time.
func unreadable(stackName, name string, err error) progress.Event {
	return progress.Event{Installation: stackName + "/" + name, Group: "project", State: progress.Warning, Message: fmt.Sprintf("nemo can't read it (%v): the stack goes one installation at a time", err)}
}

// steps are the actions of the plan, each after the installations it
// depends on and the objects it shares that another creates.
func (plan *upPlan) steps() []step {
	steps := make([]step, len(plan.ordered))
	for i, bundle := range plan.ordered {
		steps[i] = step{name: bundle.Name, after: stack.DependencyNames(bundle), needs: plan.sharing.needs(bundle.Name), creates: plan.sharing.creates(bundle.Name)}
	}
	return steps
}

// upPlan is what Up does: the bundles to act on, in the order of their
// dependencies, and what the stack had installed before.
type upPlan struct {
	stack   stack.Stack
	entries map[string]stackEntry
	ordered []cnab.ExtendedBundle
	present map[string]storage.Installation
	// prepared are the actions, planned with their credentials read, and
	// inventories what each installation declares, by name.
	prepared    map[string]actionPlan
	inventories map[string]*inventory
	sharing     sharingPlan
	// limit is how many actions run at once.
	limit int
}

// planUp loads the bundles of the stack and the dependencies it does not
// list, checks their dependencies and orders them: all of them, or those Only
// names with their dependencies not installed yet.
func (nemo *Nemo) planUp(ctx context.Context, request UpRequest) (*upPlan, error) {
	if err := request.Stack.Validate(); err != nil {
		return nil, err
	}
	loaded, err := nemo.loadStack(ctx, request.Stack)
	if err != nil {
		return nil, err
	}
	if err := stack.CheckDependencies(loaded.bundles); err != nil {
		return nil, fmt.Errorf("stack %s: %w", request.Stack.Name, err)
	}
	ordered, err := stack.Order(loaded.bundles)
	if err != nil {
		return nil, fmt.Errorf("stack %s: %w", request.Stack.Name, err)
	}
	present, err := nemo.present(ctx, request.Stack.Name)
	if err != nil {
		return nil, err
	}
	plan := &upPlan{stack: request.Stack, entries: loaded.entries, ordered: ordered, present: present}
	if len(request.Only) > 0 {
		return plan, plan.narrow(request.Only)
	}
	return plan, nil
}

// stackBundles are the bundles of a stack, in the order it lists them, then
// the dependencies it does not list.
type stackBundles struct {
	entries map[string]stackEntry
	bundles []cnab.ExtendedBundle
}

func (loaded *stackBundles) add(entry stackEntry) {
	loaded.entries[entry.loaded.Definition.Name] = entry
	loaded.bundles = append(loaded.bundles, entry.loaded.Definition)
}

// loadStack loads the bundle of each installation of the stack, then the
// dependencies the stack does not list, until none is missing.
func (nemo *Nemo) loadStack(ctx context.Context, stackOf stack.Stack) (stackBundles, error) {
	loaded := stackBundles{entries: map[string]stackEntry{}}
	if err := nemo.loadInstallations(ctx, stackOf, &loaded); err != nil {
		return loaded, err
	}
	return loaded, nemo.loadDependencies(ctx, stackOf, &loaded)
}

// loadInstallations loads the bundle of each installation the stack lists.
func (nemo *Nemo) loadInstallations(ctx context.Context, stackOf stack.Stack, loaded *stackBundles) error {
	for _, spec := range stackOf.Installations {
		source, err := stackOf.Source(spec)
		if err != nil {
			return err
		}
		bundle, err := nemo.load(ctx, source)
		if err != nil {
			return err
		}
		if spec.Name != bundle.Definition.Name {
			return fmt.Errorf("stack %s: the bundle at %s is %s, not %s", stackOf.Name, source, bundle.Definition.Name, spec.Name)
		}
		if _, twice := loaded.entries[spec.Name]; twice {
			return fmt.Errorf("stack %s: installation %s is given twice", stackOf.Name, spec.Name)
		}
		spec.Stack = stackOf.Name
		loaded.add(stackEntry{spec: spec, source: source, loaded: bundle})
	}
	return nil
}

// loadDependencies loads the dependencies of the bundles the stack does not
// list, and theirs, until none is missing.
func (nemo *Nemo) loadDependencies(ctx context.Context, stackOf stack.Stack, loaded *stackBundles) error {
	resolver := cnab.Resolver{Tags: nemo.tags}
	for missing := stack.Missing(loaded.bundles); len(missing) > 0; missing = stack.Missing(loaded.bundles) {
		for _, requirement := range missing {
			spec, source, bundle, err := nemo.dependency(ctx, stackOf, resolver, requirement)
			if err != nil {
				return fmt.Errorf("stack %s: dependency %s: %w", stackOf.Name, requirement.Dependency.Bundle, err)
			}
			loaded.add(stackEntry{spec: spec, source: source, loaded: bundle})
		}
	}
	return nil
}

// narrow keeps the installations only names, and their dependencies not
// installed yet. Backwards, dependants come before their dependencies: those
// join them on the way.
func (plan *upPlan) narrow(only []string) error {
	for _, name := range only {
		if _, listed := plan.entries[name]; !listed {
			return fmt.Errorf("stack %s has no installation %s", plan.stack.Name, name)
		}
	}
	wanted := slices.Clone(only)
	for _, bundle := range slices.Backward(plan.ordered) {
		if !slices.Contains(wanted, bundle.Name) {
			continue
		}
		for _, name := range stack.DependencyNames(bundle) {
			if !plan.present[name].IsInstalled() {
				wanted = append(wanted, name)
			}
		}
	}
	plan.ordered = slices.DeleteFunc(plan.ordered, func(bundle cnab.ExtendedBundle) bool { return !slices.Contains(wanted, bundle.Name) })
	return nil
}

// action is install for a bundle the stack does not have installed, else upgrade.
func (plan *upPlan) action(bundle cnab.ExtendedBundle) string {
	if plan.present[bundle.Name].IsInstalled() {
		return cnab.ActionUpgrade
	}
	return cnab.ActionInstall
}

// announce tells each installation pending, with its action.
func (plan *upPlan) announce(events progress.Sink) {
	for _, bundle := range plan.ordered {
		events.Emit(progress.Event{Installation: plan.stack.Name + "/" + bundle.Name, Group: progress.Installation, State: progress.Pending, Message: plan.action(bundle)})
	}
}

// validated returns the bundles that have a validate action, in order.
func (plan *upPlan) validated() []cnab.ExtendedBundle {
	return slices.DeleteFunc(slices.Clone(plan.ordered), func(bundle cnab.ExtendedBundle) bool {
		_, declared := bundle.Actions[cnab.ActionValidate]
		return !declared
	})
}

// dropped returns the installations of the stack whose bundle it no longer has.
func (plan *upPlan) dropped() map[string]storage.Installation {
	dropped := maps.Clone(plan.present)
	for name := range plan.entries {
		delete(dropped, name)
	}
	return dropped
}

// actionRequest is the request of the action on the bundle: the stack's
// parameters the bundle declares, under the installation's own, and the
// credentials of both.
func (plan *upPlan) actionRequest(bundle cnab.ExtendedBundle, events progress.Sink, validate bool) ActionRequest {
	entry := plan.entries[bundle.Name]
	parameters := map[string]string{}
	for name, value := range plan.stack.Parameters {
		if _, declared := bundle.Parameters[name]; declared {
			parameters[name] = value
		}
	}
	maps.Copy(parameters, entry.spec.Parameters)
	if entry.spec.Override != "" {
		parameters[cnab.ParameterOverride] = entry.spec.Override
	}
	spec := entry.spec
	spec.Parameters, spec.Override = parameters, ""
	// The manifest's overrides were checked when the stack was read.
	extended, _ := credentials.WithOverride(bundle, entry.spec.Override)
	spec.Credentials = credentials.Merge(extended, plan.stack.Credentials, entry.spec.Credentials)
	return ActionRequest{
		InstallationSpec:  spec,
		BundleSource:      entry.source,
		Target:            plan.stack.Target,
		Installer:         plan.stack.Installer,
		Progress:          events,
		Validate:          validate,
		ReplaceParameters: true,
	}
}

// actOn runs the action of the plan on the bundle: its validate action when validate is set.
func (nemo *Nemo) actOn(ctx context.Context, plan *upPlan, bundle cnab.ExtendedBundle, events progress.Sink, validate bool) (Run, error) {
	entry := plan.entries[bundle.Name]
	run, err := nemo.act(ctx, plan.action(bundle), plan.actionRequest(bundle, events, validate), &entry.loaded)
	if err != nil {
		err = fmt.Errorf("%s/%s: %w", plan.stack.Name, bundle.Name, err)
	}
	return run, err
}

// dependency loads a dependency the stack does not list: from the
// stack's remote first, at the version of the bundle that
// requires it, which is how a build's or a kit's bundles are found; else
// from where the dependency says, at the version its ranges allow, as
// Porter does. A bundle the remote has must satisfy the ranges.
func (nemo *Nemo) dependency(ctx context.Context, stackOf stack.Stack, resolver cnab.Resolver, requirement stack.Requirement) (storage.InstallationSpec, string, cnab.BundleReference, error) {
	dependency := requirement.Dependency
	name, err := dependency.BundleName()
	if err != nil {
		return storage.InstallationSpec{}, "", cnab.BundleReference{}, err
	}
	spec := storage.InstallationSpec{Name: name, Stack: stackOf.Name, Bundle: storage.BundleReference{Version: requirement.Dependant.Version}}
	if stackOf.Remote != "" {
		source, err := stackOf.Source(spec)
		if err != nil {
			return spec, "", cnab.BundleReference{}, err
		}
		if loaded, err := nemo.load(ctx, source); err == nil && loaded.Definition.Name == name {
			if err := dependency.Allows(loaded.Definition.Version); err != nil {
				return spec, "", cnab.BundleReference{}, fmt.Errorf("the stack's remote has it at %s: %w", source, err)
			}
			spec.Bundle.Version = loaded.Definition.Version
			return spec, source, loaded, nil
		}
	}
	reference, err := resolver.ResolveVersion(ctx, dependency.Name, dependency)
	if err != nil {
		return spec, "", cnab.BundleReference{}, err
	}
	source := "docker://" + reference.String()
	loaded, err := nemo.load(ctx, source)
	spec.Bundle = storage.BundleReference{Repository: reference.Repository(), Version: reference.Tag()}
	return spec, source, loaded, err
}

// stackEntry is an installation of the stack, and the bundle it names.
type stackEntry struct {
	spec   storage.InstallationSpec
	source string
	loaded cnab.BundleReference
}

// Down uninstalls every installation of the stack, dependants first. It
// needs nothing but the stack's name: the rest comes from the installations.
// A stack with nothing installed is already down.
func (nemo *Nemo) Down(ctx context.Context, request DownRequest) (_ StackResult, err error) {
	done, err := nemo.open(ctx, request.Manifest, request.Name, true)
	if err != nil {
		return StackResult{}, err
	}
	defer release(done, &err)
	present, err := nemo.present(ctx, request.Name)
	if err != nil {
		return StackResult{}, err
	}
	runs, err := nemo.uninstall(ctx, request.Name, present, request.Purge, request.Progress, nil)
	return StackResult{Runs: runs}, err
}

// present returns the installations of the stack that may have something on
// the target, by bundle name: all but the uninstalled, those whose install
// failed included.
func (nemo *Nemo) present(ctx context.Context, stackName string) (map[string]storage.Installation, error) {
	installations, err := nemo.records(stackName).ListInstallations(ctx, stackName)
	if err != nil {
		return nil, err
	}
	present := map[string]storage.Installation{}
	for _, installation := range installations {
		if !installation.IsUninstalled() {
			present[installation.Name] = installation
		}
	}
	return present, nil
}

// uninstall uninstalls the installations, each once those that depend on
// it are gone, and the creator of an object they share after its other
// users, beside the others that can. The installations kept are those the
// stack still has: one that shares a volume with them keeps its volumes, even
// with purge.
func (nemo *Nemo) uninstall(ctx context.Context, stackName string, installations map[string]storage.Installation, purge bool, events progress.Sink, kept map[string]*inventory) ([]Run, error) {
	events = progressOf(events)
	bundles := make([]cnab.ExtendedBundle, 0, len(installations))
	for _, name := range slices.Sorted(maps.Keys(installations)) {
		bundle, err := nemo.bundleOf(ctx, installations[name])
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, bundle)
	}
	ordered, err := stack.Order(bundles)
	if err != nil {
		return nil, fmt.Errorf("stack %s: %w", stackName, err)
	}

	limit, inventories := parallelActions, map[string]*inventory{}
	var warnings []progress.Event
	for _, bundle := range ordered {
		installation := installations[bundle.Name]
		declared, err := inventoryOf(ctx, runtime.Request{Bundle: bundle, Stack: stackName, Action: cnab.ActionUninstall, Target: nemo.reach(stackName, installation.Target), Parameters: installation.Parameters}, nil)
		if err != nil {
			limit = 1
			warnings = append(warnings, unreadable(stackName, bundle.Name, err))
			continue
		}
		inventories[bundle.Name] = declared
	}
	// What the installations declare orders them: they were installed, and can't be refused any more.
	shared, _ := sharing(orderedNames(ordered), inventories)
	if limit == 1 {
		shared = nil
	}
	sharing := newSharingPlan(orderedNames(ordered), shared)

	for _, bundle := range slices.Backward(ordered) {
		events.Emit(progress.Event{Installation: stackName + "/" + bundle.Name, Group: progress.Installation, State: progress.Pending, Message: cnab.ActionUninstall})
	}
	sharing.announce(events, stackName)
	for _, warning := range warnings {
		events.Emit(warning)
	}

	var steps []step
	for _, bundle := range slices.Backward(ordered) {
		after := sharing.leaveAfter(bundle.Name)
		for _, other := range ordered {
			if slices.Contains(stack.DependencyNames(other), bundle.Name) {
				after = append(after, other.Name)
			}
		}
		steps = append(steps, step{name: bundle.Name, after: after})
	}
	var runs []Run
	var mutex sync.Mutex
	err = schedule(steps, limit, func(current step, _ func(object)) error {
		bundle := ordered[slices.IndexFunc(ordered, func(bundle cnab.ExtendedBundle) bool { return bundle.Name == current.name })]
		var parameters map[string]string
		if _, declared := bundle.Parameters[cnab.ParameterPurge]; declared && purge {
			if still := sharedVolumes(inventories[bundle.Name], kept); len(still) > 0 {
				for _, volume := range slices.Sorted(maps.Keys(still)) {
					events.Emit(progress.Event{Installation: stackName + "/" + bundle.Name, Group: groupVolumes, Item: volume, State: progress.Warning, Message: "kept: " + strings.Join(still[volume], ", ") + " still uses it"})
				}
			} else {
				parameters = map[string]string{cnab.ParameterPurge: strconv.FormatBool(purge)}
			}
		}
		spec := storage.InstallationSpec{Name: bundle.Name, Stack: stackName, Parameters: parameters, Credentials: nemo.manifestCredentials(stackName, bundle)}
		run, err := nemo.Uninstall(ctx, ActionRequest{InstallationSpec: spec, Progress: sharing.eventsOf(events, stackName, bundle.Name, nil)})
		if run.Revision != "" {
			mutex.Lock()
			runs = append(runs, run)
			mutex.Unlock()
		}
		if err != nil {
			return fmt.Errorf("%s/%s: %w", stackName, bundle.Name, err)
		}
		return nil
	})
	return runs, err
}

// manifestCredentials are the credentials the manifest of the operation
// gives the bundle, the stack's under the installation's own: those the
// records keep without their value.
func (nemo *Nemo) manifestCredentials(stackName string, bundle cnab.ExtendedBundle) []valuesource.Strategy {
	nemo.mutex.Lock()
	defer nemo.mutex.Unlock()
	opened, ok := nemo.opened[stackName]
	if !ok || opened.manifest == nil {
		return nil
	}
	var own []valuesource.Strategy
	extended := bundle
	for _, installation := range opened.manifest.Installations {
		if installation.Name == bundle.Name {
			own = installation.Credentials
			extended, _ = credentials.WithOverride(bundle, installation.Override)
		}
	}
	return credentials.Merge(extended, opened.manifest.Credentials, own)
}
