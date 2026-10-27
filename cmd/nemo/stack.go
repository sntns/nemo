package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/cnabio/cnab-go/valuesource"
	"github.com/spf13/cobra"

	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/stack"
	"github.com/sntns/nemo/pkg/storage/file"
)

// stackStore is the store of the stacks of nemo's home.
func stackStore() (*stack.Store, string, error) {
	root, err := file.DefaultRoot()
	if err != nil {
		return nil, "", err
	}
	return stack.NewStore(root), root, nil
}

// chosenStack is the stack of the file given, else the stored stack of
// that name, else the current one.
func chosenStack(path, name string) (stack.Stack, error) {
	if path != "" && name != "" {
		return stack.Stack{}, errors.New("give the stack file or the stack's name, not both")
	}
	if path != "" {
		return stack.Load(path)
	}
	name, err := stackName(name)
	if err != nil {
		return stack.Stack{}, err
	}
	store, _, err := stackStore()
	if err != nil {
		return stack.Stack{}, err
	}
	return store.Load(name)
}

// completeStacks completes the first argument with the names of the stacks
// nemo keeps.
func completeStacks(_ *cobra.Command, arguments []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	store, _, err := stackStore()
	if len(arguments) > 0 || err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names, _ := store.List()
	return names, cobra.ShellCompDirectiveNoFileComp
}

// optional is the argument given, or nothing.
func optional(arguments []string) string {
	if len(arguments) == 0 {
		return ""
	}
	return arguments[0]
}

// stackCommand groups the commands on the stacks nemo keeps.
func stackCommand() *cobra.Command {
	command := &cobra.Command{Use: "stack", Short: "Keep stacks: their manifests, in nemo's home, and the current one"}
	command.AddCommand(stackCreate(), stackEdit(), stackImport(), stackUse(), stackList(), stackInspect(), stackSet(), stackRemove())
	return command
}

func stackImport() *cobra.Command {
	var name, format, remote string
	var yes bool
	command := &cobra.Command{
		Use:   "import FILE",
		Short: "Keep a stack from its manifest, YAML or JSON, and make it the current one",
		Long: `Keep a stack from its manifest, YAML or JSON, and make it the current one. In a
terminal, the editor opens on its review first: what the manifest misses is
marked, and can be completed before the stack is kept. --yes keeps it as it is.`,
		Example: `  nemo stack import stack.yaml --name staging
  nemo stack import stack.yaml --remote "oci:$PWD/build/bundles/{{ .Component }}.oci" --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			imported, err := stack.Read(arguments[0])
			if err != nil {
				return err
			}
			if remote != "" {
				imported.Remote = remote
			}
			if name != "" {
				for i := range imported.Installations {
					imported.Installations[i].Stack = name
				}
				imported.Name = name
			}
			if format == "" {
				format = stack.YAML
				if filepath.Ext(arguments[0]) == ".json" {
					format = stack.JSON
				}
			}
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			if !yes && interactive("", "") == nil {
				var saved bool
				if imported, saved, err = editStack(command.Context(), store, imported, modeImport); err != nil || !saved {
					return cmp.Or(err, errors.New("stack not imported"))
				}
			}
			return keep(command, store, imported, format, "kept")
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "keep the manifest as it is, without the editor")
	command.Flags().StringVar(&name, "name", "", "name of the stack, over the manifest's")
	command.Flags().StringVar(&remote, "remote", "", "where the bundles given by version are, over the manifest's: where they were built or pushed")
	command.Flags().StringVar(&format, "format", "", "format to keep it in, yaml or json (default: the file's)")
	return command
}

func stackUse() *cobra.Command {
	return &cobra.Command{
		Use:               "use NAME",
		ValidArgsFunction: completeStacks,
		Short:             "Make a stack the current one, which up and down act on",
		Args:              cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			return store.Use(arguments[0])
		},
	}
}

func stackList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stacks, the current one marked",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			names, err := store.List()
			if err != nil {
				return err
			}
			current, err := store.Current()
			for _, name := range names {
				mark := " "
				if name == current {
					mark = "*"
				}
				fmt.Fprintln(command.OutOrStdout(), mark, name)
			}
			return err
		},
	}
}

func stackInspect() *cobra.Command {
	var format string
	command := &cobra.Command{
		Use:               "inspect [NAME]",
		ValidArgsFunction: completeStacks,
		Short:             "Print the manifest of a stack, the current one by default",
		Args:              cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			inspected, err := chosenStack("", optional(arguments))
			if err != nil {
				return err
			}
			if format == "" {
				store, _, err := stackStore()
				if err != nil {
					return err
				}
				path, err := store.Path(inspected.Name)
				if err != nil {
					return err
				}
				format = filepath.Ext(path)[1:]
			}
			data, err := stack.Encode(inspected, format)
			if err != nil {
				return err
			}
			_, err = command.OutOrStdout().Write(data)
			return err
		},
	}
	command.Flags().StringVar(&format, "format", "", "yaml or json (default: the manifest's)")
	return command
}

func stackSet() *cobra.Command {
	var (
		remote          string
		parameters      []string
		credentialFlags []string
	)
	command := &cobra.Command{
		Use:               "set [NAME]",
		ValidArgsFunction: completeStacks,
		Short:             "Change a stack, the current one by default",
		Example:           `  nemo stack set --remote "oci:$PWD/build/bundles/{{ .Component }}.oci" --param region=eu6 --credential token=env:TOKEN`,
		Args:              cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			changed, err := chosenStack("", optional(arguments))
			if err != nil {
				return err
			}
			if remote != "" {
				changed.Remote = remote
			}
			values, err := pairs("--param", parameters)
			if err != nil {
				return err
			}
			if len(values) > 0 && changed.Parameters == nil {
				changed.Parameters = map[string]string{}
			}
			for name, value := range values {
				changed.Parameters[name] = value
			}
			sources, err := pairs("--credential", credentialFlags)
			if err != nil {
				return err
			}
			workingDirectory, err := os.Getwd()
			if err != nil {
				return err
			}
			for name, text := range sources {
				strategy, err := credentials.Parse(name, text)
				if err != nil {
					return err
				}
				changed.Credentials = slices.DeleteFunc(changed.Credentials, func(given valuesource.Strategy) bool { return given.Name == name })
				changed.Credentials = append(changed.Credentials, credentials.Absolute(strategy, workingDirectory))
			}
			store, _, err := stackStore()
			if err != nil {
				return err
			}
			return store.Save(changed, "")
		},
	}
	command.Flags().StringVar(&remote, "remote", "", "where the bundles given by version are: docker://…/{{ .Component }}:{{ .Version }} or oci:…/{{ .Component }}.oci")
	command.Flags().StringArrayVar(&parameters, "param", nil, "parameter of the stack, NAME=VALUE (repeatable)")
	command.Flags().StringArrayVar(&credentialFlags, "credential", nil, "credential of the stack, NAME=KEY:VALUE with KEY a CNAB source, path, env, command or value (repeatable)")
	return command
}

func stackRemove() *cobra.Command {
	return &cobra.Command{
		Use:               "remove NAME",
		ValidArgsFunction: completeStacks,
		Short:             "Forget a stack, its manifest and its installations' records, once nothing of it is installed",
		Args:              cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			name := arguments[0]
			store, root, err := stackStore()
			if err != nil {
				return err
			}
			installations, err := file.New(root).ListInstallations(command.Context(), name)
			if err != nil {
				return err
			}
			for _, installation := range installations {
				if !installation.IsUninstalled() {
					return fmt.Errorf("stack %s has installations: nemo down --stack %s first", name, name)
				}
			}
			return store.Remove(name)
		},
	}
}
