package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/sntns/nemo/pkg/progress"
)

// upEvents are the events of an up of two installations, the second failing.
func upEvents(sink progress.Sink) {
	runtime, iam := progress.About(sink, "demo/runtime"), progress.About(sink, "demo/iam")
	runtime.Emit(progress.Event{Group: progress.Installation, State: progress.Pending, Message: "install"})
	iam.Emit(progress.Event{Group: progress.Installation, State: progress.Pending, Message: "install"})
	runtime.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "install"})
	runtime.Emit(progress.Event{Group: progress.Images, State: progress.Working, Current: 0, Total: 2})
	runtime.Emit(progress.Event{Group: progress.Images, State: progress.Working, Current: 1, Total: 2})
	runtime.Emit(progress.Event{Group: progress.Images, State: progress.Done, Current: 2, Total: 2})
	runtime.Emit(progress.Event{Group: "volumes", Item: "demo_data", State: progress.Done, Message: "created"})
	runtime.Emit(progress.Event{Group: "networks", Item: "demo_default", State: progress.Done})
	runtime.Emit(progress.Event{Group: "services", Item: "mongo", State: progress.Working, Message: "created"})
	runtime.Emit(progress.Event{Group: "services", Item: "mongo", State: progress.Working, Current: 0, Total: 1, Message: "preparing"})
	runtime.Emit(progress.Event{Group: "services", Item: "mongo", State: progress.Working, Current: 1, Total: 1, Message: "running"})
	runtime.Emit(progress.Event{Group: "services", Item: "mongo", State: progress.Done, Message: "running"})
	runtime.Emit(progress.Event{Group: progress.Installation, State: progress.Done, Message: "install"})
	iam.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "install"})
	iam.Emit(progress.Event{Group: "services", Item: "edge", State: progress.Working, Current: 0, Total: 3, Message: "preparing"})
	iam.Emit(progress.Event{Group: "services", Item: "edge", State: progress.Error, Message: "No such image"})
	iam.Emit(progress.Event{Group: progress.Output, State: progress.Working, Message: "error: service edge: No such image"})
	iam.Emit(progress.Event{Group: progress.Output, State: progress.Working, Message: "panic: the installer's own failure"})
	iam.Emit(progress.Event{Group: progress.Installation, State: progress.Error, Message: "the installer exited with code 1"})
}

var durations = regexp.MustCompile(`\d+(\.\d+)?(ms|s)`)

func TestLines(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	upEvents(&lines{output: &output})
	assert.Equal(t, `runtime: install
runtime: loading 2 images
runtime: volume demo_data created
runtime: service mongo 1/1 running
runtime: installed in D · 1 service · 1 volume · 1 network
iam: install
iam: service edge failed: No such image
iam: panic: the installer's own failure
iam: install failed: the installer exited with code 1
`, durations.ReplaceAllString(output.String(), "D"), "a line for each change, none repeated")
}

func TestLive_uninstall(t *testing.T) {
	t.Parallel()
	live := &live{title: "demo"}
	web := progress.About(live, "demo/web")
	web.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "uninstall"})
	web.Emit(progress.Event{Group: "services", Item: "web", State: progress.Done, Message: "removed"})
	web.Emit(progress.Event{Group: "volumes", Item: "demo_data", State: progress.Warning, Message: "kept: still in use"})
	frame := durations.ReplaceAllString(ansi.Strip(strings.Join(live.render(false), "\n")), "D")
	assert.Equal(t, `demo · 1 installation · 1 working

⠋ web  uninstall  ▰▰▰▰▰▰▰▰▰▰  1/1 services removed  D
    ! volume demo_data kept: still in use
    ✔ web  removed`, frame, "no tasks to count for a service removed, and the warning in sight")
}

func TestLive(t *testing.T) {
	t.Parallel()
	live := &live{title: "demo · swarm"}
	upEvents(live)
	frame := durations.ReplaceAllString(ansi.Strip(strings.Join(live.render(false), "\n")), "D")
	assert.Equal(t, `demo · swarm · 2 installations · 1 done · 1 failed

✔ runtime  install    1 service · 1 volume · 1 network  D
✖ iam      install    the installer exited with code 1
    ✖ edge  ▱▱▱      0/3   No such image
    panic: the installer's own failure`, frame, "a line for each installation, the failed one unfolded, the installer's error said once")
}

