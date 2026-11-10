// Package bundle builds nemo bundles from a compose project, and moves
// them between registries and OCI layout directories.
package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/compose-spec/compose-go/v2/types"
	"go.yaml.in/yaml/v3"

	"github.com/sntns/nemo/internal/decode"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/transfer"
)

// Extension is the block of a bundle's compose that describes the bundle,
// as x-sntns-bundle did.
const Extension = "x-nemo-bundle"

// Metadata is the content of x-nemo-bundle.
type Metadata struct {
	Name        string              `json:"name"`
	Version     string              `json:"version,omitempty"`
	Description string              `json:"description,omitempty"`
	License     string              `json:"license,omitempty"`
	Keywords    []string            `json:"keywords,omitempty"`
	Maintainers []bundle.Maintainer `json:"maintainers,omitempty"`
	// Dependencies are the bundles this one needs, as io.cnab.dependencies
	// declares them: each a REGISTRY/NAME reference with version ranges.
	Dependencies *cnab.Dependencies `json:"dependencies,omitempty"`
	// Outputs are the values the bundle gives the bundles that require it,
	// by name.
	Outputs map[string]Output `json:"outputs,omitempty"`
	// Parameters give parameters of the bundle a source, by name.
	Parameters map[string]Parameter `json:"parameters,omitempty"`
}

// Output is an output of the bundle: a value made of its variables, as its
// compose is, which the installer writes once it installs or upgrades it.
type Output struct {
	Description string `json:"description,omitempty"`
	Value       string `json:"value"`
	// Sensitive keeps the value out of what nemo records and prints.
	Sensitive bool `json:"sensitive,omitempty"`
}

// Parameter is what x-nemo-bundle says of a parameter of the bundle.
type Parameter struct {
	Source ParameterSource `json:"source"`
}

// ParameterSource is where a parameter is read from: an output of a bundle
// this one requires, by its key in dependencies.requires.
type ParameterSource struct {
	Dependency string `json:"dependency"`
	Output     string `json:"output"`
}

// BuildRequest builds a bundle from compose files.
type BuildRequest struct {
	// ComposeFiles are merged in order; one of them has x-nemo-bundle.
	ComposeFiles []string
	// Version overrides the version of x-nemo-bundle.
	Version string
	// Images maps each image name of the compose to the location it is
	// built at: docker://REFERENCE or oci:PATH[:REFERENCE].
	Images map[string]string
	// Installer is the invocation image: a location, docker://REFERENCE or
	// oci:PATH, to carry it with the bundle, or the name of an image the
	// target has.
	Installer string
}

// Sources returns the images of the bundle read where they are built, by
// the name the bundle gives them, the installer's included when it is a
// location.
func (request BuildRequest) Sources() ([]transfer.Image, error) {
	var sources []transfer.Image
	for name, text := range request.Images {
		location, err := ParseLocation(text)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", name, err)
		}
		sources = append(sources, location.Image(name))
	}
	if location, err := ParseLocation(request.Installer); err == nil {
		sources = append(sources, location.Image(location.ImageName()))
	}
	return sources, nil
}

