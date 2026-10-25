package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/sntns/nemo/pkg/progress"
)

// installationView is what is known of an installation while its stack moves.
type installationView struct {
	name     string
	action   string
	state    progress.State
	message  string
	started  time.Time
	ended    time.Time
	images   progress.Event
	services []string
	tasks    map[string]progress.Event
	objects  map[string]int
	warnings []string
	output   []string
}

// views follows the installations in the order they appear.
type views struct {
	ordered []*installationView
}

func (views *views) of(installation string) *installationView {
	_, name, _ := strings.Cut(installation, "/")
	for _, view := range views.ordered {
		if view.name == name {
			return view
		}
	}
	view := &installationView{name: name, state: progress.Pending, tasks: map[string]progress.Event{}, objects: map[string]int{}}
	views.ordered = append(views.ordered, view)
	return view
}

// apply records the event, and returns the view it changed.
func (views *views) apply(event progress.Event) *installationView {
	view := views.of(event.Installation)
	switch {
	case event.Group == progress.Installation:
		view.state = event.State
		switch event.State {
		case progress.Pending:
			view.action = event.Message
		case progress.Working:
			view.action, view.started = event.Message, time.Now()
		case progress.Done:
			view.ended = time.Now()
		case progress.Error:
			view.ended, view.message = time.Now(), event.Message
		}
	case event.Group == progress.Images:
		view.images = event
	case event.Group == progress.Output:
		view.output = append(view.output, event.Message)
	case event.State == progress.Warning:
		view.warnings = append(view.warnings, strings.TrimSpace(strings.TrimSuffix(event.Group, "s")+" "+event.Item+" "+event.Message))
	case event.Group == "services":
		if _, seen := view.tasks[event.Item]; !seen {
			view.services = append(view.services, event.Item)
		}
		if event.Total == 0 {
			event.Current, event.Total = view.tasks[event.Item].Current, view.tasks[event.Item].Total
		}
		view.tasks[event.Item] = event
	case event.State == progress.Done:
		view.objects[event.Group]++
	}
	return view
}

// repeats reports whether a line of the installer's output only repeats the
// error of a service, shown already: error: service NAME: MESSAGE.
func (view *installationView) repeats(line string) bool {
	service, ok := strings.CutPrefix(line, "error: service ")
	if !ok {
		return false
	}
	name, message, _ := strings.Cut(service, ": ")
	task, seen := view.tasks[name]
	return seen && task.State == progress.Error && task.Message == message
}

func elapsed(view *installationView) time.Duration {
	end := view.ended
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(view.started).Round(100 * time.Millisecond)
}

// summary counts what the installation has: its services, and the volumes,
// networks, configs and secrets it made sure of.
func summary(view *installationView) string {
	parts := []string{}
	if count := len(view.services); count > 0 {
		parts = append(parts, plural(count, "service"))
	}
	for _, group := range []string{"volumes", "networks", "configs", "secrets"} {
		if count := view.objects[group]; count > 0 {
			parts = append(parts, plural(count, strings.TrimSuffix(group, "s")))
		}
	}
	if count := len(view.warnings); count > 0 {
		parts = append(parts, plural(count, "warning"))
	}
	return strings.Join(parts, " · ")
}

func plural(count int, name string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", name)
	}
	return fmt.Sprintf("%d %ss", count, name)
}

// past is how an action reads once done.
func past(action string) string {
	return map[string]string{"install": "installed", "upgrade": "upgraded", "uninstall": "uninstalled"}[action]
}

// newRenderer returns the sink that shows the events on output, and the
// function that ends it with the error of what it showed: a view redrawn in
// place on a terminal, else a line for each change.
func newRenderer(output *os.File, title string) (progress.Sink, func(error) error) {
	if !term.IsTerminal(int(output.Fd())) {
		lines := &lines{output: output}
		return lines, func(err error) error { return shown(err, &lines.mutex, &lines.views) }
	}
	live := &live{output: output, title: title, done: make(chan struct{}), stopped: make(chan struct{})}
	_, _ = io.WriteString(output, "\033[?25l")
	go live.run()
	return live, func(err error) error {
		live.stop()
		return shown(err, &live.mutex, &live.views)
	}
}

// shownError is an error the progress showed: main exits without printing
// it again.
type shownError struct{ error }

