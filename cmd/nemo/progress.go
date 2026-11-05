package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	// expected is how many services the installation has, when nemo told.
	expected int
	// kind is set on the row of an object installations share, the stack's
	// own: network, volume, config or secret; users says who shares it.
	kind  string
	users string
}

// views follows the installations, and the objects they share, in the
// order they appear.
type views struct {
	ordered []*installationView
	// shared are the rows of the objects installations share, by group and
	// item: what an installation's installer says of one is the stack's row's.
	shared map[string]*installationView
}

func (views *views) of(event progress.Event) *installationView {
	if !strings.Contains(event.Installation, "/") && event.Item != "" {
		key := event.Group + "/" + event.Item
		if view, seen := views.shared[key]; seen {
			return view
		}
		if views.shared == nil {
			views.shared = map[string]*installationView{}
		}
		view := &installationView{name: event.Item, kind: strings.TrimSuffix(event.Group, "s"), state: progress.Pending}
		views.shared[key] = view
		views.ordered = append(views.ordered, view)
		return view
	}
	_, name, _ := strings.Cut(event.Installation, "/")
	for _, view := range views.ordered {
		if view.kind == "" && view.name == name {
			return view
		}
	}
	view := &installationView{name: name, state: progress.Pending, tasks: map[string]progress.Event{}, objects: map[string]int{}}
	views.ordered = append(views.ordered, view)
	return view
}

