package installer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/sntns/nemo/internal/decode"
	"github.com/sntns/nemo/pkg/cnab"
)

// CredentialExtension is the compose extension by which a secret read from
// a credential says when the credential is required. The bundle declares
// the credential optional, as CNAB has no condition; gill requires it when
// the parameters say so.
const CredentialExtension = "x-nemo-credential"

// CredentialClaim is what a secret says of its credential in its
// x-nemo-credential.
type CredentialClaim struct {
	// RequiredIf requires the credential when every parameter it names has
	// the value it gives.
	RequiredIf map[string]string `json:"required-if"`
}

// CredentialClaimOf reads the x-nemo-credential of a secret, nil when it
// has none.
func CredentialClaimOf(extensions types.Extensions) (*CredentialClaim, error) {
	raw, declared := extensions[CredentialExtension]
	if !declared {
		return nil, nil
	}
	var claim CredentialClaim
	if err := decode.Strict(raw, &claim); err != nil {
		return nil, fmt.Errorf("%s: %w", CredentialExtension, err)
	}
	if len(claim.RequiredIf) == 0 {
		return nil, fmt.Errorf("%s: required-if names no parameter", CredentialExtension)
	}
	return &claim, nil
}

// requires reports whether the parameters, by name in their string form,
// require the credential.
func (claim CredentialClaim) requires(parameters map[string]string) bool {
	for name, value := range claim.RequiredIf {
		if parameters[name] != value {
			return false
		}
	}
	return true
}

func (claim CredentialClaim) String() string {
	var conditions []string
	for _, name := range slices.Sorted(maps.Keys(claim.RequiredIf)) {
		conditions = append(conditions, name+" is "+claim.RequiredIf[name])
	}
	return strings.Join(conditions, " and ")
}

// withoutOptionalSecrets leaves out the secrets read from a credential the
// runtime did not deliver and the parameters do not require: the services
// do without them. A secret whose credential they require is an error.
func (operation Operation) withoutOptionalSecrets(compose *types.Project, delivered types.Mapping) error {
	parameters := map[string]string{}
	for name, value := range operation.Parameters {
		text, err := cnab.WriteParameterToString(name, value)
		if err != nil {
			return err
		}
		parameters[name] = text
	}
	for _, key := range slices.Sorted(maps.Keys(compose.Secrets)) {
		secret := compose.Secrets[key]
		if _, given := delivered[secret.Environment]; secret.Environment == "" || given {
			continue
		}
		claim, err := CredentialClaimOf(secret.Extensions)
		if err != nil {
			return fmt.Errorf("secrets.%s: %w", key, err)
		}
		if claim == nil {
			continue
		}
		if claim.requires(parameters) {
			return fmt.Errorf("secrets.%s: the credential %s is required when %s", key, operation.credentialOf(secret.Environment), claim)
		}
		delete(compose.Secrets, key)
		for name, service := range compose.Services {
			service.Secrets = slices.DeleteFunc(service.Secrets, func(reference types.ServiceSecretConfig) bool { return reference.Source == key })
			compose.Services[name] = service
		}
	}
	return nil
}

// credentialOf is the name of the credential delivered in the variable.
func (operation Operation) credentialOf(variable string) string {
	for name, credential := range operation.Bundle.Credentials {
		if credential.EnvironmentVariable == variable {
			return name
		}
	}
	return variable
}