func TestWrap(t *testing.T) {
	t.Parallel()
	line := faint.Render("    error: service web: Bind for 0.0.0.0:8080 failed: port is already allocated")
	var plain []string
	for _, part := range wrap(line, 40) {
		plain = append(plain, ansi.Strip(part))
		assert.LessOrEqual(t, ansi.StringWidth(part), 40)
	}
	assert.Equal(t, []string{
		"    error: service web: Bind for",
		"    0.0.0.0:8080 failed: port is already",
		"    allocated",
	}, plain, "the whole error, its next lines indented as the first")
}

func TestLive_notRun(t *testing.T) {
	t.Parallel()
	live := &live{title: "demo", ended: true}
	nginx, whoami := progress.About(live, "demo/nginx"), progress.About(live, "demo/whoami")
	nginx.Emit(progress.Event{Group: progress.Installation, State: progress.Pending, Message: "install"})
	whoami.Emit(progress.Event{Group: progress.Installation, State: progress.Pending, Message: "install"})
	nginx.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "install"})
	nginx.Emit(progress.Event{Group: progress.Installation, State: progress.Error, Message: "the installer exited with code 1"})
	frame := ansi.Strip(strings.Join(live.render(false), "\n"))
	assert.Equal(t, `demo · 2 installations · 1 failed · 1 not run

✖ nginx   install    the installer exited with code 1
· whoami  install    not run`, frame, "once up stopped, the installations it did not reach say so")
}

func TestShown(t *testing.T) {
	t.Parallel()
	failed := errors.New("demo/iam: install of demo/iam failed, see the installer's output: the installer exited with code 1")
	before := &lines{output: io.Discard}
	assert.Equal(t, failed, shown(failed, &before.mutex, &before.views), "an error the progress did not show is printed")
	after := &lines{output: io.Discard}
	upEvents(after)
	assert.ErrorAs(t, shown(failed, &after.mutex, &after.views), new(shownError), "a failure the progress showed is not printed again")
	assert.NoError(t, shown(nil, &after.mutex, &after.views))
}

func TestLive_outputFirst(t *testing.T) {
	t.Parallel()
	live := &live{title: "demo"}
	nginx := progress.About(live, "demo/nginx")
	nginx.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "install"})
	nginx.Emit(progress.Event{Group: progress.Output, State: progress.Working, Message: "error: service web: port is already allocated"})
	nginx.Emit(progress.Event{Group: "services", Item: "web", State: progress.Error, Message: "port is already allocated"})
	nginx.Emit(progress.Event{Group: progress.Installation, State: progress.Error, Message: "the installer exited with code 1"})
	frame := ansi.Strip(strings.Join(live.render(false), "\n"))
	assert.Equal(t, `demo · 1 installation · 1 failed

✖ nginx  install    the installer exited with code 1
    ✖ web  port is already allocated`, frame, "the service's error said once, whichever of the installer's streams came first")
}

func TestLive_fitsTheTerminal(t *testing.T) {
	t.Parallel()
	live := &live{title: "sntns-dev"}
	for _, name := range []string{"grafana", "traefik", "portal", "runtime"} {
		installation := progress.About(live, "sntns-dev/"+name)
		installation.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "upgrade"})
		installation.Emit(progress.Event{Group: progress.Installation, State: progress.Done, Message: "upgrade"})
	}
	iam := progress.About(live, "sntns-dev/iam")
	iam.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "upgrade"})
	for i := range 20 {
		state := progress.Done
		if i%4 == 0 {
			state = progress.Working
		}
		iam.Emit(progress.Event{Group: "services", Item: fmt.Sprintf("iam-%02d", i), State: state, Current: 1, Total: 1, Message: "running"})
	}
	text := func(lines []string) string { return ansi.Strip(strings.Join(lines, "\n")) }

	assert.Len(t, live.lines(80, 40), 27, "the whole frame fits")
	fitted := live.lines(80, 14)
	assert.LessOrEqual(t, len(fitted), 13, "a line under the terminal's height, for the cursor")
	assert.Contains(t, text(fitted), "iam-04", "a service at work")
	assert.NotContains(t, text(fitted), "iam-01", "a service ready, folded")
	assert.Contains(t, text(fitted), "15/20 services ready", "counted by its installation")

	assert.Contains(t, text(live.lines(80, 8)), "more", "cut at the bottom, saying so")
	live.ended = true
	assert.Len(t, live.lines(80, 14), 27, "the last frame whole")
}

