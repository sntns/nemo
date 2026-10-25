package main

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sntns/nemo/pkg/nemo"
)

// installationRead is a command that reads what nemo keeps of the
// installations of a stack, --stack else the current one, and prints it
// as text, or as JSON with --json.
func installationRead(use, short string, arguments cobra.PositionalArgs, read func(api *nemo.Nemo, command *cobra.Command, stack string, arguments []string) (any, func(io.Writer), error)) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:               use,
		Short:             short,
		Args:              arguments,
		ValidArgsFunction: completeInstallations,
		RunE: func(command *cobra.Command, arguments []string) error {
			stack, err := stackName(name)
			if err != nil {
				return err
			}
			api, err := nemo.New(nemo.Options{})
			if err != nil {
				return err
			}
			record, text, err := read(api, command, stack, arguments)
			if err != nil {
				return err
			}
			if asJSON, _ := command.Flags().GetBool("json"); asJSON {
				return render(command, record, nil)
			}
			table := tabwriter.NewWriter(command.OutOrStdout(), 0, 0, 3, ' ', 0)
			text(table)
			return table.Flush()
		},
	}
	command.Flags().StringVar(&name, "stack", "", "name of the stack (default: the current one)")
	command.Flags().Bool("json", false, "print it as JSON on stdout")
	return command
}

func installationList() *cobra.Command {
	return installationRead("list", "List the installations of a stack", cobra.NoArgs,
		func(api *nemo.Nemo, command *cobra.Command, stack string, _ []string) (any, func(io.Writer), error) {
			installations, err := api.Installations(command.Context(), stack)
			return installations, func(output io.Writer) {
				fmt.Fprintln(output, "NAME\tVERSION\tSTATE\tMODIFIED")
				now := time.Now()
				for _, installation := range installations {
					fmt.Fprintf(output, "%s\t%s\t%s\t%s\n", installation.Name, installation.Status.BundleVersion,
						lastAction(installation.Status.Action, installation.Status.ResultStatus), ago(installation.Status.Modified, now))
				}
			}, err
		})
}

func installationShow() *cobra.Command {
	return installationRead("show INSTALLATION", "Show an installation: its bundle, target, parameters and credential sources", cobra.ExactArgs(1),
		func(api *nemo.Nemo, command *cobra.Command, stack string, arguments []string) (any, func(io.Writer), error) {
			installation, err := api.Installation(command.Context(), stack, arguments[0])
			return installation, func(output io.Writer) {
				status := installation.Status
				fmt.Fprintf(output, "Name:\t%s\n", installation.Name)
				fmt.Fprintf(output, "Stack:\t%s\n", installation.Stack)
				fmt.Fprintf(output, "Bundle:\t%s %s\n", cmp.Or(status.BundleReference, installation.Bundle.File, installation.Bundle.Repository), status.BundleVersion)
				fmt.Fprintf(output, "Target:\t%s %s\n", installation.Target.Orchestrator, cmp.Or(installation.Target.Host, installation.Target.Context))
				fmt.Fprintf(output, "State:\t%s %s\n", lastAction(status.Action, status.ResultStatus), ago(status.Modified, time.Now()))
				if len(installation.Parameters) > 0 {
					fmt.Fprintln(output, "Parameters:")
					for _, name := range slices.Sorted(maps.Keys(installation.Parameters)) {
						fmt.Fprintf(output, "  %s\t%s\n", name, installation.Parameters[name])
					}
				}
				if len(installation.Credentials) > 0 {
					fmt.Fprintln(output, "Credentials:")
					for _, credential := range installation.Credentials {
						source := credential.Source.Value
						// A value source holds the secret itself.
						if credential.Source.Key == "value" {
							source = nemo.Redacted
						}
						fmt.Fprintf(output, "  %s\t%s:%s\n", credential.Name, credential.Source.Key, source)
					}
				}
			}, err
		})
}

func installationRuns() *cobra.Command {
	return installationRead("runs INSTALLATION", "List the actions run on an installation, oldest first", cobra.ExactArgs(1),
		func(api *nemo.Nemo, command *cobra.Command, stack string, arguments []string) (any, func(io.Writer), error) {
			runs, err := api.Runs(command.Context(), stack, arguments[0])
			return runs, func(output io.Writer) {
				fmt.Fprintln(output, "STARTED\tACTION\tVERSION\tSTATUS\tDURATION")
				for _, run := range runs {
					duration := ""
					if !run.Ended.IsZero() {
						duration = run.Ended.Sub(run.Started).Round(100 * time.Millisecond).String()
					}
					fmt.Fprintf(output, "%s\t%s\t%s\t%s\t%s\n", run.Started.Local().Format(time.DateTime), run.Action, run.Bundle.Version, cmp.Or(run.Status, "unknown"), duration)
				}
			}, err
		})
}

func installationOutputs() *cobra.Command {
	return installationRead("outputs INSTALLATION [OUTPUT]", "List the outputs of an installation, the last value of each, or print one", cobra.RangeArgs(1, 2),
		func(api *nemo.Nemo, command *cobra.Command, stack string, arguments []string) (any, func(io.Writer), error) {
			runs, err := api.Runs(command.Context(), stack, arguments[0])
			outputs := map[string]string{}
			for _, run := range runs {
				maps.Copy(outputs, run.Outputs)
			}
			if len(arguments) == 2 {
				value, ok := outputs[arguments[1]]
				if !ok && err == nil {
					err = fmt.Errorf("installation %s has no output %s", arguments[0], arguments[1])
				}
				return value, func(io.Writer) { fmt.Fprintln(command.OutOrStdout(), value) }, err
			}
			return outputs, func(output io.Writer) {
				fmt.Fprintln(output, "NAME\tVALUE")
				for _, name := range slices.Sorted(maps.Keys(outputs)) {
					fmt.Fprintf(output, "%s\t%s\n", name, outputs[name])
				}
			}, err
		})
}