// apply records the event, and returns the view it changed: none for an
// installation's event about an object it shares, which the stack's row
// tells.
func (views *views) apply(event progress.Event) *installationView {
	if _, sharedObject := views.shared[event.Group+"/"+event.Item]; sharedObject && strings.Contains(event.Installation, "/") {
		return nil
	}
	view := views.of(event)
	if view.kind != "" {
		switch event.State {
		case progress.Pending:
			view.users = event.Message
		case progress.Warning:
			view.warnings = append(view.warnings, event.Message)
		default:
			view.state = event.State
			if event.Message != "" {
				view.message = event.Message
			}
		}
		return view
	}
	switch {
	case event.Group == progress.Installation:
		view.state = event.State
		switch event.State {
		case progress.Pending:
			view.action, view.expected = event.Message, int(event.Total)
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
		view.warnings = append(view.warnings, words(strings.TrimSuffix(event.Group, "s"), event.Item, event.Message))
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
func newRenderer(output *os.File, title string, all bool) (progress.Sink, func(error) error) {
	if !term.IsTerminal(int(output.Fd())) {
		lines := &lines{output: output}
		return lines, func(err error) error { return shown(err, &lines.mutex, &lines.views) }
	}
	live := &live{output: output, title: title, all: all, done: make(chan struct{}), stopped: make(chan struct{})}
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
	if view == nil {
		return
	}
	say := func(format string, arguments ...any) {
		fmt.Fprintf(lines.output, "%s: %s\n", view.name, fmt.Sprintf(format, arguments...))
	}
	if view.kind != "" {
		switch event.State {
		case progress.Done:
			say("%s %s", view.kind, sharedText(view))
		case progress.Warning:
			say("warning: %s", event.Message)
		case progress.Error:
			say("%s failed: %s", view.kind, event.Message)
		}
		return
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
		say("warning: %s", words(event.Group, event.Item, event.Message))
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
	mutex  sync.Mutex
	output *os.File
	title  string
	// all shows the services of every installation at work, not only of
	// the only one, or of those in trouble.
	all     bool
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
	width, height, err := term.GetSize(int(live.output.Fd()))
	if err != nil || width <= 0 {
		width, height = 120, 0
	}
	live.mutex.Lock()
	lines := live.lines(width, height)
	live.mutex.Unlock()
	var screen strings.Builder
	if live.drawn > 0 {
		fmt.Fprintf(&screen, "\033[%dA", live.drawn)
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

// lines are the frame's lines, wrapped to the width. While the frame is
// redrawn, they fit in the terminal's height, as the cursor goes back over
// the screen only: the services ready fold into their installation's count,
// then the bottom is cut. The last frame, which nothing redraws, is whole.
func (live *live) lines(width, height int) []string {
	wrapped := func(frame []string) []string {
		var lines []string
		for _, line := range frame {
			lines = append(lines, wrap(line, width-1)...)
		}
		return lines
	}
	lines := wrapped(live.render(false))
	// The cursor stays on the line after the frame.
	if live.ended || height <= 0 || len(lines) < height {
		return lines
	}
	return window(wrapped(live.render(true)), 0, height-1)
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

// render returns the lines of the frame: a title that counts the
// installations by where they stand, then a line for each object
// installations share and each installation, their columns lined up. An
// installation at work shows its services only when it is the only one at
// work, or all shows them, else those in trouble; one that failed shows them
// all. Folded, the
// services ready are left to their installation's count.
func (live *live) render(folded bool) []string {
	frame := []string{bold.Render(live.title) + faint.Render(" · "+live.tally()), ""}
	working := 0
	for _, view := range live.views.ordered {
		if view.kind == "" && view.state == progress.Working {
			working++
		}
	}

	rows := make([]frameRow, len(live.views.ordered))
	shown := make([][]string, len(live.views.ordered))
	widths := [3]int{}
	for i, view := range live.views.ordered {
		rows[i] = live.row(view)
		widths[0] = max(widths[0], len(view.name))
		// Only the lines that end with a duration line it up.
		if rows[i].elapsed != "" {
			widths[1] = max(widths[1], utf8.RuneCountInString(rows[i].detail))
		}
		if view.kind != "" {
			continue
		}
		for _, name := range slices.Sorted(slices.Values(view.services)) {
			task := view.tasks[name]
			unfold := view.state == progress.Error || view.state == progress.Working && (live.all || working == 1 || inTrouble(task))
			if unfold && (!folded || task.State != progress.Done) {
				shown[i] = append(shown[i], name)
				widths[2] = max(widths[2], len(name))
			}
		}
	}
	for i, view := range live.views.ordered {
		frame = append(frame, rows[i].render(widths[0], widths[1]))
		frame = append(frame, warnings(view)...)
		frame = append(frame, services(view, shown[i], widths[2])...)
		if view.state == progress.Error {
			for _, line := range view.output {
				if !view.repeats(line) {
					frame = append(frame, faint.Render("    "+line))
				}
			}
		}
	}
	return frame
}

// tally counts the installations, and those done, at work, waiting and
// failed, while some are not done.
func (live *live) tally() string {
	counts := map[progress.State]int{}
	total := 0
	for _, view := range live.views.ordered {
		if view.kind == "" {
			counts[view.state]++
			total++
		}
	}
	parts := []string{plural(total, "installation")}
	if counts[progress.Done] == total {
		return parts[0]
	}
	waiting := "waiting"
	if live.ended {
		waiting = "not run"
	}
	for _, state := range []struct {
		state progress.State
		name  string
	}{{progress.Done, "done"}, {progress.Working, "working"}, {progress.Error, "failed"}, {progress.Pending, waiting}} {
		if counts[state.state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state.state], state.name))
		}
	}
	return strings.Join(parts, " · ")
}

// frameRow is the line of an installation or of a shared object, its columns
// as plain text, and how each shows.
type frameRow struct {
	icon                 string
	name, action, detail string
	// colored is the detail as it shows, when it has colors of its own.
	colored                  string
	elapsed                  string
	nameStyle, actionStyle   lipgloss.Style
	detailStyle, otherStyles lipgloss.Style
}

// row says where the installation or the shared object stands.
func (live *live) row(view *installationView) frameRow {
	line := frameRow{name: view.name, action: view.action, nameStyle: plain, actionStyle: faint, detailStyle: faint, otherStyles: faint}
	if view.kind != "" {
		line.action = view.kind
	}
	waiting := "waiting"
	if live.ended {
		waiting = "not run"
	}
	switch view.state {
	case progress.Pending:
		line.icon, line.nameStyle, line.detail = faint.Render("·"), faint, waiting
		if view.kind != "" {
			line.detail = view.users
		}
	case progress.Done:
		line.icon, line.detail = green.Render("✔"), summary(view)
		if view.kind != "" {
			line.detail = sharedText(view)
		} else {
			line.elapsed = elapsed(view).String()
		}
	case progress.Error:
		line.icon, line.nameStyle, line.actionStyle, line.detailStyle = red.Render("✖"), bold, plain, red
		line.detail = view.message
	default:
		line.icon, line.nameStyle, line.actionStyle, line.detailStyle = cyan.Render(spinnerFrames[live.frame%len(spinnerFrames)]), bold, plain, plain
		if view.kind != "" {
			line.detail = view.message
		} else {
			line.detail, line.colored = doing(view)
			line.elapsed = elapsed(view).String()
		}
	}
	return line
}

// render lines the row up: the name and the detail padded to the widths.
func (line frameRow) render(nameWidth, detailWidth int) string {
	text := line.icon + " " + line.nameStyle.Render(fmt.Sprintf("%-*s", nameWidth, line.name)) + "  " + line.actionStyle.Render(fmt.Sprintf("%-9s", line.action)) + "  "
	detail := line.detailStyle.Render(line.detail)
	if line.colored != "" {
		detail = line.colored
	}
	if line.elapsed == "" {
		return text + detail
	}
	return text + detail + strings.Repeat(" ", max(0, detailWidth-utf8.RuneCountInString(line.detail))) + "  " + line.otherStyles.Render(line.elapsed)
}

// sharedText is what became of a shared object, and how many share it.
func sharedText(view *installationView) string {
	if view.message == "" {
		return view.users
	}
	return view.message + " · " + view.users
}

// inTrouble tells a service that needs looking at: failed, or restarted
// by Swarm.
func inTrouble(task progress.Event) bool {
	return task.State == progress.Error || strings.HasPrefix(task.Message, "restarting")
}

// doing is what an installation at work is at: its images, then its
// services, on a meter. It returns it as text, and as it shows.
func doing(view *installationView) (string, string) {
	if view.images.State == progress.Working {
		loaded := int(view.images.Current)
		cells, colored := meter([]int{loaded, int(view.images.Total) - loaded}, []lipgloss.Style{green, faint})
		text := fmt.Sprintf("%d/%d images loaded", loaded, view.images.Total)
		return cells + "  " + text, colored + "  " + text
	}
	total := max(view.expected, len(view.services))
	if total == 0 {
		return "starting", ""
	}
	// A square for each service, the color of its line: green ready, cyan
	// verified or checked, yellow on its way, red failed, faint not there yet.
	counts := make([]int, 5)
	for _, name := range view.services {
		task := view.tasks[name]
		switch {
		case task.State == progress.Done:
			counts[0]++
		case task.Message == "verifying" || task.Message == "checking health":
			counts[1]++
		case task.State == progress.Error:
			counts[3]++
		default:
			counts[2]++
		}
	}
	counts[4] = total - len(view.services)
	cells, colored := meter(counts, []lipgloss.Style{green, cyan, yellow, red, faint})
	state := "services ready"
	if view.action == "uninstall" {
		state = "services removed"
	}
	text := fmt.Sprintf("%d/%d %s", counts[0], total, state)
	return cells + "  " + text, colored + "  " + text
}

// meterWidth is how many squares a meter has at most: one for each of what
// it counts, or each its share of them.
const meterWidth = 10

// meter draws the counts as squares, in the styles, the last one empty:
// one square each up to meterWidth, else their shares, one square at least
// for any count. The text is padded to meterWidth, for the columns after.
func meter(counts []int, styles []lipgloss.Style) (string, string) {
	total := 0
	for _, count := range counts {
		total += count
	}
	squares := slices.Clone(counts)
	if total > meterWidth {
		squares = shares(counts, total)
	}
	var text, colored strings.Builder
	for i, count := range squares {
		if count == 0 {
			continue
		}
		glyph := "▰"
		if i == len(squares)-1 {
			glyph = "▱"
		}
		text.WriteString(strings.Repeat(glyph, count))
		colored.WriteString(styles[i].Render(strings.Repeat(glyph, count)))
	}
	padding := strings.Repeat(" ", max(0, meterWidth-utf8.RuneCountInString(text.String())))
	return text.String() + padding, colored.String() + padding
}

// shares splits meterWidth squares among the counts, by largest remainder,
// and gives one to any count that has none, from the largest share.
func shares(counts []int, total int) []int {
	squares := make([]int, len(counts))
	remainders := make([]int, len(counts))
	given := 0
	for i, count := range counts {
		squares[i], remainders[i] = count*meterWidth/total, count*meterWidth%total
		given += squares[i]
	}
	for ; given < meterWidth; given++ {
		largest := 0
		for i := range remainders {
			if remainders[i] > remainders[largest] {
				largest = i
			}
		}
		squares[largest]++
		remainders[largest] = -1
	}
	for i, count := range counts {
		if count > 0 && squares[i] == 0 {
			squares[i]++
			squares[slices.Index(squares, slices.Max(squares))]--
		}
	}
	return squares
}

// services are the lines of the services named, of an installation, the
// names padded to the width, their tasks or containers counted: green once
// ready, cyan while Swarm verifies that they stay up or Compose checks
// their health, yellow on their way, red on failure.
func services(view *installationView, names []string, width int) []string {
	var rendered []string
	for _, name := range names {
		task := view.tasks[name]
		icon, style := "◌", yellow
		switch {
		case task.State == progress.Done:
			icon, style = "✔", green
		case task.State == progress.Error:
			icon, style = "✖", red
		case task.Message == "verifying" || task.Message == "checking health":
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

// words joins those that are not empty.
func words(given ...string) string {
	return strings.Join(slices.DeleteFunc(given, func(word string) bool { return word == "" }), " ")
}
