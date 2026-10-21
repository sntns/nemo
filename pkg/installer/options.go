package installer

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sntns/nemo/internal/decode"
	"github.com/sntns/nemo/pkg/cnab"
	"github.com/sntns/nemo/pkg/target"
)

// DefaultTimeout bounds an action whose bundle sets no nemo-timeout.
const DefaultTimeout = 5 * time.Minute

// Options are the installer's reading of the nemo-* parameters, all of
// them: what the runtime tells it about the target.
type Options struct {
	Orchestrator string
	Timeout      time.Duration
	Purge        bool
	// VolumeTypes are what the target makes the volumes of each type of.
	VolumeTypes map[string]target.VolumeType
	// RegistryAuth holds the credentials the images are pulled with.
	RegistryAuth RegistryAuth
}

// optionsOf reads the nemo-* parameters. The orchestrator is checked
// where it is needed, by Run.
func optionsOf(parameters map[string]any) (Options, error) {
	opts := Options{Timeout: DefaultTimeout}

	var err error
	if opts.Orchestrator, err = parameter[string](parameters, cnab.ParameterOrchestrator); err != nil {
		return opts, err
	}

	timeout, err := parameter[string](parameters, cnab.ParameterTimeout)
	if err != nil {
		return opts, err
	}
	if timeout != "" {
		if opts.Timeout, err = time.ParseDuration(timeout); err != nil {
			return opts, fmt.Errorf("parameter %s: %w", cnab.ParameterTimeout, err)
		}
		if opts.Timeout <= 0 {
			return opts, fmt.Errorf("parameter %s: %s is not a positive duration", cnab.ParameterTimeout, timeout)
		}
	}

	if opts.Purge, err = parameter[bool](parameters, cnab.ParameterPurge); err != nil {
		return opts, err
	}
	if opts.VolumeTypes, err = volumeTypesOf(parameters); err != nil {
		return opts, err
	}
	if opts.RegistryAuth, err = registryAuthOf(parameters); err != nil {
		return opts, err
	}
	return opts, nil
}

// volumeTypesOf reads the nemo-volumes parameter: the volume types of the
// target, given as a JSON object or as its text.
func volumeTypesOf(parameters map[string]any) (map[string]target.VolumeType, error) {
	raw, ok := parameters[cnab.ParameterVolumes]
	if !ok || raw == nil {
		return nil, nil
	}
	// The runtime delivers it as text, which the bundle's definition may not convert.
	if text, isText := raw.(string); isText {
		raw = json.RawMessage(text)
	}
	var volumeTypes map[string]target.VolumeType
	if err := decode.Strict(raw, &volumeTypes); err != nil {
		return nil, fmt.Errorf("parameter %s: %w", cnab.ParameterVolumes, err)
	}
	return volumeTypes, nil
}

// parameter returns a parameter's value, or the zero value when the bundle
// does not declare it.
func parameter[T any](parameters map[string]any, name string) (T, error) {
	var zero T
	raw, ok := parameters[name]
	if !ok || raw == nil {
		return zero, nil
	}
	value, ok := raw.(T)
	if !ok {
		return zero, fmt.Errorf("parameter %s: %v is a %T, not a %T", name, raw, raw, zero)
	}
	return value, nil
}
