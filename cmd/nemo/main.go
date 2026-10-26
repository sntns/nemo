// Command nemo deploys CNAB bundles to Docker, with Compose or Swarm.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/sntns/nemo/pkg/bundle"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/nemo"
	"github.com/sntns/nemo/pkg/target"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root().ExecuteContext(ctx); err != nil {
		if shown := (shownError{}); !errors.As(err, &shown) {
			_, _ = lipgloss.Fprintln(os.Stderr, red.Render("error:"), err)
		}
		os.Exit(1)
	}
}

func root() *cobra.Command {
	command := &cobra.Command{
		Use:           "nemo",
		Short:         "Deploy CNAB bundles to Docker, with Compose or Swarm",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.AddGroup(
		&cobra.Group{ID: "stacks", Title: "Stacks:"},
		&cobra.Group{ID: "installations", Title: "Installations:"},
		&cobra.Group{ID: "bundles", Title: "Bundles:"},
	)
	cobra.EnableCommandSorting = false
	for _, group := range []struct {
		id       string
		commands []*cobra.Command
	}{
		{"stacks", []*cobra.Command{up(), down(), status(), logs(), stackCommand()}},
		{"installations", []*cobra.Command{installationCommand()}},
		{"bundles", []*cobra.Command{bundleCommand()}},
	} {
		for _, child := range group.commands {
			child.GroupID = group.id
			command.AddCommand(child)
		}
	}
	command.AddCommand(reference())
	completeStackFlags(command)
	return command
}

// stamped is the version a release stamps: -X main.stamped=1.2.0.
var stamped string

// version is nemo's version: the release's, else the module's as go install
// stamps it.
func version() string {
	if stamped != "" {
		return stamped
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

var release = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// defaultInstaller is NEMO_INSTALLER, else the gill of a released nemo's
// version: a development build has none of its own.
func defaultInstaller() string {
	if installer := os.Getenv("NEMO_INSTALLER"); installer != "" {
		return installer
	}
	if release.MatchString(version()) {
		return "docker://ghcr.io/sntns/gill:" + version()
	}
	return ""
}

// installationCommand groups the commands on one installation: what nemo
// remembers of it, and the CNAB actions on it, without a stack's manifest.
func installationCommand() *cobra.Command {
	command := &cobra.Command{Use: "installation", Short: "What nemo remembers of an installation, and the CNAB actions on one, without a manifest"}
	command.AddCommand(
		installationList(), installationShow(), installationRuns(), installationOutputs(),
		action("install", "Install a bundle", `  nemo installation install -f docker://registry.example.com/hello/nginx:1.0.0 --stack demo \
    --orchestrator compose --param region=eu5 --credential token=env:HELLO_TOKEN`, (*nemo.Nemo).Install),
		action("upgrade", "Upgrade an installation, to a new bundle or its own", `  nemo installation upgrade nginx --param region=eu6
  nemo installation upgrade -f nginx-1.1.0/bundle.json`, (*nemo.Nemo).Upgrade),
		action("uninstall", "Uninstall an installation", `  nemo installation uninstall nginx`, (*nemo.Nemo).Uninstall),
	)
	return command
}

// bundleCommand groups the commands on bundles.
func bundleCommand() *cobra.Command {
	command := &cobra.Command{Use: "bundle", Short: "Build and copy bundles"}
	command.AddCommand(build(), copyBundle())
	return command
}

// copyBundle copies a bundle and its images, as porter copy does.
func copyBundle() *cobra.Command {
	return &cobra.Command{
		Use:   "copy SOURCE DESTINATION",
		Short: "Copy a bundle and its images, from an OCI layout to a registry for one",
		Example: `  nemo bundle copy oci:build/api.oci docker://registry.example.com/shop/api:1.4.0
  nemo bundle copy docker://registry.example.com/shop/api:1.4.0 oci:/media/usb/api.oci`,
		Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			source, err := bundle.ParseLocation(arguments[0])
			if err != nil {
				return err
			}
			destination, err := bundle.ParseLocation(arguments[1])
			if err != nil {
				return err
			}
			_, err = bundle.Copy(command.Context(), source, destination)
			return err
		},
	}
}

// build builds a bundle.json from compose files with x-nemo-bundle.
func build() *cobra.Command {
	var (
		request bundle.BuildRequest
		images  []string
		output  string
	)
	command := &cobra.Command{
		Use:   "build",
		Short: "Build a bundle from compose files with x-nemo-bundle",
		Example: `  nemo bundle build -f compose.yaml --image nginx:1.27-alpine=docker://nginx:1.27-alpine
  nemo bundle build -f compose.yaml --version 1.4.0 \
    --image shop-api:latest=oci:build/images/shop-api.oci \
    -o docker://registry.example.com/shop/api`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			var err error
			if request.Images, err = pairs("--image", images); err != nil {
				return err
			}
			if request.Installer == "" {
				return errors.New("give the invocation image with --installer, or set NEMO_INSTALLER: a development build of nemo has no gill of its own")
			}
			built, err := bundle.Build(command.Context(), request)
			if err != nil {
				return err
			}
			destination, err := bundle.ParseLocation(output)
			if err != nil {
				return built.WriteFile(output, 0o644)
			}
			sources, err := request.Sources()
			if err != nil {
				return err
			}
			_, err = bundle.Push(command.Context(), built, sources, destination)
			return err
		},
	}
	flags := command.Flags()
	flags.StringArrayVarP(&request.ComposeFiles, "file", "f", nil, "compose file, merged in order; one has x-nemo-bundle (repeatable)")
	flags.StringVar(&request.Version, "version", "", "version of the bundle, over x-nemo-bundle's")
	flags.StringArrayVar(&images, "image", nil, "where an image of the compose is built, NAME=docker://REFERENCE or NAME=oci:PATH (repeatable)")
	flags.StringVar(&request.Installer, "installer", defaultInstaller(), "the invocation image, gill: docker://REFERENCE or oci:PATH to carry it with the bundle, or an image name (NEMO_INSTALLER, else the gill of this nemo's version)")
	flags.StringVarP(&output, "output", "o", "bundle.json", "a bundle.json to write, or where to push the bundle and its images: docker://REFERENCE or oci:PATH")
	_ = command.MarkFlagRequired("file")
	return command
}

// up brings a stack to its manifest.
func up() *cobra.Command {
	var (
		file  string
		name  string
		purge bool
	)
	command := &cobra.Command{
		Use:   "up [INSTALLATION...]",
		Short: "Bring a stack to its manifest: install, upgrade, and uninstall what it no longer has",
		Example: `  nemo up                  # the current stack
  nemo up api              # that installation alone, as the manifest has it
  nemo up --stack staging
  nemo up -f manifest.yaml`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeManifestInstallations,
		RunE: func(command *cobra.Command, arguments []string) error {
			loaded, err := chosenStack(file, name)
			if err != nil {
				return err
			}
			if err = ensureSwarm(command.Context(), loaded.Target, command.ErrOrStderr()); err != nil {
				return err
			}
			api, err := nemo.New(nemo.Options{})
			if err != nil {
				return err
			}
			events, stop := newRenderer(os.Stderr, loaded.Name+" · "+loaded.Target.Orchestrator)
			result, err := api.Up(command.Context(), nemo.UpRequest{Stack: loaded, Only: arguments, Purge: purge, Progress: events})
			return render(command, result, stop(err))
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "a stack's manifest, instead of a stack nemo keeps")
	command.Flags().StringVar(&name, "stack", "", "a stack nemo keeps (default: the current one)")
	command.Flags().BoolVar(&purge, "purge", false, "also remove the volumes of the bundles the stack no longer has")
	command.Flags().Bool("json", false, "print the record of the runs as JSON on stdout")
	return command
}

// down removes a stack: it needs only its name, given, read from its
// manifest, or the current one's.
func down() *cobra.Command {
	var (
		file  string
		name  string
		purge bool
	)
	command := &cobra.Command{
		Use:     "down",
		Short:   "Uninstall every installation of a stack",
		Example: "  nemo down                     # the current stack\n  nemo down --stack demo --purge\n  nemo down -f manifest.yaml",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			if file != "" && name != "" {
				return errors.New("give the stack file or the stack's name, not both")
			}
			if file != "" || name == "" {
				loaded, err := chosenStack(file, "")
				if err != nil {
					return err
				}
				name = loaded.Name
			}
			api, err := nemo.New(nemo.Options{})
			if err != nil {
				return err
			}
			events, stop := newRenderer(os.Stderr, name)
			result, err := api.Down(command.Context(), nemo.DownRequest{Name: name, Purge: purge, Progress: events})
			return render(command, result, stop(err))
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "a stack's manifest")
	command.Flags().StringVar(&name, "stack", "", "name of the stack (default: the current one)")
	command.Flags().BoolVar(&purge, "purge", false, "also remove the volumes")
	command.Flags().Bool("json", false, "print the record of the runs as JSON on stdout")
	return command
}

// render prints the record of the runs as JSON on stdout with --json, even
// when one failed: the progress on stderr is for people, this for scripts.
func render(command *cobra.Command, record any, err error) error {
	if asJSON, _ := command.Flags().GetBool("json"); asJSON {
		encoder := json.NewEncoder(command.OutOrStdout())
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(record); encodeErr != nil && err == nil {
			err = encodeErr
		}
	}
	return err
}

// action is the command of one action on one installation. What it leaves
// out comes from the installation, when nemo knows it. Its progress goes
// to stderr, and with --json, the record of the run to stdout.
func action(name, short, example string, call func(*nemo.Nemo, context.Context, nemo.ActionRequest) (nemo.Run, error)) *cobra.Command {
	var (
		request         nemo.ActionRequest
		parameters      []string
		credentialFlags []string
		namespace       string
		kubeconfig      string
		volumes         string
	)
	command := &cobra.Command{
		Use:               name + " [INSTALLATION]",
		ValidArgsFunction: completeInstallations,
		Short:             short,
		Example:           example,
		Args:              cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if len(arguments) == 1 {
				request.Name = arguments[0]
			}
			var err error
			if request.Stack, err = stackName(request.Stack); err != nil {
				return err
			}
			if request.Parameters, err = pairs("--param", parameters); err != nil {
				return err
			}
			sources, err := pairs("--credential", credentialFlags)
			if err != nil {
				return err
			}
			workingDirectory, err := os.Getwd()
			if err != nil {
				return err
			}
			for _, name := range slices.Sorted(maps.Keys(sources)) {
				strategy, err := credentials.Parse(name, sources[name])
				if err != nil {
					return err
				}
				request.Credentials = append(request.Credentials, credentials.Absolute(strategy, workingDirectory))
			}
			if volumes != "" {
				data, err := os.ReadFile(volumes)
				if err != nil {
					return err
				}
				if err = json.Unmarshal(data, &request.Target.Volumes); err != nil {
					return fmt.Errorf("--volumes %s: %w", volumes, err)
				}
			}
			if request.Installer.Driver == target.DriverKubernetes {
				request.Installer.Kubernetes = &target.Kubernetes{Namespace: namespace, Kubeconfig: kubeconfig}
			}
			api, err := nemo.New(nemo.Options{})
			if err != nil {
				return err
			}
			events, stop := newRenderer(os.Stderr, request.Stack)
			request.Progress = events
			run, err := call(api, command.Context(), request)
			err = stop(err)
			if run.Revision == "" {
				return err
			}
			return render(command, run, err)
		},
	}
	flags := command.Flags()
	flags.Bool("json", false, "print the record of the run as JSON on stdout")
	flags.StringVarP(&request.BundleSource, "file", "f", "", "the bundle: a bundle.json, or where it was pushed, docker://REFERENCE or oci:PATH (default: the installation's)")
	flags.StringVar(&request.Stack, "stack", "", "stack the installation belongs to (default: the current one)")
	flags.StringVar(&request.Target.Host, "host", "", "address of the target's Docker: unix://, tcp:// or ssh://user@host (default: the installation's, else the environment's)")
	flags.StringVar(&request.Target.Context, "context", "", "docker context to reach the target with, instead of --host")
	flags.StringVar(&request.Target.Orchestrator, "orchestrator", "", "how the installer deploys the bundle on the target: compose or swarm (default: the installation's)")
	flags.StringVar(&request.Target.Images, "images", "", "how the images reach the target: registry, which the target pulls from, or load, into its daemon with no registry (default: the installation's, else registry)")
	flags.StringVar(&volumes, "volumes", "", "JSON file of the target's volume types, which make the volumes of a bundle with an x-nemo-volume type (default: the installation's)")
	flags.StringVar(&request.Installer.Driver, "driver", "", "where the invocation image runs: docker, on the target; or kubernetes, as a Job in a cluster that reaches the target over tcp:// (default: the installation's, else docker)")
	flags.StringVar(&namespace, "kubernetes-namespace", "", "namespace of the Job, with --driver kubernetes")
	flags.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig of the cluster, with --driver kubernetes (default: KUBECONFIG, ~/.kube/config, else the cluster nemo runs in)")
	flags.StringArrayVar(&parameters, "param", nil, "parameter value, NAME=VALUE, over the installation's (repeatable)")
	flags.StringArrayVar(&credentialFlags, "credential", nil, "credential source, NAME=KEY:VALUE with KEY a CNAB source, path, env, command or value, over the installation's (repeatable)")
	return command
}

// pairs parses NAME=VALUE flags.
func pairs(flag string, values []string) (map[string]string, error) {
	result := map[string]string{}
	for _, pair := range values {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("%s %q: expected NAME=VALUE", flag, pair)
		}
		result[name] = value
	}
	return result, nil
}

// reference writes the reference of the commands, a Markdown page each, for
// the documentation.
func reference() *cobra.Command {
	return &cobra.Command{
		Use:    "reference DIRECTORY",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			root := command.Root()
			root.DisableAutoGenTag = true
			return doc.GenMarkdownTree(root, arguments[0])
		},
	}
}

// completeStackFlags completes --stack with the names of the stacks, on every
// command that has it.
func completeStackFlags(command *cobra.Command) {
	if command.Flags().Lookup("stack") != nil {
		_ = command.RegisterFlagCompletionFunc("stack", completeStacks)
	}
	for _, child := range command.Commands() {
		completeStackFlags(child)
	}
}
