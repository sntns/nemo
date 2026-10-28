// Package runtime runs the actions of a bundle's installations, as a CNAB
// runtime: it builds the claim, hands the operation to the driver that runs
// the invocation image, and returns the claim, its result and its outputs.
// It knows nothing about orchestrators: the target names one, and the
// installer applies it.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cnabio/cnab-go/action"
	"github.com/cnabio/cnab-go/driver"
	"github.com/cnabio/cnab-go/valuesource"
	"github.com/cnabio/cnab-to-oci/relocation"
	"github.com/docker/cli/cli/config"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/hashicorp/go-multierror"
	"github.com/moby/moby/client"

	nemobundle "github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/progress"
	"github.com/sntns/nemo/pkg/target"
	"github.com/sntns/nemo/pkg/transfer"
)

// ErrNotImplemented is returned for what is planned but not built yet.
var ErrNotImplemented = errors.New("not implemented")

// Request is one action on one installation.
type Request struct {
	Bundle cnab.ExtendedBundle
	// Stack is the stack the installation belongs to. The installation is
	// named <stack>/<bundle>.
	Stack  string
	Action string
	// Target receives the deployment; Installer says where the invocation
	// image runs.
	Target    target.Target
	Installer target.Installer

	// Parameters holds the values given for the bundle's parameters, as
	// text. They are converted to the type their definition says.
	Parameters map[string]string

	// Credentials holds the resolved values of the bundle's credentials.
	Credentials valuesource.Set

	// Relocation maps the bundle's images to where they are, when the
	// bundle was pulled from a registry: the invocation image runs from
	// there, and the installer gets the map.
	Relocation relocation.ImageRelocationMap

	// Source is where the bundle came from, a bundle.json or a location:
	// its images are read from there when the target loads them. It is the
	// claim's bundleReference.
	Source string

	// Custom is what the runtime records in the claim besides CNAB's own.
	Custom any

	// Progress receives the events of the action, about its installation:
	// the loading of its images, and what its installer does.
	Progress progress.Sink
}

// Run is what an action did.
type Run struct {
	Claim   cnab.Claim
	Result  cnab.Result
	Outputs map[string]string
}

// stackName is a stack's name: a directory of the store, without /.
var stackName = regexp.MustCompile(`^[^/]+$`)

// Execute runs the action. The error is set when the action could not run,
// or when it ran and failed; the Run is filled as far as the action went.
func Execute(ctx context.Context, request Request) (Run, error) {
	bundle := request.Bundle
	invalid := validation.Errors{
		"stack":     validation.Validate(request.Stack, validation.Required, validation.Match(stackName).Error("must be a name without /")),
		"target":    request.Target.Validate(),
		"installer": request.Installer.Validate(),
	}.Filter()
	if err := errors.Join(invalid, bundle.CheckRequiredExtensions()); err != nil {
		return Run{}, err
	}

	registryAuth, err := registryAuthOf(bundle, request.Relocation, config.LoadDefaultConfigFile(io.Discard))
	if err != nil {
		return Run{}, fmt.Errorf("reading the registries' credentials: %w", err)
	}
	parameters, err := parametersOf(request, registryAuth)
	if err != nil {
		return Run{}, err
	}
	installation := request.Stack + "/" + bundle.Name
	events := progress.Discard
	if request.Progress != nil {
		events = request.Progress
	}
	loaded := request.Target.Images == target.ImagesLoad
	if loaded {
		if request.Relocation, err = loadImages(ctx, request, installation, events); err != nil {
			return Run{}, err
		}
	}
	invocationDriver, err := newDriver(ctx, request.Target, request.Installer, bundle)
	if err != nil {
		return Run{}, err
	}
	claim, err := newClaim(installation, request.Action, bundle, parameters)
	if err != nil {
		return Run{}, err
	}
	claim.BundleReference, claim.Custom = request.Source, request.Custom
	// The installer writes its events as JSON, which the writer reads back about the installation.
	output := progress.NewWriter(events, installation)
	defer output.Close()
	operationResult, result, err := action.New(invocationDriver).Run(ctx, claim, request.Credentials, writingTo(output), relocating(request.Relocation, loaded))
	run := Run{Claim: claim, Result: result, Outputs: operationResult.Outputs}
	if err != nil {
		return run, err
	}
	if operationResult.Error != nil {
		return run, &FailedError{Action: request.Action, Installation: claim.Installation, Cause: failure(operationResult.Error)}
	}
	return run, nil
}

