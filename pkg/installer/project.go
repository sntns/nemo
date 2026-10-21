package installer

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/opencontainers/go-digest"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/labels"
	"github.com/sntns/nemo/pkg/target"
)

// AppDir is the bundle's own directory in the invocation image: the compose
// project resolves its relative paths there.
const AppDir = "/cnab/app"

// Installation identifies what an operation installs. Its fields go on
// every object the orchestrator creates.
type Installation struct {
	Stack    string
	Bundle   string
	Version  string
	Revision string
}

// Project is the compose project of an operation, ready for an orchestrator.
type Project struct {
	Installation Installation

	// Compose is the compose project, named after the stack. Its configs and
	// secrets hold their content: an orchestrator never reads a file or a
	// variable of the installer to get it.
	Compose *types.Project

	// Levels holds the services in the order of their depends_on: each level
	// only depends on the levels before it.
	Levels [][]string

	// Labels go on every object of the installation.
	Labels map[string]string

	// Provisions holds, by volume name, the directory to create before the
	// volume, as its target's volume type says.
	Provisions map[string]target.VolumeProvision

	// InvocationImage is the installer's own image, which provisions the
	// volumes' directories.
	InvocationImage string

	// RegistryAuth holds the credentials the images are pulled with.
	RegistryAuth RegistryAuth
}

// LoadProject builds the compose project of the operation's bundle.
//
// The compose documents are interpolated with the parameters, each under the
// name of its environment destination, so that ${REGION} reads the same as
// with docker compose. Credentials are not: they reach the containers only
// as compose secrets, from their environment variable or their file. For an
// uninstall, configs and secrets are left without their content.
func LoadProject(ctx context.Context, operation Operation) (*Project, error) {
	bundle := operation.Bundle
	if bundle.Compose == nil {
		return nil, fmt.Errorf("bundle %s: no %s extension: it was not built for the nemo installer", bundle.Name, cnab.ComposeExtension.Key)
	}

	// Porter and nemo name installations <namespace>/<name>: the stack is
	// the namespace, or the name when there is none.
	stack := operation.Installation
	if namespace, name, found := strings.Cut(stack, "/"); found {
		stack = cmp.Or(namespace, name)
	}
	installation := Installation{
		Stack:    stack,
		Bundle:   bundle.Name,
		Version:  bundle.Version,
		Revision: operation.Revision,
	}

	variables, err := operation.parameterVariables()
	if err != nil {
		return nil, err
	}
	compose, err := bundle.Compose.Load(ctx, installation.Stack, AppDir, variables)
	if err != nil {
		return nil, fmt.Errorf("loading the compose project of %s: %w", bundle.Name, err)
	}

	// An uninstall creates nothing, so it needs no content; and the
	// credentials that hold it may not apply to it, nor be delivered.
	if operation.Action != cnab.ActionUninstall {
		if err := operation.resolveFileObjects(compose, variables); err != nil {
			return nil, err
		}
	}

	if err := operation.pinImages(compose); err != nil {
		return nil, err
	}
	opts, err := optionsOf(operation.Parameters)
	if err != nil {
		return nil, err
	}
	provisions, err := resolveVolumes(compose, installation.Stack, opts.VolumeTypes)
	if err != nil {
		return nil, err
	}

	levels, err := levels(compose.Services)
	if err != nil {
		return nil, err
	}

	return &Project{
		Installation: installation,
		Compose:      compose,
		Levels:       levels,
		Labels: map[string]string{
			labels.Stack:   installation.Stack,
			labels.Bundle:  installation.Bundle,
			labels.Version: installation.Version,
		},
		Provisions:      provisions,
		InvocationImage: operation.invocationImage(),
		RegistryAuth:    opts.RegistryAuth,
	}, nil
}

// parameterVariables returns the parameters that have an environment
// destination, in their string form. A parameter the runtime did not
// deliver, because it does not apply to the action or has no value, reads
// as blank, as an unset variable does with docker compose, but without its
// warning: only a variable the bundle does not declare deserves one.
func (operation Operation) parameterVariables() (types.Mapping, error) {
	variables := types.Mapping{}
	for name, parameter := range operation.Bundle.Parameters {
		destination := parameter.Destination
		if destination == nil || destination.EnvironmentVariable == "" {
			continue
		}

		value, delivered := operation.Parameters[name]
		if !delivered {
			variables[destination.EnvironmentVariable] = ""
			continue
		}
		text, err := cnab.WriteParameterToString(name, value)
		if err != nil {
			return nil, err
		}
		variables[destination.EnvironmentVariable] = text
	}
	return variables, nil
}