// Build returns the bundle of the compose files: their metadata, a
// parameter per variable, a credential per secret read from a variable,
// the images with their digest, the nemo-* parameters, the compose
// project itself in io.nemo.compose, and the outputs and the sources of
// parameters x-nemo-bundle gives.
func Build(ctx context.Context, request BuildRequest) (cnab.ExtendedBundle, error) {
	documents, metadata, err := readCompose(request.ComposeFiles)
	if err != nil {
		return cnab.ExtendedBundle{}, err
	}
	version := metadata.Version
	if request.Version != "" {
		version = request.Version
	}

	built := bundle.Bundle{
		SchemaVersion: bundle.GetDefaultSchemaVersion(),
		Name:          metadata.Name,
		Version:       version,
		Description:   metadata.Description,
		License:       metadata.License,
		Keywords:      metadata.Keywords,
		Maintainers:   metadata.Maintainers,
		Definitions:   nemoDefinitions(),
		Parameters:    nemoParameters(),
		Credentials: map[string]bundle.Credential{
			cnab.CredentialOverride: {Location: bundle.Location{EnvironmentVariable: "NEMO_CREDENTIALS"}, ApplyTo: []string{cnab.ActionInstall, cnab.ActionUpgrade}, Description: "The secrets the override reads its secrets and configs from, as JSON by variable"},
		},
		Actions:            map[string]bundle.Action{cnab.ActionValidate: {Stateless: true, Description: "Check the bundle against the target, and change nothing"}},
		RequiredExtensions: []string{cnab.DockerExtension.Key},
	}
	compose := cnab.Compose{SchemaVersion: cnab.ComposeSchemaVersion, Documents: documents}
	extensionsErr := errors.Join(
		cnab.DockerExtension.Write(&built, cnab.Docker{}),
		cnab.ComposeExtension.Write(&built, compose),
	)
	if metadata.Dependencies != nil {
		extensionsErr = errors.Join(extensionsErr, cnab.DependenciesExtension.Write(&built, *metadata.Dependencies))
	}
	if extensionsErr != nil {
		return cnab.ExtendedBundle{}, extensionsErr
	}

	installer, err := resolveInstaller(ctx, request.Installer)
	if err != nil {
		return cnab.ExtendedBundle{}, err
	}
	built.InvocationImages = []bundle.InvocationImage{{BaseImage: installer}}

	buildTime := cnab.Compose{Documents: buildable(documents)}
	project, err := buildTime.Load(ctx, metadata.Name, filepath.Dir(request.ComposeFiles[0]), types.Mapping{}, "")
	if err != nil {
		return cnab.ExtendedBundle{}, fmt.Errorf("loading the compose project: %w", err)
	}
	variablesErr := declareVariables(&built, documents, project, metadata.Outputs)
	images, imagesErr := resolveImages(ctx, project, request.Images)
	if err := errors.Join(variablesErr, imagesErr); err != nil {
		return cnab.ExtendedBundle{}, err
	}
	if err := errors.Join(declareOutputs(&built, metadata.Outputs), declareSources(&built, metadata.Parameters, metadata.Dependencies)); err != nil {
		return cnab.ExtendedBundle{}, err
	}
	built.Images = images
	return cnab.NewBundle(built)
}

// buildable returns the documents as the build loads them, knowing no value of the variables yet. A
// value that needs a variable without a default is left out: whatever it will be, it is checked
// when the bundle runs. A value whose variables all have a default is checked with them. An image
// keeps its variables, escaped: the build resolves it as written; so does a config's content, which
// has no type to check and can't be left out.
func buildable(documents []cnab.ComposeDocument) []cnab.ComposeDocument {
	result := make([]cnab.ComposeDocument, len(documents))
	for i, document := range documents {
		config, _ := withoutUnknowns(document.Config, nil).(map[string]any)
		result[i] = cnab.ComposeDocument{Name: document.Name, Config: config}
	}
	return result
}

// withoutUnknowns copies value without what needs an unknown variable, and returns nil when value
// itself does.
func withoutUnknowns(value any, path []string) any {
	switch value := value.(type) {
	case map[string]any:
		copied := map[string]any{}
		for key, element := range value {
			if element = withoutUnknowns(element, append(slices.Clone(path), key)); element != nil {
				copied[key] = element
			}
		}
		return copied
	case []any:
		copied := []any{}
		for _, element := range value {
			if element = withoutUnknowns(element, path); element != nil {
				copied = append(copied, element)
			}
		}
		return copied
	case string:
		if len(path) == 3 && (path[0] == "services" && path[2] == "image" || path[0] == "configs" && path[2] == "content") {
			return strings.ReplaceAll(value, "$", "$$")
		}
		for _, variable := range template.ExtractVariables(map[string]any{"value": value}, template.DefaultPattern) {
			if variable.DefaultValue == "" {
				return nil
			}
		}
	}
	return value
}

