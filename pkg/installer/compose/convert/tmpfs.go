package convert

import (
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

func tmpfs(entries types.StringList) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	result := map[string]string{}
	for _, entry := range entries {
		target, options, _ := strings.Cut(strings.TrimSpace(entry), ":")
		result[target] = options
	}
	return result
}
