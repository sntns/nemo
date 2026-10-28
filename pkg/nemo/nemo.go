// Package nemo is nemo's API. The nemo CLI and the Terraform provider are
// its clients: they turn their inputs into requests, and render the results.
// Nothing here prints.
package nemo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/cnabio/cnab-go/valuesource"
	"github.com/cnabio/cnab-to-oci/relocation"

	"github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/progress"
	"github.com/sntns/nemo/pkg/runtime"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/storage/file"
	"github.com/sntns/nemo/pkg/target"
)

// Options configure nemo.
type Options struct {
	// Home is where nemo keeps its files. Empty, it is $NEMO_HOME, or ~/.nemo.
	Home string
}

// Nemo is the entry point of the API.
type Nemo struct {
	store   storage.Store
	execute func(context.Context, runtime.Request) (runtime.Run, error)
	// load reads a bundle from its source.
	load func(context.Context, string) (cnab.BundleReference, error)
	// tags lists the versions of a bundle in its registry.
	tags cnab.TagLister
}

// New returns the API.
func New(opts Options) (*Nemo, error) {
	home := opts.Home
	if home == "" {
		var err error
		if home, err = file.DefaultRoot(); err != nil {
			return nil, err
		}
	}
	return &Nemo{store: file.New(home), execute: runtime.Execute, load: LoadBundle, tags: bundle.RegistryTags{}}, nil
}

// ActionRequest is an action on one installation of a bundle. What it
// leaves out comes from the installation, when nemo knows it.
type ActionRequest struct {
	// InstallationSpec names the installation and its stack; its name can
	// be left out when BundleSource is given. Its parameters and
	// credentials override the installation's, and its bundle reference is
	// recorded with it.
	storage.InstallationSpec
	// BundleSource is the bundle to run: a bundle.json, or where it was
	// pushed, docker://REFERENCE or oci:PATH. Install needs it; upgrade and
	// uninstall take the installation's when it is left out.
	BundleSource string
	// Target receives the deployment. Left empty, it is the installation's;
	// it can't change once installed.
	Target target.Target
	// Installer says where the invocation image runs. Left empty, it is the
	// installation's.
	Installer target.Installer
	// Progress receives the events of the action.
	Progress progress.Sink
	// Validate runs the bundle's validate action instead, with what the
	// action would run with: nothing changes, and nothing is recorded.
	Validate bool
}

// Run is the record of an action: what was asked, and what came of it.
// Values of sensitive parameters and outputs are redacted, and credentials
// appear by name only.
type Run struct {
	Installation string            `json:"installation"`
	Revision     string            `json:"revision"`
	Action       string            `json:"action"`
	Bundle       BundleVersion     `json:"bundle"`
	Target       target.Target     `json:"target"`
	Driver       string            `json:"driver"`
	Parameters   map[string]any    `json:"parameters,omitempty"`
	Credentials  []string          `json:"credentials,omitempty"`
	Status       string            `json:"status"`
	Started      time.Time         `json:"started"`
	Ended        time.Time         `json:"ended"`
	Outputs      map[string]string `json:"outputs,omitempty"`
}

// BundleVersion identifies a bundle, by its name and version.
type BundleVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Redacted replaces the value of a sensitive parameter or output.
const Redacted = "*******"

// Install installs the bundle. Installing again an installed bundle applies
// it again, as an upgrade does.
func (nemo *Nemo) Install(ctx context.Context, request ActionRequest) (Run, error) {
	return nemo.act(ctx, cnab.ActionInstall, request, nil)
}

// Upgrade upgrades an installation, to the bundle given or to its own.
func (nemo *Nemo) Upgrade(ctx context.Context, request ActionRequest) (Run, error) {
	return nemo.act(ctx, cnab.ActionUpgrade, request, nil)
}

// Uninstall removes an installation.
func (nemo *Nemo) Uninstall(ctx context.Context, request ActionRequest) (Run, error) {
	return nemo.act(ctx, cnab.ActionUninstall, request, nil)
}