// readCompose reads the compose files as documents, and takes
// x-nemo-bundle out of them.
func readCompose(files []string) ([]cnab.ComposeDocument, Metadata, error) {
	var metadata Metadata
	var found bool
	documents := make([]cnab.ComposeDocument, len(files))
	for i, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, metadata, err
		}
		var config map[string]any
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, metadata, fmt.Errorf("%s: %w", file, err)
		}
		if raw, ok := config[Extension]; ok {
			if found {
				return nil, metadata, fmt.Errorf("%s: %s is given twice", file, Extension)
			}
			if block, ok := raw.(map[string]any); ok && block["claims"] != nil {
				return nil, metadata, fmt.Errorf("%s: %s: claims: give each volume its type with x-nemo-volume instead", file, Extension)
			}
			if err := decode.Strict(raw, &metadata); err != nil {
				return nil, metadata, fmt.Errorf("%s: %s: %w", file, Extension, err)
			}
			delete(config, Extension)
			found = true
		}
		// Compose ignores it and warns that it is obsolete.
		delete(config, "version")
		documents[i] = cnab.ComposeDocument{Name: filepath.Base(file), Config: config}
	}
	if !found || metadata.Name == "" {
		return nil, metadata, fmt.Errorf("no %s with a name in %s", Extension, strings.Join(files, ", "))
	}
	return documents, metadata, nil
}

