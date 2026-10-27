package nemo

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/progress"
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
	// Purge also removes the volumes.
	Purge    bool
	Progress progress.Sink
}

// StackResult is the runs of an up or a down, in the order they ran.
type StackResult struct {
	Runs []Run `json:"runs"`
}

// Up installs the bundles of the stack in the order of their dependencies,
// or upgrades those installed, then uninstalls the installations of the
// stack whose bundle it no longer has, dependants first. It stops at the
// first action that fails.
func (nemo *Nemo) Up(ctx context.Context, request UpRequest) (StackResult, error) {
	var result StackResult
	if err := request.Stack.Validate(); err != nil {
		return result, err
	}
	bundles := make([]cnab.ExtendedBundle, len(request.Stack.Installations))
	entries := map[string]stackEntry{}
	for i, spec := range request.Stack.Installations {
		source, err := request.Stack.Source(spec)
		if err != nil {
			return result, err
		}
		loaded, err := nemo.load(ctx, source)
		if err != nil {
			return result, err
		}
		if spec.Name != loaded.Definition.Name {
			return result, fmt.Errorf("stack %s: the bundle at %s is %s, not %s", request.Stack.Name, source, loaded.Definition.Name, spec.Name)
		}
		if _, twice := entries[spec.Name]; twice {
			return result, fmt.Errorf("stack %s: installation %s is given twice", request.Stack.Name, spec.Name)
		}
		spec.Stack = request.Stack.Name
		bundles[i], entries[spec.Name] = loaded.Definition, stackEntry{spec: spec, source: source, loaded: loaded}
	}
	resolver := cnab.Resolver{Tags: nemo.tags}
	for missing := stack.Missing(bundles); len(missing) > 0; missing = stack.Missing(bundles) {
		for _, requirement := range missing {
			spec, source, loaded, err := nemo.dependency(ctx, request.Stack, resolver, requirement)
			if err != nil {
				return result, fmt.Errorf("stack %s: dependency %s: %w", request.Stack.Name, requirement.Dependency.Bundle, err)
			}
			bundles = append(bundles, loaded.Definition)
			entries[loaded.Definition.Name] = stackEntry{spec: spec, source: source, loaded: loaded}
		}
	}
	if err := stack.CheckDependencies(bundles); err != nil {
		return result, fmt.Errorf("stack %s: %w", request.Stack.Name, err)
	}
	ordered, err := stack.Order(bundles)
	if err != nil {
		return result, fmt.Errorf("stack %s: %w", request.Stack.Name, err)
	}

	present, err := nemo.present(ctx, request.Stack.Name)
	if err != nil {
		return result, err
	}
	if len(request.Only) > 0 {
		for _, name := range request.Only {
			if _, listed := entries[name]; !listed {
				return result, fmt.Errorf("stack %s has no installation %s", request.Stack.Name, name)
			}
		}
		// Dependants come before their dependencies backwards: the dependencies not installed yet join them.
		wanted := slices.Clone(request.Only)
		for _, bundle := range slices.Backward(ordered) {
			if slices.Contains(wanted, bundle.Name) {
				for _, name := range stack.DependencyNames(bundle) {
					if !present[name].IsInstalled() {
						wanted = append(wanted, name)
					}
				}
			}
		}
		ordered = slices.DeleteFunc(ordered, func(bundle cnab.ExtendedBundle) bool { return !slices.Contains(wanted, bundle.Name) })
	}
	events := progressOf(request.Progress)
	for _, bundle := range ordered {
		action := cnab.ActionInstall
		if present[bundle.Name].IsInstalled() {
			action = cnab.ActionUpgrade
		}
		events.Emit(progress.Event{Installation: request.Stack.Name + "/" + bundle.Name, Group: progress.Installation, State: progress.Pending, Message: action})
	}
	act := func(bundle cnab.ExtendedBundle, validate bool) (Run, error) {
		stacked := entries[bundle.Name]
		parameters := map[string]string{}
		for name, value := range request.Stack.Parameters {
			if _, declared := bundle.Parameters[name]; declared {
				parameters[name] = value
			}
		}
		maps.Copy(parameters, stacked.spec.Parameters)
		action := cnab.ActionInstall
		if present[bundle.Name].IsInstalled() {
			action = cnab.ActionUpgrade
		}
		spec := stacked.spec
		spec.Parameters = parameters
		spec.Credentials = credentials.Merge(bundle, request.Stack.Credentials, stacked.spec.Credentials)
		run, err := nemo.act(ctx, action, ActionRequest{
			InstallationSpec: spec,
			BundleSource:     stacked.source,
			Target:           request.Stack.Target,
			Installer:        request.Stack.Installer,
			Progress:         request.Progress,
			Validate:         validate,
		}, &stacked.loaded)
		if err != nil {
			err = fmt.Errorf("%s/%s: %w", request.Stack.Name, bundle.Name, err)
		}
		return run, err
	}
	for _, bundle := range ordered {
		if _, declared := bundle.Actions[cnab.ActionValidate]; !declared {
			continue
		}
		if _, err := act(bundle, true); err != nil {
			return result, err
		}
	}
	for _, bundle := range ordered {
		run, err := act(bundle, false)
		if run.Revision != "" {
			result.Runs = append(result.Runs, run)
		}
		if err != nil {
			return result, err
		}
		delete(present, bundle.Name)
	}
	if len(request.Only) > 0 {
		return result, nil
	}

	runs, err := nemo.uninstall(ctx, request.Stack.Name, present, request.Purge, request.Progress)
	result.Runs = append(result.Runs, runs...)
	return result, err
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
func (nemo *Nemo) Down(ctx context.Context, request DownRequest) (StackResult, error) {
	present, err := nemo.present(ctx, request.Name)
	if err != nil {
		return StackResult{}, err
	}
	if len(present) == 0 {
		return StackResult{}, fmt.Errorf("stack %s has no installations", request.Name)
	}
	runs, err := nemo.uninstall(ctx, request.Name, present, request.Purge, request.Progress)
	return StackResult{Runs: runs}, err
}

// present returns the installations of the stack that may have something on
// the target, by bundle name: all but the uninstalled, those whose install
// failed included.
func (nemo *Nemo) present(ctx context.Context, stackName string) (map[string]storage.Installation, error) {
	installations, err := nemo.store.ListInstallations(ctx, stackName)
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

// uninstall uninstalls the installations, dependants first.
func (nemo *Nemo) uninstall(ctx context.Context, stackName string, installations map[string]storage.Installation, purge bool, events progress.Sink) ([]Run, error) {
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

	for _, bundle := range slices.Backward(ordered) {
		progressOf(events).Emit(progress.Event{Installation: stackName + "/" + bundle.Name, Group: progress.Installation, State: progress.Pending, Message: cnab.ActionUninstall})
	}
	var runs []Run
	for _, bundle := range slices.Backward(ordered) {
		var parameters map[string]string
		if _, declared := bundle.Parameters[cnab.ParameterPurge]; declared && purge {
			parameters = map[string]string{cnab.ParameterPurge: strconv.FormatBool(purge)}
		}
		spec := storage.InstallationSpec{Name: bundle.Name, Stack: stackName, Parameters: parameters}
		run, err := nemo.Uninstall(ctx, ActionRequest{InstallationSpec: spec, Progress: events})
		if run.Revision != "" {
			runs = append(runs, run)
		}
		if err != nil {
			return runs, fmt.Errorf("%s/%s: %w", stackName, bundle.Name, err)
		}
	}
	return runs, nil
}