// writingTo has the installer write its output, its events as JSON, to the writer.
func writingTo(output io.Writer) action.OperationConfigFunc {
	return func(operation *driver.Operation) error {
		operation.Out, operation.Err = output, output
		if operation.Environment == nil {
			operation.Environment = map[string]string{}
		}
		operation.Environment[progress.Variable] = progress.FormatJSON
		return nil
	}
}

// relocating runs the invocation image where the map puts it, and gives
// the installer the map.
func relocating(relocationMap relocation.ImageRelocationMap, loaded bool) action.OperationConfigFunc {
	return func(operation *driver.Operation) error {
		if len(relocationMap) == 0 {
			return nil
		}
		if relocated, ok := relocationMap[operation.Image.Image]; ok {
			operation.Image.Image = relocated
			// A loaded image has no repository digest to check; it was loaded by its digest.
			if loaded {
				operation.Image.Digest = ""
			}
		}
		data, err := json.Marshal(relocationMap)
		if err != nil {
			return err
		}
		if operation.Files == nil {
			operation.Files = map[string]string{}
		}
		operation.Files[cnab.RelocationMapFile] = string(data)
		return nil
	}
}

// FailedError is an action the installer ran, and that failed.
type FailedError struct {
	Action       string
	Installation string
	// Cause is how the installer ended; its output says why.
	Cause string
}

func (failed *FailedError) Error() string {
	return fmt.Sprintf("%s of %s failed, see the installer's output: %s", failed.Action, failed.Installation, failed.Cause)
}

// failure flattens the errors cnab-go gathers about an operation into one line.
func failure(err error) string {
	var gathered *multierror.Error
	if !errors.As(err, &gathered) {
		return err.Error()
	}
	messages := make([]string, len(gathered.Errors))
	for i, cause := range gathered.Errors {
		messages[i] = cause.Error()
	}
	return strings.Join(messages, "; ")
}

// newClaim builds the claim of the action. The installation is named
// <stack>/<bundle>, as Porter names it <namespace>/<name>; cnab-go's
// claim.New refuses the /, so the claim is built here.
func newClaim(installation, action string, bundle cnab.ExtendedBundle, parameters map[string]any) (cnab.Claim, error) {
	created := cnab.Claim{
		SchemaVersion: cnab.ClaimSchemaVersion(),
		ID:            cnab.NewULID(),
		Installation:  installation,
		Revision:      cnab.NewULID(),
		Created:       time.Now(),
		Action:        action,
		Bundle:        bundle.Bundle,
		Parameters:    parameters,
	}
	return created, created.Validate()
}

// withOwnParameters returns the parameters given, with those nemo sets
// itself, from where they come: the orchestrator and the volume types from
// the target, the registries' credentials from docker login. Each is set
// when the bundle declares it, and refused when given.
func withOwnParameters(request Request, registryAuth string) (map[string]string, error) {
	given := maps.Clone(request.Parameters)
	if given == nil {
		given = map[string]string{}
	}
	var volumes string
	if len(request.Target.Volumes) > 0 {
		encoded, err := json.Marshal(request.Target.Volumes)
		if err != nil {
			return nil, err
		}
		volumes = string(encoded)
	}
	own := []struct{ name, from, value string }{
		{cnab.ParameterOrchestrator, "the target", request.Target.Orchestrator},
		{cnab.ParameterVolumes, "the target", volumes},
		{cnab.ParameterRegistryAuth, "docker login", registryAuth},
	}
	for _, parameter := range own {
		if _, set := given[parameter.name]; set {
			return nil, fmt.Errorf("parameter %s comes from %s, it can't be given", parameter.name, parameter.from)
		}
		if _, declared := request.Bundle.Parameters[parameter.name]; declared && parameter.value != "" {
			given[parameter.name] = parameter.value
		}
	}
	return given, nil
}

