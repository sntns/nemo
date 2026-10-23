// Package storage keeps what nemo knows of its installations, as Porter
// keeps it: an Installation, its spec and how its last action went, and
// for each action its Run, the CNAB claim with the bundle it ran, its
// Result and its outputs, documents of cnab-spec 400-claims.md.
//
// A stack is what Porter calls a namespace: installation demo/nginx is the
// bundle nginx in the stack demo.
//
// Nothing secret is stored: credentials appear by their source only, and
// the values of sensitive parameters and outputs are redacted.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/cnabio/cnab-go/valuesource"
	"github.com/cnabio/cnab-to-oci/relocation"
	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/target"
)

// SchemaVersion is the version of the stored documents.
const SchemaVersion = "4"

// ErrNotFound is returned for an installation the store does not know.
var ErrNotFound = errors.New("not found")

// Installation is a bundle installed in a stack, as Porter's: its spec,
// where it is deployed, and how its last action went. The bundle.json
// that action ran is in its run.
type Installation struct {
	SchemaVersion string `json:"schemaVersion"`
	InstallationSpec
	Target    target.Target    `json:"target"`
	Installer target.Installer `json:"installer"`
	// RelocationMap says where the bundle's images are, when it was pulled
	// from a registry: the next actions run them from there.
	RelocationMap relocation.ImageRelocationMap `json:"relocationMap,omitempty"`
	Status        InstallationStatus            `json:"status"`
}

// InstallationSpec is the desired state of an installation, as Porter's:
// which bundle to install in which stack, with which parameters and
// credentials. A stack file lists its installations by their spec.
type InstallationSpec struct {
	// Name is the installation's name, which is its bundle's.
	Name string `json:"name"`
	// Stack is the stack it belongs to, Porter's namespace. A stack file
	// gives its own to its installations.
	Stack  string          `json:"stack,omitempty"`
	Bundle BundleReference `json:"bundle"`
	// Parameters holds the values given, as text. The values of sensitive
	// parameters are never stored: they are given again.
	Parameters map[string]string `json:"parameters,omitempty"`
	// Credentials are the sources of the credentials, the strategies of a
	// CNAB credential set; their values are never stored.
	Credentials []valuesource.Strategy `json:"credentials,omitempty"`
}

// BundleReference is where an installation's bundle is, as Porter's
// OCIReferenceParts: a repository and a version. nemo adds File, a
// bundle.json for a bundle that is not pushed. In a stack file, the
// stack's remote locates a bundle given by its version alone.
type BundleReference struct {
	// Repository is the bundle's repository in a registry, ghcr.io/sntns/whoami.
	Repository string `json:"repository,omitempty"`
	Version    string `json:"version,omitempty"`
	File       string `json:"file,omitempty"`
}

// Validate checks the spec: a file has its own version, a repository or a
// remote needs one.
func (spec InstallationSpec) Validate() error {
	return validation.ValidateStruct(&spec,
		validation.Field(&spec.Name, validation.Required),
		validation.Field(&spec.Bundle),
	)
}

// Validate checks that the reference is a file, or a version and maybe a repository.
func (reference BundleReference) Validate() error {
	fromFile := reference.File != ""
	return validation.ValidateStruct(&reference,
		validation.Field(&reference.Repository, validation.When(fromFile, validation.Empty.Error("give a file or a repository, not both"))),
		validation.Field(&reference.Version, validation.When(fromFile, validation.Empty.Error("a file has its own version")).Else(validation.Required)),
	)
}

// InstallationStatus is how the installation's last action went, as Porter
// sums it up.
type InstallationStatus struct {
	RunID        string     `json:"runId"`
	Action       string     `json:"action"`
	ResultID     string     `json:"resultId"`
	ResultStatus string     `json:"resultStatus"`
	Created      time.Time  `json:"created"`
	Modified     time.Time  `json:"modified"`
	Installed    *time.Time `json:"installed,omitempty"`
	Uninstalled  *time.Time `json:"uninstalled,omitempty"`
	// BundleReference is where the bundle came from: a bundle.json, or a
	// location, where its images are read from when they are loaded.
	BundleReference string `json:"bundleReference,omitempty"`
	BundleVersion   string `json:"bundleVersion"`
	BundleDigest    string `json:"bundleDigest,omitempty"`
}

// RunCustom is what nemo records in a claim's custom data: where the
// action ran, with which credential sources and images.
type RunCustom struct {
	Target        target.Target                 `json:"target"`
	Installer     target.Installer              `json:"installer"`
	Credentials   []valuesource.Strategy        `json:"credentials,omitempty"`
	RelocationMap relocation.ImageRelocationMap `json:"relocationMap,omitempty"`
}

// ID is the installation's CNAB name: <stack>/<bundle>.
func (installation Installation) ID() string {
	return installation.Stack + "/" + installation.Name
}

// IsInstalled reports whether the installation is deployed: installed, and
// not uninstalled since.
func (installation Installation) IsInstalled() bool {
	return installation.Status.Installed != nil && installation.Status.Uninstalled == nil
}

// Store keeps installations and their runs.
type Store interface {
	// GetInstallation returns the installation of the bundle in the stack,
	// or ErrNotFound.
	GetInstallation(ctx context.Context, stack, name string) (Installation, error)
	// ListInstallations returns the installations of the stack, by name.
	ListInstallations(ctx context.Context, stack string) ([]Installation, error)
	// SaveInstallation creates or replaces an installation.
	SaveInstallation(ctx context.Context, installation Installation) error
	// SaveRun records an action of an installation: its claim, then its
	// result and outputs once it ran.
	SaveRun(ctx context.Context, stack, name string, run cnab.Claim) error
	SaveResult(ctx context.Context, stack, name string, result cnab.Result, outputs map[string]string) error
	// GetRun returns the claim of an action of the installation.
	GetRun(ctx context.Context, stack, name, id string) (cnab.Claim, error)
	// ListRuns returns the claims of the installation's actions, oldest first.
	ListRuns(ctx context.Context, stack, name string) ([]cnab.Claim, error)
	// ListResults returns the results of the installation's actions, oldest first.
	ListResults(ctx context.Context, stack, name string) ([]cnab.Result, error)
	// GetOutputs returns the outputs a result recorded, by name.
	GetOutputs(ctx context.Context, stack, name, resultID string) (map[string]string, error)
}
