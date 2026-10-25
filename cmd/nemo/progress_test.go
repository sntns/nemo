package main

import (
	"bytes"
	"errors"
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
	frame := durations.ReplaceAllString(ansi.Strip(strings.Join(live.render(), "\n")), "D")
	assert.Equal(t, `demo · 1 installation

⠋ web  uninstall  1/1 services removed  D
    ! volume demo_data kept: still in use
    ✔ web  removed`, frame, "no tasks to count for a service removed, and the warning in sight")
}

func TestLive(t *testing.T) {
	t.Parallel()
	live := &live{title: "demo · swarm"}
	upEvents(live)
	frame := durations.ReplaceAllString(ansi.Strip(strings.Join(live.render(), "\n")), "D")
	assert.Equal(t, `demo · swarm · 2 installations

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
	frame := ansi.Strip(strings.Join(live.render(), "\n"))
	assert.Equal(t, `demo · 2 installations

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
	frame := ansi.Strip(strings.Join(live.render(), "\n"))
	assert.Equal(t, `demo · 1 installation

✖ nginx  install    the installer exited with code 1
    ✖ web  port is already allocated`, frame, "the service's error said once, whichever of the installer's streams came first")
}
