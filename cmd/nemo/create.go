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

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/stack"
)

// projectName is what Compose and Swarm accept as a stack's name.
var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func stackCreate() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use:   "create [NAME]",
		Short: "Create a stack in the editor: its bundles, its target, and the parameters and credentials they declare",
		Long: `Create a stack in the editor, and make it the current one. The bundles come
from a directory of OCI layouts or from a registry; the editor then asks for
the target, the volume types they use, and the parameters and the credentials
they declare, one at a time, and reviews the stack before saving it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if err := interactive("nemo stack create", "write the manifest and nemo stack import it"); err != nil {
				return err
			}
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			created, saved, err := editStack(command.Context(), store, stack.Stack{Name: optional(arguments)}, modeCreate)
			if err != nil || !saved {
				return cmp.Or(err, errors.New("stack not created"))
			}
			return keep(command, store, created, format, "written")
		},
	}
	command.Flags().StringVar(&format, "format", stack.YAML, "format to keep it in, yaml or json")
	return command
}

func stackEdit() *cobra.Command {
	return &cobra.Command{
		Use:               "edit [NAME]",
		ValidArgsFunction: completeStacks,
		Short:             "Change a stack in the editor, the current one by default",
		Long: `Open a stack in the editor, on its review: what is missing is marked, and
each line opens the step that changes it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if err := interactive("nemo stack edit", "use nemo stack set"); err != nil {
				return err
			}
			edited, err := chosenStack("", optional(arguments))
			if err != nil {
				return err
			}
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			changed, saved, err := editStack(command.Context(), store, edited, modeEdit)
			if err != nil || !saved {
				return cmp.Or(err, errors.New("stack not changed"))
			}
			if err := store.Save(changed, ""); err != nil {
				return err
			}
			path, err := store.Path(changed.Name)
			fmt.Fprintf(command.ErrOrStderr(), "Stack %s written to %s\n", changed.Name, path)
			return err
		},
	}
}

// interactive refuses to open the editor without a terminal, and says what to do instead.
func interactive(name, instead string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("%s opens an editor in a terminal: in a script, %s", name, instead)
	}
	return nil
}

// keep saves the stack in nemo's home, and makes it the current one.
func keep(command *cobra.Command, store *stack.Store, kept stack.Stack, format, done string) error {
	if err := store.Save(kept, format); err != nil {
		return err
	}
	if err := store.Use(kept.Name); err != nil {
		return err
	}
	path, err := store.Path(kept.Name)
	fmt.Fprintf(command.ErrOrStderr(), "Stack %s %s to %s, now the current one: nemo up\n", kept.Name, done, path)
	return err
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

// registryNamespace checks a registry and namespace: REGISTRY/NAMESPACE, what the editor writes the remote from.
func registryNamespace(value string) error {
	value = strings.TrimSuffix(value, "/")
	switch {
	case value == "":
		return errors.New("required")
	case strings.Contains(value, "://") || strings.Contains(value, "{{"):
		return errors.New("REGISTRY/NAMESPACE alone, such as registry.example.com/shop: nemo writes the remote from it")
	}
	if _, err := cnab.ParseOCIReference(value + "/bundle"); err != nil {
		return errors.New("REGISTRY/NAMESPACE, in lowercase, such as registry.example.com/shop")
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