// act runs an action, on the bundle loaded from the request's source unless
// it is given loaded. When it ran and failed, both the Run and the error
// are returned. Every action that ran is recorded, and so is the
// installation's new state.
func (nemo *Nemo) act(ctx context.Context, action string, request ActionRequest, loaded *cnab.BundleReference) (Run, error) {
	plan, err := nemo.plan(ctx, action, request, loaded)
	if err != nil {
		return Run{}, err
	}
	events := progress.About(progressOf(request.Progress), plan.stack+"/"+plan.bundle.Name)
	working, done := progress.Event{Group: progress.Installation, State: progress.Working, Message: action}, progress.Event{Group: progress.Installation, State: progress.Done, Message: action}
	if request.Validate {
		working.Message, done.State = cnab.ActionValidate, progress.Pending
	}
	events.Emit(working)
	run, err := nemo.run(ctx, action, request, plan, events)
	if err != nil {
		// The installation and its action are the event's already: a failed run says only how it ended.
		message := err.Error()
		var failed *runtime.FailedError
		if errors.As(err, &failed) {
			message = failed.Cause
		}
		events.Emit(progress.Event{Group: progress.Installation, State: progress.Error, Message: message})
	} else {
		events.Emit(done)
	}
	return run, err
}

// progressOf is the sink of a request, which may give none.
func progressOf(sink progress.Sink) progress.Sink {
	if sink == nil {
		return progress.Discard
	}
	return sink
}

// run runs the planned action and records it.
func (nemo *Nemo) run(ctx context.Context, action string, request ActionRequest, plan actionPlan, events progress.Sink) (Run, error) {
	if request.Validate {
		if _, declared := plan.bundle.Actions[cnab.ActionValidate]; !declared {
			return Run{}, fmt.Errorf("the bundle %s has no %s action: it was built by an older nemo", plan.bundle.Name, cnab.ActionValidate)
		}
		action = cnab.ActionValidate
	}
	values, err := credentials.Resolve(plan.credentials, plan.bundle, action)
	if err != nil {
		return Run{}, err
	}
	run, err := nemo.execute(ctx, runtime.Request{
		Bundle:      plan.bundle,
		Stack:       request.Stack,
		Action:      action,
		Target:      plan.target,
		Installer:   plan.installer,
		Parameters:  plan.parameters,
		Credentials: values,
		Relocation:  plan.relocation,
		Source:      plan.source,
		Custom:      storage.RunCustom{Target: plan.target, Installer: plan.installer, Credentials: plan.credentials, RelocationMap: plan.relocation},
		Progress:    events,
	})
	if run.Claim.ID == "" || request.Validate {
		return Run{}, err
	}

	run = redact(plan.bundle, run)
	if saveErr := nemo.save(ctx, plan, run); saveErr != nil {
		err = errors.Join(err, fmt.Errorf("the action ran, but could not be recorded: %w", saveErr))
	}
	return Run{
		Installation: run.Claim.Installation,
		Revision:     run.Claim.Revision,
		Action:       run.Claim.Action,
		Bundle:       BundleVersion{Name: plan.bundle.Name, Version: plan.bundle.Version},
		Target:       plan.target,
		Driver:       plan.installer.DriverName(),
		Parameters:   run.Claim.Parameters,
		Credentials:  slices.Sorted(maps.Keys(values)),
		Status:       run.Result.Status,
		Started:      run.Claim.Created,
		Ended:        run.Result.Created,
		Outputs:      run.Outputs,
	}, err
}

// actionPlan is what an action runs with: the request, completed by the installation.
type actionPlan struct {
	stack       string
	bundle      cnab.ExtendedBundle
	reference   storage.BundleReference
	target      target.Target
	installer   target.Installer
	parameters  map[string]string
	credentials []valuesource.Strategy
	relocation  relocation.ImageRelocationMap
	source      string
	digest      string
}

// plan is what an action runs: its bundle, the request's, else the one of
// the installation nemo keeps, and its target, installer, parameters and
// credentials, from the request, and from the installation unless it is a
// fresh install.
func (nemo *Nemo) plan(ctx context.Context, action string, request ActionRequest, loaded *cnab.BundleReference) (actionPlan, error) {
	plan := actionPlan{stack: request.Stack}
	given, err := nemo.givenBundle(ctx, request, loaded)
	if err != nil {
		return plan, err
	}
	name := request.Name
	if name == "" && given != nil {
		name = given.Definition.Name
	}
	if request.Stack == "" || name == "" {
		return plan, errors.New("give the stack, and the bundle's name or its file")
	}

	stored, err := nemo.store.GetInstallation(ctx, request.Stack, name)
	known := err == nil
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return plan, err
	}
	installed := known && stored.IsInstalled()
	if err := allowed(action, request.Stack+"/"+name, given != nil, known, installed); err != nil {
		return plan, err
	}

	if given != nil {
		plan.bundle, plan.relocation, plan.source, plan.digest = given.Definition, given.RelocationMap, request.BundleSource, given.Digest.String()
		plan.reference = cmp.Or(request.Bundle, referenceOf(request.BundleSource, plan.bundle.Version))
	} else {
		if plan.bundle, err = nemo.bundleOf(ctx, stored); err != nil {
			return plan, err
		}
		plan.reference, plan.relocation, plan.source, plan.digest = stored.Bundle, stored.RelocationMap, stored.Status.BundleReference, stored.Status.BundleDigest
	}

	plan.target, plan.installer = request.Target, request.Installer
	plan.parameters, plan.credentials = maps.Clone(request.Parameters), request.Credentials
	if known && (installed || action != cnab.ActionInstall) {
		return plan, plan.goOnFrom(stored, request)
	}
	return plan, nil
}

