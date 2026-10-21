package installer

import (
	"errors"
	"strings"

	"github.com/sntns/nemo/pkg/progress"
)

// Problem is something of the compose project an orchestrator can't honour,
// at its path in the compose file.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (problem Problem) Error() string {
	if problem.Path == "" {
		return problem.Message
	}
	return problem.Path + ": " + problem.Message
}

// Problems collects the problems of a project: errors, which stop the
// action, and warnings, about what an orchestrator ignores. Its methods take
// values, and every Problems derived with At adds to the same lists.
type Problems struct {
	errors   *[]Problem
	warnings *[]Problem
	path     string
}

// NewProblems returns an empty collection.
func NewProblems() Problems {
	return Problems{errors: &[]Problem{}, warnings: &[]Problem{}}
}

// At returns the collection at a path below this one.
func (problem Problems) At(parts ...string) Problems {
	path := make([]string, 0, len(parts)+1)
	if problem.path != "" {
		path = append(path, problem.path)
	}
	for _, part := range parts {
		if part != "" {
			path = append(path, part)
		}
	}
	return Problems{errors: problem.errors, warnings: problem.warnings, path: strings.Join(path, ".")}
}

// AddIf adds an error at this path when the condition holds.
func (problem Problems) AddIf(condition bool, message string) {
	if condition {
		*problem.errors = append(*problem.errors, Problem{Path: problem.path, Message: message})
	}
}

// EmitWarnings sends each warning to events: what the orchestrator ignores
// does not stop the action, but is said.
func (problem Problems) EmitWarnings(events progress.Sink) {
	for _, warning := range *problem.warnings {
		events.Emit(progress.Event{Group: "project", Item: warning.Path, State: progress.Warning, Message: warning.Message})
	}
}

// WarnIf adds a warning at this path when the condition holds.
func (problem Problems) WarnIf(condition bool, message string) {
	if condition {
		*problem.warnings = append(*problem.warnings, Problem{Path: problem.path, Message: message})
	}
}

// Warnings returns the warnings collected so far.
func (problem Problems) Warnings() []Problem {
	return append([]Problem(nil), *problem.warnings...)
}

// Err joins the errors collected so far, or returns nil.
func (problem Problems) Err() error {
	errs := make([]error, len(*problem.errors))
	for i, problem := range *problem.errors {
		errs[i] = problem
	}
	return errors.Join(errs...)
}