// declareVariables declares a string parameter for each ${VARIABLE} of the
// documents and of the outputs' values, and for each config read from a
// variable; and a credential for each secret read from a variable. The
// installer interpolates the documents and the outputs with the parameters,
// and reads configs and secrets from both.
func declareVariables(built *bundle.Bundle, documents []cnab.ComposeDocument, project *types.Project, outputs map[string]Output) error {
	declare := func(variable string, value template.Variable) error {
		name := parameterName(variable)
		if strings.HasPrefix(name, "nemo-") {
			return fmt.Errorf("variable %s: the nemo-* parameters are nemo's", variable)
		}
		if _, declared := built.Parameters[name]; declared {
			return nil
		}
		schema := &definition.Schema{Type: "string"}
		if value.DefaultValue != "" {
			schema.Default = value.DefaultValue
		}
		built.Definitions[name] = schema
		built.Parameters[name] = bundle.Parameter{Definition: name, Required: value.Required, Destination: &bundle.Location{EnvironmentVariable: variable}}
		return nil
	}

	values := map[string]any{}
	for name, output := range outputs {
		values[name] = output.Value
	}
	for _, config := range append(configsOf(documents), values) {
		variables := template.ExtractVariables(config, template.DefaultPattern)
		for _, variable := range slices.Sorted(maps.Keys(variables)) {
			if err := declare(variable, variables[variable]); err != nil {
				return err
			}
		}
	}

	var errs []error
	for _, key := range slices.Sorted(maps.Keys(project.Configs)) {
		config := project.Configs[key]
		switch {
		case config.File != "":
			errs = append(errs, fmt.Errorf("configs.%s: a bundle carries no files: use content or environment", key))
		case config.Environment != "":
			errs = append(errs, declare(config.Environment, template.Variable{}))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(project.Secrets)) {
		secret := project.Secrets[key]
		switch {
		case secret.File != "":
			errs = append(errs, fmt.Errorf("secrets.%s: a bundle carries no files: use environment, filled by a credential", key))
		case secret.Environment != "":
			built.Credentials[parameterName(secret.Environment)] = bundle.Credential{
				Location: bundle.Location{EnvironmentVariable: secret.Environment},
				Required: true,
				ApplyTo:  []string{cnab.ActionInstall, cnab.ActionUpgrade},
			}
		}
	}
	return errors.Join(errs...)
}

func configsOf(documents []cnab.ComposeDocument) []map[string]any {
	configs := make([]map[string]any, len(documents))
	for i, document := range documents {
		configs[i] = document.Config
	}
	return configs
}

// outputName is the name an output may have: the file it is written to.
var outputName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// declareOutputs declares the outputs, each written at
// /cnab/app/outputs/NAME once the bundle is installed or upgraded, with a
// write-only definition when sensitive, and carries their values in
// io.nemo.outputs.
func declareOutputs(built *bundle.Bundle, outputs map[string]Output) error {
	if len(outputs) == 0 {
		return nil
	}
	built.Outputs = map[string]bundle.Output{}
	values := cnab.OutputValues{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(outputs)) {
		output := outputs[name]
		switch {
		case !outputName.MatchString(name):
			errs = append(errs, fmt.Errorf("outputs.%s: an output is named with lowercase letters, digits and dashes", name))
			continue
		case output.Value == "":
			errs = append(errs, fmt.Errorf("outputs.%s: give it a value", name))
			continue
		}
		schema := &definition.Schema{Type: "string"}
		if output.Sensitive {
			schema.WriteOnly = &output.Sensitive
		}
		built.Definitions[outputDefinition(name)] = schema
		built.Outputs[name] = bundle.Output{
			Definition:  outputDefinition(name),
			Description: output.Description,
			ApplyTo:     []string{cnab.ActionInstall, cnab.ActionUpgrade},
			Path:        path.Join(cnab.OutputsDirectory, name),
		}
		values[name] = cnab.OutputValue{Value: output.Value}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return cnab.OutputsExtension.Write(built, values)
}

// outputDefinition is the definition of an output: a nemo-* name, which no
// parameter of the bundle has.
func outputDefinition(output string) string {
	return "nemo-output-" + output
}

// declareSources gives each parameter its source, an output of a bundle
// the bundle requires, in io.cnab.parameter-sources, which the bundle then
// requires.
func declareSources(built *bundle.Bundle, parameters map[string]Parameter, dependencies *cnab.Dependencies) error {
	if len(parameters) == 0 {
		return nil
	}
	sources := cnab.ParameterSources{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(parameters)) {
		source := parameters[name].Source
		_, declared := built.Parameters[name]
		_, required := dependencyOf(dependencies, source.Dependency)
		switch {
		case strings.HasPrefix(name, "nemo-"):
			errs = append(errs, fmt.Errorf("parameters.%s: the nemo-* parameters are nemo's", name))
		case !declared:
			errs = append(errs, fmt.Errorf("parameters.%s: no variable of the compose is this parameter", name))
		case source.Dependency == "" || source.Output == "":
			errs = append(errs, fmt.Errorf("parameters.%s.source: give the dependency and its output", name))
		case !required:
			errs = append(errs, fmt.Errorf("parameters.%s.source.dependency: %s is not in dependencies.requires", name, source.Dependency))
		default:
			raw, err := json.Marshal(map[string]string{"dependency": source.Dependency, "name": source.Output})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			sources[name] = cnab.ParameterSource{Priority: []string{cnab.SourceDependencyOutput}, Sources: map[string]json.RawMessage{cnab.SourceDependencyOutput: raw}}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	built.RequiredExtensions = append(built.RequiredExtensions, cnab.ParameterSourcesExtension.Key)
	return cnab.ParameterSourcesExtension.Write(built, sources)
}

func dependencyOf(dependencies *cnab.Dependencies, key string) (cnab.Dependency, bool) {
	if dependencies == nil {
		return cnab.Dependency{}, false
	}
	dependency, ok := dependencies.Requires[key]
	return dependency, ok
}

// parameterName is the parameter of a variable: GATEWAY_HOST is gateway-host.
func parameterName(variable string) string {
	return strings.ToLower(strings.ReplaceAll(variable, "_", "-"))
}

// resolveImages returns the images of the services, each with the digest
// of the location it is built at. Every image needs a location, and every
// location given an image.
func resolveImages(ctx context.Context, project *types.Project, locations map[string]string) (map[string]bundle.Image, error) {
	images := map[string]bundle.Image{}
	used := map[string]bool{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		image := project.Services[name].Image
		if image == "" || used[image] {
			continue
		}
		used[image] = true
		text, ok := locations[image]
		if !ok {
			errs = append(errs, fmt.Errorf("services.%s.image: %s has no location: give --image %s=docker://REFERENCE or oci:PATH", name, image, image))
			continue
		}
		location, err := ParseLocation(text)
		if err != nil {
			errs = append(errs, fmt.Errorf("image %s: %w", image, err))
			continue
		}
		descriptor, err := location.Resolve(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("image %s: %w", image, err))
			continue
		}
		key, _, _ := strings.Cut(path.Base(image), ":")
		if _, taken := images[key]; taken {
			errs = append(errs, fmt.Errorf("images %s: two images of the compose have this name", key))
			continue
		}
		images[key] = bundle.Image{BaseImage: bundle.BaseImage{
			ImageType: "docker",
			Image:     image,
			Digest:    descriptor.Digest.String(),
			Size:      uint64(descriptor.Size),
			MediaType: descriptor.MediaType,
		}}
	}
	for _, image := range slices.Sorted(maps.Keys(locations)) {
		if !used[image] {
			errs = append(errs, fmt.Errorf("--image %s: no service of the compose has this image", image))
		}
	}
	return images, errors.Join(errs...)
}

// resolveInstaller returns the invocation image: with the digest of its
// location, or by name only when it is not one.
func resolveInstaller(ctx context.Context, installer string) (bundle.BaseImage, error) {
	location, err := ParseLocation(installer)
	if err != nil {
		return bundle.BaseImage{ImageType: "docker", Image: installer}, nil
	}
	descriptor, err := location.Resolve(ctx)
	if err != nil {
		return bundle.BaseImage{}, fmt.Errorf("installer: %w", err)
	}
	return bundle.BaseImage{ImageType: "docker", Image: location.ImageName(), Digest: descriptor.Digest.String(), Size: uint64(descriptor.Size), MediaType: descriptor.MediaType}, nil
}

func nemoDefinitions() definition.Definitions {
	sensitive := true
	return definition.Definitions{
		cnab.ParameterRegistryAuth: {Type: "string", WriteOnly: &sensitive},
		cnab.ParameterOrchestrator: {Type: "string", Enum: []any{"compose", "swarm"}},
		cnab.ParameterTimeout:      {Type: "string", Default: "5m"},
		cnab.ParameterPurge:        {Type: "boolean", Default: false},
		cnab.ParameterVolumes:      {Type: "object"},
		cnab.ParameterOverride:     {Type: "string"},
	}
}

// nemoParameters are the parameters by which the runtime tells the installer about the target.
func nemoParameters() map[string]bundle.Parameter {
	return map[string]bundle.Parameter{
		cnab.ParameterOrchestrator: {Definition: cnab.ParameterOrchestrator, Required: true, Description: "The orchestrator of the target: compose or swarm", Destination: &bundle.Location{EnvironmentVariable: "NEMO_ORCHESTRATOR"}},
		cnab.ParameterTimeout:      {Definition: cnab.ParameterTimeout, Description: "How long the action may take, as a Go duration", Destination: &bundle.Location{EnvironmentVariable: "NEMO_TIMEOUT"}},
		cnab.ParameterPurge:        {Definition: cnab.ParameterPurge, ApplyTo: []string{cnab.ActionUninstall}, Description: "Also remove the volumes", Destination: &bundle.Location{EnvironmentVariable: "NEMO_PURGE"}},
		cnab.ParameterVolumes:      {Definition: cnab.ParameterVolumes, Description: "The volume types of the target, as JSON", Destination: &bundle.Location{EnvironmentVariable: "NEMO_VOLUMES"}},
		cnab.ParameterRegistryAuth: {Definition: cnab.ParameterRegistryAuth, ApplyTo: []string{cnab.ActionInstall, cnab.ActionUpgrade}, Description: "The credentials of the registries the images are pulled from, as JSON by domain", Destination: &bundle.Location{EnvironmentVariable: "NEMO_REGISTRY_AUTH"}},
		cnab.ParameterOverride:     {Definition: cnab.ParameterOverride, Description: "A compose document fused into the bundle's, last, as YAML: the override the stack gives the installation", Destination: &bundle.Location{EnvironmentVariable: "NEMO_OVERRIDE"}},
	}
}
