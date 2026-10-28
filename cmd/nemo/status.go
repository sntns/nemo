package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/nemo"
)

// stackName is the stack given, else the current one.
func stackName(given string) (string, error) {
	if given != "" {
		return given, nil
	}
	store, _, err := stackStore()
	if err != nil {
		return "", err
	}
	current, err := store.Current()
	if err != nil {
		return "", err
	}
	if current == "" {
		return "", errors.New("no current stack: create one with nemo stack create, import one with nemo stack import, or give one with --stack")
	}
	return current, nil
}

// completeInstallations completes the first argument with the installations
// nemo keeps of the stack of --stack, else of the current one.
func completeInstallations(command *cobra.Command, arguments []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	given, _ := command.Flags().GetString("stack")
	name, err := stackName(given)
	if len(arguments) > 0 || err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	api, err := nemo.New(nemo.Options{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	installations, _ := api.Installations(command.Context(), name)
	names := make([]cobra.Completion, 0, len(installations))
	for _, installation := range installations {
		names = append(names, installation.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeServices completes the first argument with the installations of
// the stack, the second with the services of the installation.
func completeServices(command *cobra.Command, arguments []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(arguments) != 1 {
		return completeInstallations(command, arguments, toComplete)
	}
	given, _ := command.Flags().GetString("stack")
	name, err := stackName(given)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	api, err := nemo.New(nemo.Options{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	current, _ := api.Status(command.Context(), name)
	var names []cobra.Completion
	for _, installation := range current.Installations {
		if installation.Name == arguments[0] {
			for _, service := range installation.Services {
				names = append(names, service.Name)
			}
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeManifestInstallations completes with the installations of the
// stack's manifest not given yet.
func completeManifestInstallations(command *cobra.Command, arguments []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	file, _ := command.Flags().GetString("file")
	given, _ := command.Flags().GetString("stack")
	loaded, err := chosenStack(file, given)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []cobra.Completion
	for _, installation := range loaded.Installations {
		if !slices.Contains(arguments, installation.Name) {
			names = append(names, installation.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func status() *cobra.Command {
	var (
		name string
		all  bool
	)
	command := &cobra.Command{
		Use:     "status",
		Short:   "Show where a stack stands: its installations, and the services they run",
		Example: "  nemo status                   # the current stack\n  nemo status --stack demo --all",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			stack, err := stackName(name)
			if err != nil {
				return err
			}
			api, err := nemo.New(nemo.Options{})
			if err != nil {
				return err
			}
			current, err := api.Status(command.Context(), stack)
			if err != nil {
				return err
			}
			if asJSON, _ := command.Flags().GetBool("json"); asJSON {
				return render(command, current, nil)
			}
			_, err = lipgloss.Fprint(command.OutOrStdout(), statusText(current, all, time.Now()))
			return err
		},
	}
	command.Flags().StringVar(&name, "stack", "", "name of the stack (default: the current one)")
	command.Flags().BoolVarP(&all, "all", "a", false, "show the services of every installation, not only of those not ready")
	command.Flags().Bool("json", false, "print the status as JSON on stdout")
	return command
}

// statusText is a line for each installation, its services below when it
// is not ready, or with all, every line's columns lined up.
func statusText(current nemo.StackStatus, all bool, now time.Time) string {
	var rows []row
	for _, installation := range current.Installations {
		line, unfold := installationRow(installation, all, now)
		rows = append(rows, line)
		if unfold {
			for _, service := range installation.Services {
				rows = append(rows, serviceRow(service))
			}
		}
	}
	widths := columnWidths(rows)
	var text strings.Builder
	text.WriteString(statusTitle(current))
	for _, line := range rows {
		text.WriteString(line.render(widths))
	}
	return text.String()
}

// statusTitle is the stack's name, its orchestrator, and where it runs.
func statusTitle(current nemo.StackStatus) string {
	title := []string{current.Target.Orchestrator}
	if where := cmp.Or(current.Target.Host, current.Target.Context); where != "" {
		title = append(title, where)
	}
	return bold.Render(current.Name) + faint.Render(" · "+strings.Join(title, " · ")) + "\n\n"
}

// installationRow is the line of an installation: red when its last action
// failed, faint once uninstalled, yellow on its way, green once ready. It
// tells whether its services unfold below it: unless it is ready or
// uninstalled, and when ready with all.
func installationRow(installation nemo.InstallationState, all bool, now time.Time) (row, bool) {
	ready := readyServices(installation.Services)
	count := ""
	if len(installation.Services) > 0 {
		count = fmt.Sprintf("%d/%d services", ready, len(installation.Services))
	}
	line := row{cells: [4]string{installation.Name, installation.Version, lastAction(installation.Action, installation.Result) + " " + ago(installation.Modified, now), count}}
	switch {
	case installation.Result == cnab.StatusFailed || installation.Result == cnab.StatusCanceled:
		line.icon, line.styles = red.Render("✖"), [4]lipgloss.Style{bold, plain, red, plain}
		return line, true
	case !installation.Installed:
		line.icon, line.styles = faint.Render("·"), [4]lipgloss.Style{faint, faint, faint, faint}
		return line, false
	case installation.Result != cnab.StatusSucceeded || ready < len(installation.Services):
		line.icon, line.styles = yellow.Render("◌"), [4]lipgloss.Style{bold, plain, yellow, yellow}
		return line, true
	}
	line.icon, line.styles = green.Render("✔"), [4]lipgloss.Style{plain, faint, faint, faint}
	return line, all
}

func readyServices(services []nemo.ServiceState) int {
	ready := 0
	for _, service := range services {
		if serviceReady(service) {
			ready++
		}
	}
	return ready
}

// columnWidths is the width of each column: its widest cell, the indent of
// the first included.
func columnWidths(rows []row) [4]int {
	var widths [4]int
	for _, line := range rows {
		for i, cell := range line.cells {
			if i == 0 {
				cell = strings.Repeat(" ", line.indent) + cell
			}
			widths[i] = max(widths[i], len(cell))
		}
	}
	return widths
}

// render writes the row with its columns at the widths, up to its last cell.
func (line row) render(widths [4]int) string {
	last := 0
	for i, cell := range line.cells {
		if cell != "" {
			last = i
		}
	}
	var text strings.Builder
	text.WriteString(strings.Repeat(" ", line.indent) + line.icon + " ")
	for i := 0; i <= last; i++ {
		cell, width := line.cells[i], widths[i]
		if i == 0 {
			width -= line.indent
		}
		if i < last {
			cell = fmt.Sprintf("%-*s  ", width, cell)
		}
		text.WriteString(line.styles[i].Render(cell))
	}
	text.WriteString("\n")
	return text.String()
}

var plain = lipgloss.NewStyle()

// row is a line of the status: an installation, or one of its services
// below it, with its name, version, state and count of what runs.
type row struct {
	indent int
	icon   string
	cells  [4]string
	styles [4]lipgloss.Style
}

// serviceRow is the line of a service: green once ready, red when its
// containers stopped or are unhealthy, yellow on their way; its state is
// said only when it is not ready.
func serviceRow(service nemo.ServiceState) row {
	icon, style, state, count := "◌", yellow, service.State, yellow
	switch {
	case serviceReady(service):
		icon, style, state, count = "✔", green, "", faint
	case slices.Contains([]string{"exited", "dead", "unhealthy"}, service.State):
		icon, style, count = "✖", red, red
	}
	return row{
		indent: 2,
		icon:   style.Render(icon),
		cells:  [4]string{service.Name, "", state, fmt.Sprintf("%d/%d", service.Running, service.Desired)},
		styles: [4]lipgloss.Style{plain, plain, style, count},
	}
}

// serviceReady reports whether all the tasks or containers of the service run.
func serviceReady(service nemo.ServiceState) bool {
	return service.Running >= service.Desired && (service.State == "running" || service.State == "healthy")
}

// lastAction is how the last action went: installed, upgrade failed…
func lastAction(action, result string) string {
	if result == cnab.StatusSucceeded {
		return cmp.Or(past(action), action)
	}
	return action + " " + result
}

// ago is how long ago the time was, roughly.
func ago(then, now time.Time) string {
	elapsed := now.Sub(then)
	switch {
	case then.IsZero():
		return ""
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	}
}

// logColors tell the services apart in the logs, as Compose does.
var logColors = []string{"6", "3", "2", "5", "4", "14", "11", "10", "13", "12"}

func logs() *cobra.Command {
	request := nemo.LogsRequest{}
	command := &cobra.Command{
		Use:   "logs [INSTALLATION [SERVICE]]",
		Short: "Print the logs of the services of a stack, of one installation, or of one service",
		Example: `  nemo logs -f
  nemo logs api --tail 100
  nemo logs database postgres -f`,
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: completeServices,
		RunE: func(command *cobra.Command, arguments []string) error {
			var err error
			if request.Stack, err = stackName(request.Stack); err != nil {
				return err
			}
			if len(arguments) > 0 {
				request.Installation = arguments[0]
			}
			if len(arguments) > 1 {
				request.Service = arguments[1]
			}
			api, err := nemo.New(nemo.Options{})
			if err != nil {
				return err
			}
			// The services are read first, to give each its color and to line up the logs.
			current, err := api.Status(command.Context(), request.Stack)
			if err != nil {
				return err
			}
			prefixes := map[string]string{}
			width := 0
			for _, installation := range current.Installations {
				for _, service := range installation.Services {
					if cmp.Or(request.Installation, installation.Name) == installation.Name && cmp.Or(request.Service, service.Name) == service.Name {
						width = max(width, len(service.Name))
						prefixes[service.Name] = ""
					}
				}
			}
			for i, service := range slices.Sorted(maps.Keys(prefixes)) {
				style := lipgloss.NewStyle().Foreground(lipgloss.Color(logColors[i%len(logColors)]))
				prefixes[service] = style.Render(fmt.Sprintf("%-*s |", width, service))
			}
			var mutex sync.Mutex
			output := command.OutOrStdout()
			request.Line = func(service, line string) {
				mutex.Lock()
				defer mutex.Unlock()
				_, _ = lipgloss.Fprintln(output, cmp.Or(prefixes[service], service+" |"), line)
			}
			return api.Logs(command.Context(), request)
		},
	}
	command.Flags().StringVar(&request.Stack, "stack", "", "name of the stack (default: the current one)")
	command.Flags().BoolVarP(&request.Follow, "follow", "f", false, "follow the logs, until interrupted")
	command.Flags().StringVarP(&request.Tail, "tail", "n", "all", "how many lines of each service to start with: a number, or all")
	return command
}