// givenBundle is the request's bundle: the one loaded already, else the one
// at its source, or nil without either. It must be the installation's.
func (nemo *Nemo) givenBundle(ctx context.Context, request ActionRequest, loaded *cnab.BundleReference) (*cnab.BundleReference, error) {
	if loaded == nil && request.BundleSource != "" {
		reference, err := nemo.load(ctx, request.BundleSource)
		if err != nil {
			return nil, err
		}
		loaded = &reference
	}
	if loaded != nil && request.Name != "" && request.Name != loaded.Definition.Name {
		return nil, fmt.Errorf("the bundle at %s is %s, not %s", request.BundleSource, loaded.Definition.Name, request.Name)
	}
	return loaded, nil
}

// allowed tells whether the action can run on the installation: an install
// needs a bundle, an upgrade an installation, and an installation nemo does
// not know needs its bundle.
func allowed(action, installation string, hasBundle, known, installed bool) error {
	switch {
	case action == cnab.ActionInstall && !hasBundle:
		return errors.New("install needs the bundle's source")
	case action == cnab.ActionUpgrade && !installed:
		return fmt.Errorf("%s is not installed", installation)
	case !known && !hasBundle:
		return fmt.Errorf("nemo does not know %s: give its bundle's source and its target", installation)
	}
	return nil
}

// goOnFrom takes from the installation what the request leaves: its target,
// which must stay, its installer unless one is given, and its parameters and
// credentials, under the request's.
func (plan *actionPlan) goOnFrom(stored storage.Installation, request ActionRequest) error {
	var err error
	if plan.target, err = sameTarget(stored, request.Target); err != nil {
		return err
	}
	if request.Installer.Driver == "" && request.Installer.Kubernetes == nil {
		plan.installer = stored.Installer
	}
	plan.parameters = declared(plan.bundle.Parameters, stored.Parameters, request.Parameters)
	plan.credentials = credentials.Merge(plan.bundle, stored.Credentials, request.Credentials)
	return nil
}

// bundleOf returns the bundle the installation's last action ran, from its
// claim, where Porter keeps it too.
func (nemo *Nemo) bundleOf(ctx context.Context, installation storage.Installation) (cnab.ExtendedBundle, error) {
	run, err := nemo.store.GetRun(ctx, installation.Stack, installation.Name, installation.Status.RunID)
	if err != nil {
		return cnab.ExtendedBundle{}, fmt.Errorf("the bundle of %s: %w", installation.ID(), err)
	}
	bundle, err := cnab.NewBundle(run.Bundle)
	if err != nil {
		return cnab.ExtendedBundle{}, fmt.Errorf("the bundle of %s: %w", installation.ID(), err)
	}
	return bundle, nil
}

// referenceOf is the reference of a bundle given by its source, as Porter
// writes it: the repository and tag of a registry, or the bundle.json. A
// layout is no repository: the installation's status says where it is.
func referenceOf(source, version string) storage.BundleReference {
	location, err := bundle.ParseLocation(source)
	switch {
	case err != nil:
		return storage.BundleReference{File: source}
	case location.Transport == bundle.Registry:
		if reference, err := cnab.ParseOCIReference(location.Reference); err == nil && reference.HasTag() {
			return storage.BundleReference{Repository: reference.Repository(), Version: reference.Tag()}
		}
	}
	return storage.BundleReference{Version: version}
}

// LoadBundle reads a bundle from a bundle.json, or pulls it from where it was pushed.
func LoadBundle(ctx context.Context, source string) (cnab.BundleReference, error) {
	if location, err := bundle.ParseLocation(source); err == nil {
		return bundle.Pull(ctx, location)
	}
	extended, err := cnab.LoadBundle(source)
	return cnab.BundleReference{Definition: extended}, err
}

