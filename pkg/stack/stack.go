// Package stack is the desired state of a stack: the target, the installer,
// and the installations to make there, by their spec: a bundle, its
// parameters and the sources of its credentials. nemo up brings the
// target to it.
package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/cnabio/cnab-go/valuesource"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/sntns/nemo/internal/decode"
	"github.com/sntns/nemo/pkg/credentials"
	"github.com/sntns/nemo/pkg/storage"
	"github.com/sntns/nemo/pkg/target"
)

// Stack is a stack file, the desired state of a stack, Porter's
// namespace: its target, its installer, and its installations.
type Stack struct {
	Name      string           `json:"name"`
	Target    target.Target    `json:"target"`
	Installer target.Installer `json:"installer,omitzero"`
	// Remote says where an installation's bundle is when its reference has
	// a version alone: the template of a location of the
	// installation's name and its version,
	// docker://ghcr.io/sntns/{{ .Component }}:{{ .Version }}, or
	// oci:.artifacts/prod/{{ .Component }}.oci, relative to the stack file.
	Remote string `json:"remote,omitempty"`
	// Records says where nemo keeps the records of the stack's
	// installations: home, the default, with its manifest; or target, in a
	// volume of the target, where whoever reaches the target finds them.
	Records string `json:"records,omitempty"`
	// Parameters go to every installation whose bundle declares a parameter
	// of their name. An installation's own parameters override them.
	Parameters map[string]string `json:"parameters,omitempty"`
	// Credentials are a CNAB credential set for the whole stack: each goes
	// to every installation whose bundle declares it. An installation's own
	// credentials override them.
	Credentials   []valuesource.Strategy     `json:"credentials,omitempty"`
	Installations []storage.InstallationSpec `json:"installations"`
}

// Load reads a stack file, as Read does, and validates it.
func Load(path string) (Stack, error) {
	stack, err := Read(path)
	if err != nil {
		return Stack{}, err
	}
	return stack, stack.Validate()
}

// Read reads a stack file without validating it, to complete it first. Its
// paths, of bundle files and of credentials, are made absolute from the
// stack file's directory, and its installations are given its name as their
// stack.
func Read(path string) (Stack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Stack{}, err
	}
	// The stack shares the JSON names of the target's types.
	var document any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return Stack{}, fmt.Errorf("%s: %w", path, err)
	}
	var stack Stack
	if err := decode.Strict(document, &stack); err != nil {
		return Stack{}, fmt.Errorf("%s: %w", path, err)
	}

	directory, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Stack{}, err
	}
	if layout, ok := strings.CutPrefix(stack.Remote, "oci:"); ok && !filepath.IsAbs(layout) {
		stack.Remote = "oci:" + filepath.Join(directory, layout)
	}
	for i, strategy := range stack.Credentials {
		stack.Credentials[i] = credentials.Absolute(strategy, directory)
	}
	for i, installation := range stack.Installations {
		if file := installation.Bundle.File; file != "" && !filepath.IsAbs(file) {
			stack.Installations[i].Bundle.File = filepath.Join(directory, file)
		}
		for j, strategy := range installation.Credentials {
			stack.Installations[i].Credentials[j] = credentials.Absolute(strategy, directory)
		}
		if installation.Stack == "" {
			stack.Installations[i].Stack = stack.Name
		}
	}
	return stack, nil
}

// Where nemo keeps the records of a stack's installations.
const (
	RecordsHome   = "home"
	RecordsTarget = "target"
)

// stackName is a stack's name: a directory of the store.
var stackName = regexp.MustCompile(`^[^/\\]+$`)

// Validate checks the stack: its installations are its own, and one whose
// bundle has a version alone needs the remote to locate it.
func (stack Stack) Validate() error {
	var byRemote []string
	for _, installation := range stack.Installations {
		if installation.Bundle.File == "" && installation.Bundle.Repository == "" {
			byRemote = append(byRemote, installation.Name)
		}
	}
	noRemote := fmt.Sprintf("where are %s? Give it in the manifest, or with nemo stack import --remote: "+
		"oci:DIRECTORY/{{ .Component }}.oci, or docker://REGISTRY/{{ .Component }}:{{ .Version }}", strings.Join(byRemote, ", "))
	inStack := validation.By(func(value any) error {
		if installation := value.(storage.InstallationSpec); installation.Stack != "" && installation.Stack != stack.Name {
			return fmt.Errorf("%s is in the stack %s", installation.Name, installation.Stack)
		}
		return nil
	})
	if err := validation.ValidateStruct(&stack,
		validation.Field(&stack.Name, validation.Required, validation.Match(stackName).Error(`must be a name without / or \`)),
		validation.Field(&stack.Target),
		validation.Field(&stack.Installer),
		validation.Field(&stack.Remote, validation.When(len(byRemote) > 0, validation.Required.Error(noRemote))),
		validation.Field(&stack.Records, validation.In(RecordsHome, RecordsTarget).Error("must be home or target")),
		validation.Field(&stack.Installations, validation.Required, validation.Each(inStack)),
	); err != nil {
		return fmt.Errorf("stack %s: %w", stack.Name, err)
	}
	return nil
}

// Source is where the installation's bundle is: its file, its repository
// at its version, or the remote's location of its name and version.
func (stack Stack) Source(installation storage.InstallationSpec) (string, error) {
	reference := installation.Bundle
	switch {
	case reference.File != "":
		return reference.File, nil
	case reference.Repository != "":
		return "docker://" + reference.Repository + ":" + reference.Version, nil
	}
	remote, err := template.New("remote").Option("missingkey=error").Parse(stack.Remote)
	if err != nil {
		return "", fmt.Errorf("stack %s: remote: %w", stack.Name, err)
	}
	var source strings.Builder
	if err := remote.Execute(&source, map[string]string{"Component": installation.Name, "Version": reference.Version}); err != nil {
		return "", fmt.Errorf("stack %s: remote: %w", stack.Name, err)
	}
	return source.String(), nil
}