func (shown shownError) Unwrap() error { return shown.error }

// shown marks the error as shown when the progress showed an installation
// failing: up and down stop at the first that fails, with its error.
func shown(err error, mutex *sync.Mutex, views *views) error {
	mutex.Lock()
	defer mutex.Unlock()
	if err != nil && slices.ContainsFunc(views.ordered, func(view *installationView) bool { return view.state == progress.Error }) {
		return shownError{err}
	}
	return err
}

// lines writes a line for each change of an installation, for a log.
type lines struct {
	mutex  sync.Mutex
	output io.Writer
	views  views
}

func (lines *lines) Emit(event progress.Event) {
	lines.mutex.Lock()
	defer lines.mutex.Unlock()
	view := lines.views.apply(event)
	say := func(format string, arguments ...any) {
		fmt.Fprintf(lines.output, "%s: %s\n", view.name, fmt.Sprintf(format, arguments...))
	}
	switch {
	case event.Group == progress.Installation && event.State == progress.Working:
		say("%s", event.Message)
	case event.Group == progress.Installation && event.State == progress.Done:
		say("%s in %s · %s", past(event.Message), elapsed(view), summary(view))
	case event.Group == progress.Installation && event.State == progress.Error:
		say("%s failed: %s", view.action, event.Message)
	case event.Group == progress.Images && event.State == progress.Working && event.Current == 0:
		say("loading %s", plural(int(event.Total), "image"))
	case event.Group == progress.Output && !view.repeats(event.Message):
		say("%s", event.Message)
	case event.State == progress.Warning:
		say("warning: %s %s %s", event.Group, event.Item, event.Message)
	case event.State == progress.Error:
		say("%s %s failed: %s", strings.TrimSuffix(event.Group, "s"), event.Item, event.Message)
	case event.Group == "services" && event.State == progress.Done:
		if task := view.tasks[event.Item]; task.Total > 0 {
			say("service %s %d/%d %s", event.Item, task.Current, task.Total, event.Message)
		} else {
			say("service %s %s", event.Item, event.Message)
		}
	case event.State == progress.Done && event.Message != "":
		say("%s %s %s", strings.TrimSuffix(event.Group, "s"), event.Item, event.Message)
	}
}

// live redraws the installations in place, those at work unfolded.
type live struct {
	mutex   sync.Mutex
	output  *os.File
	title   string
	views   views
	drawn   int
	frame   int
	ended   bool
	done    chan struct{}
	stopped chan struct{}
}

func (live *live) Emit(event progress.Event) {
	live.mutex.Lock()
	defer live.mutex.Unlock()
	live.views.apply(event)
}

func (live *live) run() {
	defer close(live.stopped)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-live.done:
			live.mutex.Lock()
			live.ended = true
			live.mutex.Unlock()
			live.draw()
			return
		case <-ticker.C:
			live.draw()
		}
	}
}

func (live *live) stop() {
	close(live.done)
	<-live.stopped
	_, _ = io.WriteString(live.output, "\033[?25h")
}

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	faint  = lipgloss.NewStyle().Faint(true)
	bold   = lipgloss.NewStyle().Bold(true)
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// draw writes the frame over the previous one, each line wrapped to the
// terminal's width, so that the lines to go back over are the lines
// written. lipgloss brings the colors down to what the terminal shows.
func (live *live) draw() {
	live.mutex.Lock()
	frame := live.render()
	live.mutex.Unlock()
	width, _, err := term.GetSize(int(live.output.Fd()))
	if err != nil || width <= 0 {
		width = 120
	}
	var screen strings.Builder
	if live.drawn > 0 {
		fmt.Fprintf(&screen, "\033[%dA", live.drawn)
	}
	var lines []string
	for _, line := range frame {
		lines = append(lines, wrap(line, width-1)...)
	}
	for _, line := range lines {
		screen.WriteString("\r\033[2K")
		screen.WriteString(line)
		screen.WriteString("\n")
	}
	// Lines the previous frame had beyond this one are cleared.
	for extra := len(lines); extra < live.drawn; extra++ {
		screen.WriteString("\r\033[2K\n")
	}
	if live.drawn > len(lines) {
		fmt.Fprintf(&screen, "\033[%dA", live.drawn-len(lines))
	}
	live.drawn = len(lines)
	live.frame++
	_, _ = lipgloss.Fprint(live.output, screen.String())
}

