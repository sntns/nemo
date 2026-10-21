package progress

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriter(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	installer := JSON(&output)
	installer.Emit(Event{Group: "services", Item: "web", State: Working, Current: 1, Total: 3, Message: "starting"})

	var events []Event
	writer := NewWriter(SinkFunc(func(event Event) { events = append(events, event) }), "demo/web")
	data := output.String() + "error: no such image\nwithout an end"
	for _, chunk := range []string{data[:10], data[10:]} {
		_, err := writer.Write([]byte(chunk))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	assert.Equal(t, []Event{
		{Installation: "demo/web", Group: "services", Item: "web", State: Working, Current: 1, Total: 3, Message: "starting"},
		{Installation: "demo/web", Group: Output, State: Working, Message: "error: no such image"},
		{Installation: "demo/web", Group: Output, State: Working, Message: "without an end"},
	}, events, "an event split across writes, then the lines that are not events")
}

func TestInstaller(t *testing.T) {
	t.Parallel()
	event := Event{Group: "services", Item: "web", State: Warning, Message: "restarting"}

	var stdout, stderr bytes.Buffer
	events, failure := Installer("", &stdout, &stderr)
	events.Emit(event)
	assert.Equal(t, "services: web (warning) restarting\n", stdout.String())
	assert.Same(t, &stderr, failure, "for whoever runs it without nemo, the error goes to stderr")

	stdout.Reset()
	events, failure = Installer(FormatJSON, &stdout, &stderr)
	events.Emit(event)
	assert.JSONEq(t, `{"group":"services","item":"web","state":"warning","message":"restarting"}`, stdout.String())
	assert.Same(t, &stdout, failure, "for nemo, the error follows the events")
}
