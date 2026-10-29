// Package credentials resolves the credentials of an action from their
// sources, the strategies of a CNAB credential set (cnab-spec
// 802-credential-sets.md), as Porter writes them:
//
//	credentials:
//	  - name: token
//	    source: { path: /tmp/token }   # env, path, command or value
//
// Only the source of a credential is ever kept: its value is read when the
// action runs, handed to the runtime, and never written anywhere. A value
// source is recorded without its value: the action that needs it is given
// it again.
package credentials

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	cnabcredentials "github.com/cnabio/cnab-go/credentials"
	"github.com/cnabio/cnab-go/secrets/host"
	"github.com/cnabio/cnab-go/valuesource"

	"github.com/sntns/nemo/pkg/cnab"
)

// Redacted stands for a secret nemo does not record: the value of a value
// source, of a sensitive parameter or of a write-only output.
const Redacted = "*******"

// Recorded returns the strategies as nemo records them, their value sources
// without their value.
func Recorded(strategies []valuesource.Strategy) []valuesource.Strategy {
	if strategies == nil {
		return nil
	}
	recorded := make([]valuesource.Strategy, len(strategies))
	for i, strategy := range strategies {
		strategy.Source = *RecordedSource(&strategy.Source)
		recorded[i] = strategy
	}
	return recorded
}

// RecordedSource returns the source as nemo records it.
func RecordedSource(source *valuesource.Source) *valuesource.Source {
	if source == nil {
		return nil
	}
	recorded := *source
	if recorded.Key == host.SourceValue {
		recorded.Value = Redacted
	}
	return &recorded
}

// IsUnrecorded reports whether the source is a value source as nemo
// records it, without its value.
func IsUnrecorded(source valuesource.Source) bool {
	return source.Key == host.SourceValue && source.Value == Redacted
}

// Parse reads the command-line form of a credential's source, KEY:VALUE,
// where KEY is a source of the specification: env, path, command or value.
func Parse(name, text string) (valuesource.Strategy, error) {
	key, value, ok := strings.Cut(text, ":")
	// command: gh … is written as YAML is: CNAB splits a command on spaces, a leading one included.
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	switch {
	case !ok || value == "":
		return valuesource.Strategy{}, fmt.Errorf("credential %s: %q is not KEY:VALUE", name, text)
	case !slices.Contains([]string{host.SourceEnv, host.SourcePath, host.SourceCommand, host.SourceValue}, key):
		return valuesource.Strategy{}, fmt.Errorf("credential %s: source %s is not env, path, command or value", name, key)
	}
	return valuesource.Strategy{Name: name, Source: valuesource.Source{Key: key, Value: value}}, nil
}

// Absolute returns the strategy with its path made absolute from
// directory, so that it reads the same file from anywhere. A path from a
// variable, $HOME/certs, is left to be expanded where it is read.
func Absolute(strategy valuesource.Strategy, directory string) valuesource.Strategy {
	if strategy.Source.Key == host.SourcePath && !filepath.IsAbs(os.ExpandEnv(strategy.Source.Value)) {
		strategy.Source.Value = filepath.Join(directory, strategy.Source.Value)
	}
	return strategy
}

// Merge returns the strategies of base the bundle still declares, replaced
// by the ones given with the same name.
func Merge(bundle cnab.ExtendedBundle, base, given []valuesource.Strategy) []valuesource.Strategy {
	byName := map[string]valuesource.Strategy{}
	for _, strategy := range base {
		if _, declared := bundle.Credentials[strategy.Name]; declared {
			byName[strategy.Name] = strategy
		}
	}
	for _, strategy := range given {
		byName[strategy.Name] = strategy
	}
	merged := make([]valuesource.Strategy, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		merged = append(merged, byName[name])
	}
	return merged
}

// Resolve reads the credentials that apply to the action, from the host:
// its files, variables and commands. A credential the bundle does not
// declare is an error, as is a required one without a source.
func Resolve(strategies []valuesource.Strategy, bundle cnab.ExtendedBundle, action string) (valuesource.Set, error) {
	set := cnabcredentials.CredentialSet{}
	var errs []error
	for _, strategy := range strategies {
		credential, declared := bundle.Credentials[strategy.Name]
		switch {
		case !declared:
			errs = append(errs, fmt.Errorf("credential %s is not declared by the bundle %s", strategy.Name, bundle.Name))
		case credential.AppliesTo(action) && IsUnrecorded(strategy.Source):
			errs = append(errs, fmt.Errorf("credential %s was given by value, which nemo does not record: give it again, in the manifest or with --credential", strategy.Name))
		case credential.AppliesTo(action):
			set.Credentials = append(set.Credentials, strategy)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	values, err := set.ResolveCredentials(&host.SecretStore{})
	if err != nil {
		return nil, err
	}
	return values, cnabcredentials.Validate(values, bundle.Credentials, action)
}
