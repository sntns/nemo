package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"charm.land/huh/v2/spinner"
	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// projectName is what Compose and Swarm accept as a stack's name.
var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func stackCreate() *cobra.Command {
	return &cobra.Command{
		Use:   "create [NAME]",
		Short: "Create a stack in a form: its bundles, its target, and the parameters and credentials they declare",
		Long: `Create a stack in a form, and make it the current one. The bundles come from a
directory of OCI layouts or from a registry; the form then asks for the
parameters and the credentials they declare, and the volume types they use
get a local volume, to change in the manifest for another driver.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			// The accessible form reads its answers line by line, from a terminal or not.
			if !term.IsTerminal(int(os.Stdin.Fd())) && os.Getenv("ACCESSIBLE") == "" {
				return errors.New("nemo stack create asks in a terminal: in a script, write the manifest and nemo stack import it")
			}
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			created, format, err := askStack(command.Context(), store, optional(arguments))
			if err != nil {
				return err
			}
			if err = store.Save(created, format); err != nil {
				return err
			}
			if err = store.Use(created.Name); err != nil {
				return err
			}
			path, err := store.Path(created.Name)
			fmt.Fprintf(command.ErrOrStderr(), "Stack %s written to %s, now the current one: nemo up\n", created.Name, path)
			return err
		},
	}
}

// askStack asks for the stack, and the format of its manifest.
func askStack(ctx context.Context, store *stack.Store, name string) (stack.Stack, string, error) {
	created := stack.Stack{Name: name}
	from := bundle.Layout
	fields := []huh.Field{}
	if name == "" {
		fields = append(fields, huh.NewInput().Title("Name of the stack").Placeholder("staging").Value(&created.Name).Validate(func(value string) error {
			if !projectName.MatchString(value) {
				return errors.New("lowercase letters, digits, dashes and underscores")
			}
			if _, err := store.Path(value); err == nil {
				return fmt.Errorf("%s exists: nemo stack set changes it", value)
			}
			return nil
		}))
	}
	fields = append(fields, huh.NewSelect[string]().
		Title("Where are the bundles?").
		Options(
			huh.NewOption("A directory of OCI layouts   built here: nemo bundle build -o oci:DIRECTORY/NAME.oci", bundle.Layout),
			huh.NewOption("A registry                   pushed there; docker login for a private one", bundle.Registry),
		).
		Value(&from))
	if err := run(huh.NewGroup(fields...)); err != nil {
		return created, "", err
	}

	var definitions []cnab.ExtendedBundle
	var err error
	if from == bundle.Layout {
		definitions, err = askLayouts(ctx, &created)
	} else {
		definitions, err = askRegistry(ctx, &created)
	}
	if err != nil {
		return created, "", err
	}
	for _, definition := range definitions {
		created.Installations = append(created.Installations, storage.InstallationSpec{Name: definition.Name, Stack: created.Name, Bundle: storage.BundleReference{Version: definition.Version}})
	}

	created.Target = target.Target{Orchestrator: target.Swarm}
	created.Target.Images = map[string]string{bundle.Layout: target.ImagesLoad, bundle.Registry: target.ImagesRegistry}[from]
	if err := run(huh.NewGroup(
		huh.NewSelect[string]().Title("Orchestrator").Options(huh.NewOptions(target.Swarm, target.Compose)...).Value(&created.Target.Orchestrator),
		huh.NewSelect[string]().Title("How do the images reach the target?").Options(
			huh.NewOption("load       nemo loads them into its Docker: one machine, no registry", target.ImagesLoad),
			huh.NewOption("registry   each machine pulls them from their registry", target.ImagesRegistry),
		).Value(&created.Target.Images),
	)); err != nil {
		return created, "", err
	}
	for _, volumeType := range volumeTypes(definitions) {
		if created.Target.Volumes == nil {
			created.Target.Volumes = map[string]target.VolumeType{}
		}
		created.Target.Volumes[volumeType] = target.VolumeType{}
	}

	if err := askValues(&created, definitions); err != nil {
		return created, "", err
	}

	format := stack.YAML
	confirmed := true
	summary := []string{"Bundles        " + created.Remote}
	for _, installation := range created.Installations {
		summary = append(summary, "               "+installation.Name+" "+installation.Bundle.Version)
	}
	summary = append(summary, "Target         "+created.Target.Orchestrator+", images "+created.Target.Images)
	if err := run(huh.NewGroup(
		huh.NewSelect[string]().Title("Format of the manifest").Options(huh.NewOptions(stack.YAML, stack.JSON)...).Value(&format),
		huh.NewConfirm().Title("Create "+created.Name+"?").Description(strings.Join(summary, "\n")).Value(&confirmed),
	)); err != nil {
		return created, "", err
	}
	if !confirmed {
		return created, "", errors.New("stack not created")
	}
	return created, format, nil
}

// askLayouts asks for the directory of the layouts and the bundles to install among them.
func askLayouts(ctx context.Context, created *stack.Stack) ([]cnab.ExtendedBundle, error) {
	directory, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := run(huh.NewGroup(huh.NewInput().Title("Directory of the OCI layouts").Value(&directory).Validate(notEmpty))); err != nil {
		return nil, err
	}
	if directory, err = filepath.Abs(directory); err != nil {
		return nil, err
	}
	created.Remote = "oci:" + filepath.Join(directory, "{{ .Component }}.oci")

	var definitions map[string]cnab.ExtendedBundle
	if err := spin("Reading the bundles of "+directory, func() (err error) {
		definitions, err = layoutBundles(ctx, directory)
		return
	}); err != nil {
		return nil, err
	}
	options := []huh.Option[string]{}
	for _, name := range slices.Sorted(maps.Keys(definitions)) {
		options = append(options, huh.NewOption(name+" "+definitions[name].Version, name).Selected(true))
	}
	var chosen []string
	if err := run(huh.NewGroup(huh.NewMultiSelect[string]().Title("Bundles to install").Options(options...).Value(&chosen).Validate(atLeastOne))); err != nil {
		return nil, err
	}
	var result []cnab.ExtendedBundle
	for _, name := range chosen {
		result = append(result, definitions[name])
	}
	return result, nil
}

// layoutBundles are the bundles of the directory, by name: each NAME.oci
// layout that holds the bundle NAME, as the remote finds it.
func layoutBundles(ctx context.Context, directory string) (map[string]cnab.ExtendedBundle, error) {
	layouts, err := filepath.Glob(filepath.Join(directory, "*.oci"))
	if err != nil {
		return nil, err
	}
	definitions := map[string]cnab.ExtendedBundle{}
	for _, layout := range layouts {
		// A layout of an image, the installer's for one, is no bundle.
		pulled, err := bundle.Pull(ctx, bundle.Location{Transport: bundle.Layout, Path: layout})
		if name := strings.TrimSuffix(filepath.Base(layout), ".oci"); err == nil && pulled.Definition.Name == name {
			definitions[name] = pulled.Definition
		}
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("%s holds no NAME.oci layout of a bundle NAME", directory)
	}
	return definitions, nil
}

// askRegistry asks for the repositories of the bundles and their version.
func askRegistry(ctx context.Context, created *stack.Stack) ([]cnab.ExtendedBundle, error) {
	var prefix, names string
	if err := run(huh.NewGroup(
		huh.NewInput().Title("Registry and namespace of the bundles").Placeholder("registry.example.com/shop").Value(&prefix).Validate(notEmpty),
		huh.NewInput().Title("Bundles").Description("their names, separated by spaces").Value(&names).Validate(notEmpty),
	)); err != nil {
		return nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	created.Remote = "docker://" + prefix + "/{{ .Component }}:{{ .Version }}"
	chosen := strings.Fields(names)

	var versions []string
	if err := spin("Reading the versions of "+chosen[0], func() error {
		repository, err := cnab.ParseOCIReference(prefix + "/" + chosen[0])
		if err != nil {
			return err
		}
		tags, err := bundle.RegistryTags{}.ListTags(ctx, repository)
		for _, tag := range tags {
			if _, err := semver.NewVersion(tag); err == nil {
				versions = append(versions, tag)
			}
		}
		slices.SortFunc(versions, func(a, b string) int { return semver.MustParse(b).Compare(semver.MustParse(a)) })
		return err
	}); err != nil {
		return nil, err
	}
	version := ""
	field := huh.Field(huh.NewInput().Title("Version").Value(&version).Validate(notEmpty))
	if len(versions) > 0 {
		version = versions[0]
		field = huh.NewSelect[string]().Title("Version").Options(huh.NewOptions(versions...)...).Value(&version)
	}
	if err := run(huh.NewGroup(field)); err != nil {
		return nil, err
	}

	var result []cnab.ExtendedBundle
	err := spin("Reading the bundles", func() error {
		for _, name := range chosen {
			pulled, err := bundle.Pull(ctx, bundle.Location{Transport: bundle.Registry, Reference: prefix + "/" + name + ":" + version})
			if err != nil {
				return err
			}
			result = append(result, pulled.Definition)
		}
		return nil
	})
	return result, err
}

// askValues asks for the parameters and the credentials the bundles
// declare, nemo's own aside: the stack gives each to every bundle that
// declares it.
func askValues(created *stack.Stack, definitions []cnab.ExtendedBundle) error {
	parameters := map[string]*string{}
	required := map[string]bool{}
	descriptions := map[string]string{}
	sources := map[string]*string{}
	for _, definition := range definitions {
		for name, parameter := range definition.Parameters {
			if strings.HasPrefix(name, "nemo-") {
				continue
			}
			if parameters[name] == nil {
				parameters[name] = new(string)
			}
			if schema, ok := definition.Definitions[parameter.Definition]; ok && schema.Default != nil {
				*parameters[name] = fmt.Sprint(schema.Default)
			}
			required[name] = required[name] || parameter.Required
			descriptions[name] = cmp.Or(descriptions[name], parameter.Description)
		}
		for name := range definition.Credentials {
			sources[name] = new(string)
		}
	}

	fields := []huh.Field{}
	for _, name := range slices.Sorted(maps.Keys(parameters)) {
		fields = append(fields, huh.NewInput().Title(name).Description(descriptions[name]).Value(parameters[name]).Validate(func(value string) error {
			if required[name] && value == "" {
				return errors.New("required")
			}
			return nil
		}))
	}
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		fields = append(fields, huh.NewInput().Title("Credential "+name).Description("where to read it: path:FILE, env:VARIABLE, command:COMMAND or value:VALUE").Value(sources[name]).Validate(func(value string) error {
			_, err := credentials.Parse(name, value)
			return err
		}))
	}
	if len(fields) > 0 {
		if err := run(huh.NewGroup(fields...).Title("What the bundles declare")); err != nil {
			return err
		}
	}

	for name, value := range parameters {
		if *value != "" {
			if created.Parameters == nil {
				created.Parameters = map[string]string{}
			}
			created.Parameters[name] = *value
		}
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		strategy, err := credentials.Parse(name, *sources[name])
		if err != nil {
			return err
		}
		created.Credentials = append(created.Credentials, credentials.Absolute(strategy, workingDirectory))
	}
	return nil
}

// volumeTypes are the x-nemo-volume types the bundles' composes use.
func volumeTypes(definitions []cnab.ExtendedBundle) []string {
	types := map[string]bool{}
	for _, definition := range definitions {
		if definition.Compose == nil {
			continue
		}
		for _, document := range definition.Compose.Documents {
			volumes, _ := document.Config["volumes"].(map[string]any)
			for _, volume := range volumes {
				config, _ := volume.(map[string]any)
				claim, _ := config[installer.VolumeExtension].(map[string]any)
				if volumeType, ok := claim["type"].(string); ok {
					types[volumeType] = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(types))
}

// run shows a form; ACCESSIBLE=1 asks its questions line by line, for screen readers.
func run(groups ...*huh.Group) error {
	err := huh.NewForm(groups...).WithAccessible(os.Getenv("ACCESSIBLE") != "").Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return errors.New("stack not created")
	}
	return err
}

// spin runs the action under a spinner.
func spin(title string, action func() error) error {
	var err error
	if spinErr := spinner.New().WithAccessible(os.Getenv("ACCESSIBLE") != "").Title(title).Action(func() { err = action() }).Run(); spinErr != nil {
		return spinErr
	}
	return err
}

func notEmpty(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("required")
	}
	return nil
}

func atLeastOne(values []string) error {
	if len(values) == 0 {
		return errors.New("choose at least one")
	}
	return nil
}