// wrap cuts the line into lines of the width, the next ones indented as the
// first, so that an error is read whole.
func wrap(line string, width int) []string {
	plain := ansi.Strip(line)
	indent := len(plain) - len(strings.TrimLeft(plain, " "))
	if indent >= width/2 {
		indent = 0
	}
	lines := strings.Split(ansi.Wrap(line, width-indent, ""), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", indent) + lines[i]
	}
	return lines
}

// render returns the lines of the frame: a title, then a line for each
// installation, and the services of those at work or failed.
func (live *live) render() []string {
	frame := []string{bold.Render(live.title) + faint.Render(" · "+plural(len(live.views.ordered), "installation")), ""}
	width := 0
	for _, view := range live.views.ordered {
		width = max(width, len(view.name))
	}
	for _, view := range live.views.ordered {
		name := fmt.Sprintf("%-*s", width, view.name)
		action := fmt.Sprintf("%-9s", view.action)
		switch view.state {
		case progress.Pending:
			waiting := "waiting"
			if live.ended {
				waiting = "not run"
			}
			frame = append(frame, faint.Render("· "+name+"  "+action+"  "+waiting))
		case progress.Done:
			frame = append(frame, green.Render("✔")+" "+name+"  "+faint.Render(action+"  "+summary(view)+"  "+elapsed(view).String()))
			frame = append(frame, warnings(view)...)
		case progress.Error:
			frame = append(frame, red.Render("✖")+" "+bold.Render(name)+"  "+action+"  "+red.Render(view.message))
			frame = append(frame, warnings(view)...)
			frame = append(frame, live.services(view)...)
			for _, line := range view.output {
				if !view.repeats(line) {
					frame = append(frame, faint.Render("    "+line))
				}
			}
		default:
			doing := "starting"
			switch {
			case view.images.State == progress.Working:
				doing = fmt.Sprintf("loading images %d/%d", view.images.Current, view.images.Total)
			case len(view.services) > 0:
				ready := 0
				for _, task := range view.tasks {
					if task.State == progress.Done {
						ready++
					}
				}
				state := "ready"
				if view.action == "uninstall" {
					state = "removed"
				}
				doing = fmt.Sprintf("%d/%d services %s", ready, len(view.services), state)
			}
			spinner := cyan.Render(spinnerFrames[live.frame%len(spinnerFrames)])
			frame = append(frame, spinner+" "+bold.Render(name)+"  "+action+"  "+doing+faint.Render("  "+elapsed(view).String()))
			frame = append(frame, warnings(view)...)
			frame = append(frame, live.services(view)...)
		}
	}
	return frame
}

// services are the lines of the services of an installation, with their
// tasks as Swarm counts them: green once ready, cyan while Swarm verifies
// that they stay up, yellow on their way, red on failure.
func (live *live) services(view *installationView) []string {
	width := 0
	for _, name := range view.services {
		width = max(width, len(name))
	}
	var rendered []string
	for _, name := range slices.Sorted(slices.Values(view.services)) {
		task := view.tasks[name]
		icon, style := "◌", yellow
		switch {
		case task.State == progress.Done:
			icon, style = "✔", green
		case task.State == progress.Error:
			icon, style = "✖", red
		case task.Message == "verifying":
			style = cyan
		}
		tasks := ""
		// A service being removed has no tasks to count.
		if task.Total > 0 {
			filled := int(min(task.Current, 8))
			bar := style.Render(strings.Repeat("▰", filled)) + faint.Render(strings.Repeat("▱", max(0, int(min(task.Total, 8))-filled)))
			tasks = bar + strings.Repeat(" ", max(0, 8-int(min(task.Total, 8)))) + fmt.Sprintf(" %-5s ", fmt.Sprintf("%d/%d", task.Current, task.Total))
		}
		rendered = append(rendered, fmt.Sprintf("    %s %-*s  %s%s", style.Render(icon), width, name, tasks, style.Render(task.Message)))
	}
	return rendered
}

// warnings are the lines of an installation's warnings, kept in sight.
func warnings(view *installationView) []string {
	lines := make([]string, len(view.warnings))
	for i, warning := range view.warnings {
		lines[i] = yellow.Render("    ! " + warning)
	}
	return lines
}
