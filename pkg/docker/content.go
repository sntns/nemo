package docker

import "github.com/compose-spec/compose-go/v2/types"

// Content returns the content of a config or a secret, or nil for an
// external one, which is only referenced. installer.LoadProject has read
// every other one into its Content.
func Content(object types.FileObjectConfig) []byte {
	if bool(object.External) {
		return nil
	}
	return []byte(object.Content)
}