// parametersOf returns the value of every parameter that applies to the
// action: the one given, converted to its definition's type, else its
// default. The orchestrator and the volume types come from the target, and
// the registries' credentials from the runtime, never from a value given.
func parametersOf(request Request, registryAuth string) (map[string]any, error) {
	bundle := request.Bundle
	given, err := withOwnParameters(request, registryAuth)
	if err != nil {
		return nil, err
	}

	var errs []error
	for _, name := range slices.Sorted(maps.Keys(given)) {
		if _, declared := bundle.Parameters[name]; !declared {
			errs = append(errs, fmt.Errorf("parameter %s is not declared by the bundle %s", name, bundle.Name))
		}
	}

	values := map[string]any{}
	for _, name := range slices.Sorted(maps.Keys(bundle.Parameters)) {
		parameter := bundle.Parameters[name]
		if !parameter.AppliesTo(request.Action) {
			continue
		}
		definition, ok := bundle.Definitions[parameter.Definition]
		if !ok {
			errs = append(errs, fmt.Errorf("parameter %s: no definition %s", name, parameter.Definition))
			continue
		}

		var value any
		if text, set := given[name]; set {
			converted, err := bundle.ConvertParameterValue(name, text)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			value = converted
		} else if definition.Default != nil {
			value = definition.Default
		} else {
			if parameter.Required {
				errs = append(errs, fmt.Errorf("parameter %s is required for %s", name, request.Action))
			}
			continue
		}

		problems, err := definition.Validate(value)
		if err != nil {
			errs = append(errs, fmt.Errorf("parameter %s: %w", name, err))
			continue
		}
		for _, problem := range problems {
			errs = append(errs, fmt.Errorf("parameter %s: %s", name, problem.Error))
		}
		values[name] = value
	}
	return values, errors.Join(errs...)
}

// loadImages loads the bundle's images into the target's daemon and
// returns where each one is now, over where the bundle was relocated. Only
// one daemon has them: the target must run Compose, or a swarm of one node.
func loadImages(ctx context.Context, request Request, installation string, events progress.Sink) (relocation.ImageRelocationMap, error) {
	cli, err := dockerCLI(request.Target)
	if err != nil {
		return nil, err
	}
	api := cli.Client()
	if request.Target.Orchestrator == target.Swarm {
		info, err := api.Info(ctx, client.InfoOptions{})
		if err != nil {
			return nil, fmt.Errorf("reaching the target's Docker: %w", err)
		}
		if nodes := info.Info.Swarm.Nodes; nodes > 1 {
			return nil, fmt.Errorf("target: images are loaded into one daemon, and this swarm has %d nodes: let them pull from a registry", nodes)
		}
	}
	images := nemobundle.ImagesOf(request.Bundle, request.Relocation, request.Source)
	// A Job runs the installer in the cluster, which pulls it from its registry.
	if request.Installer.DriverName() == target.DriverKubernetes {
		images = slices.DeleteFunc(images, func(image transfer.Image) bool { return image.Name == request.Bundle.InvocationImages[0].Image })
	}
	loaded, err := transfer.Load(ctx, api, images, progress.About(events, installation))
	if err != nil {
		return nil, err
	}
	relocated := maps.Clone(request.Relocation)
	if relocated == nil {
		relocated = relocation.ImageRelocationMap{}
	}
	maps.Copy(relocated, loaded)
	return relocated, nil
}

// newDriver returns the driver the installer names, once it reached what it
// runs the invocation image on.
func newDriver(ctx context.Context, deploymentTarget target.Target, installer target.Installer, bundle cnab.ExtendedBundle) (driver.Driver, error) {
	switch installer.DriverName() {
	case target.DriverDocker:
		return newDockerDriver(ctx, deploymentTarget, bundle)
	case target.DriverKubernetes:
		return newKubernetesDriver(deploymentTarget, *installer.Kubernetes)
	}
	return nil, fmt.Errorf("driver %s: %w", installer.Driver, ErrNotImplemented)
}
