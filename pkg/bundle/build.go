// Package bundle builds nemo bundles from a compose project, and moves
// them between registries and OCI layout directories.
package bundle

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
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
// the images with their digest, the nemo-* parameters, and the compose
// project itself in io.nemo.compose.
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
		SchemaVersion:      bundle.GetDefaultSchemaVersion(),
		Name:               metadata.Name,
		Version:            version,
		Description:        metadata.Description,
		License:            metadata.License,
		Keywords:           metadata.Keywords,
		Maintainers:        metadata.Maintainers,
		Definitions:        nemoDefinitions(),
		Parameters:         nemoParameters(),
		Credentials:        map[string]bundle.Credential{},
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

	project, err := compose.Load(ctx, metadata.Name, filepath.Dir(request.ComposeFiles[0]), nil)
	if err != nil {
		return cnab.ExtendedBundle{}, fmt.Errorf("loading the compose project: %w", err)
	}
	variablesErr := declareVariables(&built, documents, project)
	images, imagesErr := resolveImages(ctx, project, request.Images)
	if err := errors.Join(variablesErr, imagesErr); err != nil {
		return cnab.ExtendedBundle{}, err
	}
	built.Images = images
	return cnab.NewBundle(built)
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
// documents, and for each config read from a variable; and a credential
// for each secret read from a variable. The installer interpolates the
// documents with the parameters, and reads configs and secrets from both.
func declareVariables(built *bundle.Bundle, documents []cnab.ComposeDocument, project *types.Project) error {
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

	for _, document := range documents {
		variables := template.ExtractVariables(document.Config, template.DefaultPattern)
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
	}
}
