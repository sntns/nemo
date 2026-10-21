// Package progress is how long operations report what they do. The code that
// does the work emits events and never prints; whoever drives it renders them.
//
// The installer runs in a container of its own: it writes its events as
// JSON lines, and the runtime reads them back from its output with Writer.
package progress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// State is where an item stands.
type State string

const (
	Pending State = "pending"
	Working State = "working"
	Done    State = "done"
	Warning State = "warning"
	Error   State = "error"
)

// Variable is the environment variable by which the runtime asks the
// installer for its events as JSON lines, setting it to FormatJSON.
const (
	Variable   = "NEMO_PROGRESS"
	FormatJSON = "json"
)

// Groups of the events about something else than the objects an
// orchestrator creates.
const (
	// Installation is an action on an installation, its Message the action.
	Installation = "installation"
	// Images is the loading of the images into the target's daemon.
	Images = "images"
	// Output is a line the installer printed that is not an event.
	Output = "output"
)

// Event is one step of an operation, about one item of a group: a network,
// a volume, a service. Readers ignore the fields they do not know, so fields
// are only ever added.
type Event struct {
	// Installation is the installation the event is about, stack/name: the
	// runtime sets it on the events of the installer it runs.
	Installation string `json:"installation,omitempty"`
	Group        string `json:"group"`
	Item         string `json:"item,omitempty"`
	State        State  `json:"state"`
	Current      int64  `json:"current,omitempty"`
	Total        int64  `json:"total,omitempty"`
	Message      string `json:"message,omitempty"`
}

// Sink receives events.
type Sink interface {
	Emit(Event)
}

// SinkFunc is a Sink made of a function.
type SinkFunc func(Event)

func (sink SinkFunc) Emit(event Event) { sink(event) }

// Discard drops every event.
var Discard Sink = SinkFunc(func(Event) {})

// Installer returns where an installer writes, as format, Variable's value,
// asks: the sink of its events and the writer of the error that ends it.
// For FormatJSON both are stdout, so that Writer reads the error after the
// events, in order; otherwise the events are text on stdout and the error
// goes to stderr.
func Installer(format string, stdout, stderr io.Writer) (Sink, io.Writer) {
	if format == FormatJSON {
		return JSON(stdout), stdout
	}
	return Text(stdout), stderr
}

// Text writes each event as a line of text, for whoever runs the installer
// without nemo's runtime, Porter for one.
func Text(output io.Writer) Sink {
	return SinkFunc(func(event Event) {
		line := event.Group + ": " + event.Item
		if event.State == Warning || event.State == Error {
			line += " (" + string(event.State) + ")"
		}
		if event.Message != "" {
			line += " " + event.Message
		}
		fmt.Fprintln(output, line)
	})
}

// JSON writes each event as a line of JSON, for Writer to read back.
func JSON(output io.Writer) Sink {
	var mutex sync.Mutex
	encoder := json.NewEncoder(output)
	return SinkFunc(func(event Event) {
		mutex.Lock()
		defer mutex.Unlock()
		_ = encoder.Encode(event)
	})
}

// About returns the sink that sets the installation of each event.
func About(sink Sink, installation string) Sink {
	return SinkFunc(func(event Event) {
		event.Installation = installation
		sink.Emit(event)
	})
}

// Writer reads what an installer prints, its events as JSON lines, and
// emits them about its installation; any other line is an Output event.
type Writer struct {
	sink   Sink
	mutex  sync.Mutex
	buffer []byte
}

// NewWriter returns the writer of the installation's output.
func NewWriter(sink Sink, installation string) *Writer {
	return &Writer{sink: About(sink, installation)}
}

func (writer *Writer) Write(data []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.buffer = append(writer.buffer, data...)
	for {
		end := bytes.IndexByte(writer.buffer, '\n')
		if end < 0 {
			return len(data), nil
		}
		writer.line(string(writer.buffer[:end]))
		writer.buffer = writer.buffer[end+1:]
	}
}

// Close emits the last line, when it did not end.
func (writer *Writer) Close() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if len(writer.buffer) > 0 {
		writer.line(string(writer.buffer))
		writer.buffer = nil
	}
	return nil
}

func (writer *Writer) line(line string) {
	line = strings.TrimRight(line, "\r")
	var event Event
	if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &event) == nil && event.Group != "" {
		writer.sink.Emit(event)
		return
	}
	if strings.TrimSpace(line) != "" {
		writer.sink.Emit(Event{Group: Output, State: Working, Message: line})
	}
}