// A network two installations share has a row of its own, which its
// creator's events fill; the installations' rows don't count it.
func TestLive_shared(t *testing.T) {
	t.Parallel()
	live := &live{title: "demo · swarm"}
	stack, alpha, bravo := progress.About(live, "demo"), progress.About(live, "demo/alpha"), progress.About(live, "demo/bravo")
	stack.Emit(progress.Event{Group: "networks", Item: "demo_mesh", State: progress.Pending, Message: "shared by 2 installations"})
	stack.Emit(progress.Event{Group: "networks", Item: "demo_mesh", State: progress.Warning, Message: "bravo declares it otherwise than alpha, which creates it as it declares it"})
	for _, installation := range []progress.Sink{alpha, bravo} {
		installation.Emit(progress.Event{Group: progress.Installation, State: progress.Pending, Message: "install"})
	}
	for _, installation := range []progress.Sink{alpha, bravo} {
		installation.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "install"})
		installation.Emit(progress.Event{Group: "networks", Item: "demo_mesh", State: progress.Done, Message: "created"})
		installation.Emit(progress.Event{Group: "services", Item: "web", State: progress.Done, Message: "running"})
		installation.Emit(progress.Event{Group: progress.Installation, State: progress.Done, Message: "install"})
	}
	stack.Emit(progress.Event{Group: "networks", Item: "demo_mesh", State: progress.Done, Message: "created"})
	frame := durations.ReplaceAllString(ansi.Strip(strings.Join(live.render(false), "\n")), "D")
	assert.Equal(t, `demo · swarm · 2 installations

✔ demo_mesh  network    created · shared by 2 installations
    ! bravo declares it otherwise than alpha, which creates it as it declares it
✔ alpha      install    1 service  D
✔ bravo      install    1 service  D`, frame)

	var output bytes.Buffer
	lines := &lines{output: &output}
	lines.Emit(progress.Event{Installation: "demo", Group: "networks", Item: "demo_mesh", State: progress.Pending, Message: "shared by 2 installations"})
	lines.Emit(progress.Event{Installation: "demo/alpha", Group: "networks", Item: "demo_mesh", State: progress.Done, Message: "created"})
	lines.Emit(progress.Event{Installation: "demo", Group: "networks", Item: "demo_mesh", State: progress.Done, Message: "created"})
	assert.Equal(t, "demo_mesh: network created · shared by 2 installations\n", output.String(), "said once, on its own line")
}

// With several installations at work, each is one line, its services left
// to its meter but for those in trouble; all unfolds them. The durations
// line up.
func TestLive_compact(t *testing.T) {
	t.Parallel()
	events := func(sink progress.Sink) {
		runtime, traefik := progress.About(sink, "demo/runtime"), progress.About(sink, "demo/traefik")
		for _, installation := range []progress.Sink{runtime, traefik} {
			installation.Emit(progress.Event{Group: progress.Installation, State: progress.Working, Message: "install"})
		}
		runtime.Emit(progress.Event{Group: "services", Item: "mongo", State: progress.Working, Current: 0, Total: 1, Message: "restarting: task: non-zero exit (1)"})
		runtime.Emit(progress.Event{Group: "services", Item: "nats", State: progress.Done, Current: 1, Total: 1, Message: "running"})
		traefik.Emit(progress.Event{Group: "services", Item: "gateway", State: progress.Working, Current: 1, Total: 1, Message: "verifying"})
	}
	compact := &live{title: "demo"}
	events(compact)
	frame := durations.ReplaceAllString(ansi.Strip(strings.Join(compact.render(false), "\n")), "D")
	assert.Equal(t, `demo · 2 installations · 2 working

⠋ runtime  install    ▰▰▰▰▰▱▱▱▱▱  1/2 services ready  D
    ◌ mongo  ▱        0/1   restarting: task: non-zero exit (1)
⠋ traefik  install    ▱▱▱▱▱▱▱▱▱▱  0/1 services ready  D`, frame, "one line each, but the service in trouble")
	raw := compact.render(false)
	assert.Contains(t, raw[2], yellow.Render("▰▰▰▰▰"), "a meter yellow while a service is in trouble")
	assert.Contains(t, raw[4], faint.Render("▱▱▱▱▱▱▱▱▱▱"), "its empty cells faint")

	full := &live{title: "demo", all: true}
	events(full)
	frame = durations.ReplaceAllString(ansi.Strip(strings.Join(full.render(false), "\n")), "D")
	assert.Equal(t, `demo · 2 installations · 2 working

⠋ runtime  install    ▰▰▰▰▰▱▱▱▱▱  1/2 services ready  D
    ◌ mongo    ▱        0/1   restarting: task: non-zero exit (1)
    ✔ nats     ▰        1/1   running
⠋ traefik  install    ▱▱▱▱▱▱▱▱▱▱  0/1 services ready  D
    ◌ gateway  ▰        1/1   verifying`, frame, "every service, their names lined up")
}
