// Package decode reads the documents nemo is given, YAML or JSON once
// parsed, into its types.
package decode

import (
	"bytes"
	"encoding/json"
)

// Strict decodes a parsed document into value, by its JSON names, and
// refuses the fields value does not have.
func Strict(document any, value any) error {
	data, err := json.Marshal(document)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