// resolveFileObjects reads the content of every config and secret that is
// not external into its Content: from an environment variable the runtime
// delivered, one of the parameters' variables or a credential, or from a
// file of the invocation image.
func (operation Operation) resolveFileObjects(compose *types.Project, parameters types.Mapping) error {
	variables := maps.Clone(parameters)
	for name, value := range operation.Credentials {
		if variable := operation.Bundle.Credentials[name].EnvironmentVariable; variable != "" {
			variables[variable] = value
		}
	}

	resolve := func(kind, key string, object types.FileObjectConfig) (types.FileObjectConfig, error) {
		switch {
		case bool(object.External), object.Content != "":
			return object, nil
		case object.Environment != "":
			value, ok := variables[object.Environment]
			if !ok {
				return object, fmt.Errorf("%s.%s: the variable %s is neither a parameter nor a credential of the bundle", kind, key, object.Environment)
			}
			object.Content, object.Environment = value, ""
		case object.File != "":
			data, err := operation.container.readFile(object.File)
			if err != nil {
				return object, fmt.Errorf("%s.%s: %w", kind, key, err)
			}
			object.Content, object.File = string(data), ""
		default:
			return object, fmt.Errorf("%s.%s: it needs content, environment or file", kind, key)
		}
		return object, nil
	}

	for _, key := range slices.Sorted(maps.Keys(compose.Configs)) {
		object, err := resolve("configs", key, types.FileObjectConfig(compose.Configs[key]))
		if err != nil {
			return err
		}
		compose.Configs[key] = types.ConfigObjConfig(object)
	}
	for _, key := range slices.Sorted(maps.Keys(compose.Secrets)) {
		object, err := resolve("secrets", key, types.FileObjectConfig(compose.Secrets[key]))
		if err != nil {
			return err
		}
		compose.Secrets[key] = types.SecretConfig(object)
	}
	return nil
}

// pinImages points each service at the image the bundle declares for it in
// its images: where the runtime relocated it, else its content digest, so
// that every node runs the image the bundle was built with. An image the
// bundle does not declare stays as written, unless the images were
// relocated: it would then not be where the target can pull it.
func (operation Operation) pinImages(compose *types.Project) error {
	declared := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(operation.Bundle.Images)) {
		image := operation.Bundle.Images[key]
		reference, err := cnab.ParseOCIReference(image.Image)
		if err != nil {
			return fmt.Errorf("images.%s: %w", key, err)
		}
		declared[reference.Named.String()] = key
	}

	for _, name := range slices.Sorted(maps.Keys(compose.Services)) {
		service := compose.Services[name]
		if service.Image == "" {
			continue
		}
		reference, err := cnab.ParseOCIReference(service.Image)
		if err != nil {
			return fmt.Errorf("services.%s.image: %w", name, err)
		}
		key, ok := declared[reference.Named.String()]
		image := operation.Bundle.Images[key]
		relocated, isRelocated := operation.Relocation[image.Image]
		switch {
		case ok && isRelocated:
			service.Image = relocated
		case len(operation.Relocation) > 0:
			return fmt.Errorf("services.%s.image: %s was not relocated: declare it in the bundle's images", name, service.Image)
		case ok && image.Digest != "" && !reference.HasDigest():
			pinned, err := reference.WithDigest(digest.Digest(image.Digest))
			if err != nil {
				return fmt.Errorf("images.%s.contentDigest: %w", key, err)
			}
			service.Image = pinned.String()
		}
		compose.Services[name] = service
	}
	return nil
}

// invocationImage returns the installer's image as the target has it:
// where the runtime relocated it, else as the bundle names it.
func (operation Operation) invocationImage() string {
	image := operation.Bundle.InvocationImages[0].Image
	if relocated, ok := operation.Relocation[image]; ok {
		return relocated
	}
	return image
}

// levels orders the services by their depends_on: each level only depends on
// the levels before it, and the services of a level are sorted by name.
func levels(services types.Services) ([][]string, error) {
	var result [][]string
	remaining := slices.Sorted(maps.Keys(services))
	placed := map[string]bool{}
	for len(remaining) > 0 {
		var level, rest []string
		for _, name := range remaining {
			ready := true
			for dependency := range services[name].DependsOn {
				if _, declared := services[dependency]; declared && !placed[dependency] {
					ready = false
				}
			}
			if ready {
				level = append(level, name)
			} else {
				rest = append(rest, name)
			}
		}
		if len(level) == 0 {
			return nil, fmt.Errorf("depends_on has a cycle between %s", strings.Join(rest, ", "))
		}
		for _, name := range level {
			placed[name] = true
		}
		result = append(result, level)
		remaining = rest
	}
	return result, nil
}