// sameTarget returns the installation's target, completed by the one
// given, which can only repeat it: an installation is not moved. Its volume
// types can change, the volumes that exist kept as they are, and so can how
// its images reach it.
func sameTarget(stored storage.Installation, given target.Target) (target.Target, error) {
	target := stored.Target
	if (given.Host != "" || given.Context != "") && (given.Host != target.Host || given.Context != target.Context) {
		where := "the environment's Docker"
		switch {
		case target.Host != "":
			where = target.Host
		case target.Context != "":
			where = "the docker context " + target.Context
		}
		return target, fmt.Errorf("%s is deployed on %s: uninstall it there first", stored.ID(), where)
	}
	if given.Orchestrator != "" && given.Orchestrator != target.Orchestrator {
		return target, fmt.Errorf("%s is deployed with %s: uninstall it with %s first", stored.ID(), target.Orchestrator, target.Orchestrator)
	}
	if given.Volumes != nil {
		target.Volumes = given.Volumes
	}
	if given.Images != "" {
		target.Images = given.Images
	}
	return target, nil
}

// declared returns the stored values the bundle still declares, overridden
// by the values given.
func declared[V, D any](declarations map[string]D, stored, given map[string]V) map[string]V {
	result := map[string]V{}
	for name, value := range stored {
		if _, ok := declarations[name]; ok {
			result[name] = value
		}
	}
	maps.Copy(result, given)
	return result
}

// redact returns the run with the values of its sensitive parameters and
// outputs replaced: what is recorded or returned never holds them.
func redact(bundle cnab.ExtendedBundle, run runtime.Run) runtime.Run {
	parameters := map[string]any{}
	for name, value := range run.Claim.Parameters {
		if bundle.IsSensitiveParameter(name) {
			value = Redacted
		}
		parameters[name] = value
	}
	run.Claim.Parameters = parameters

	outputs := map[string]string{}
	for name, value := range run.Outputs {
		if output, ok := bundle.Outputs[name]; ok {
			if definition, ok := bundle.Definitions[output.Definition]; ok && definition.WriteOnly != nil && *definition.WriteOnly {
				value = Redacted
			}
		}
		outputs[name] = value
	}
	run.Outputs = outputs
	return run
}

// save records the run and its result, redacted, then the installation as
// the run left it, as Porter sums it up. Nothing secret is written:
// credentials are kept by source only.
func (nemo *Nemo) save(ctx context.Context, plan actionPlan, run runtime.Run) error {
	bundle, claim, result := plan.bundle, run.Claim, run.Result
	if err := nemo.store.SaveRun(ctx, plan.stack, bundle.Name, claim); err != nil {
		return err
	}
	if err := nemo.store.SaveResult(ctx, plan.stack, bundle.Name, result, run.Outputs); err != nil {
		return err
	}

	stored, err := nemo.store.GetInstallation(ctx, plan.stack, bundle.Name)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	status := stored.Status
	if status.Created.IsZero() {
		status.Created = claim.Created
	}
	status.RunID, status.Action, status.ResultID, status.ResultStatus, status.Modified = claim.ID, claim.Action, result.ID, result.Status, result.Created
	status.BundleReference, status.BundleVersion, status.BundleDigest = plan.source, bundle.Version, plan.digest
	if result.Status == cnab.StatusSucceeded {
		switch claim.Action {
		case cnab.ActionInstall, cnab.ActionUpgrade:
			if status.Installed == nil || status.Uninstalled != nil {
				status.Installed = &result.Created
			}
			status.Uninstalled = nil
		case cnab.ActionUninstall:
			status.Uninstalled = &result.Created
		}
	}

	parameters := map[string]string{}
	for name, value := range plan.parameters {
		if name != cnab.ParameterOrchestrator && !bundle.IsSensitiveParameter(name) {
			parameters[name] = value
		}
	}
	return nemo.store.SaveInstallation(ctx, storage.Installation{
		InstallationSpec: storage.InstallationSpec{
			Name:        bundle.Name,
			Stack:       plan.stack,
			Bundle:      plan.reference,
			Parameters:  parameters,
			Credentials: plan.credentials,
		},
		Target:        plan.target,
		Installer:     plan.installer,
		RelocationMap: plan.relocation,
		Status:        status,
	})
}
