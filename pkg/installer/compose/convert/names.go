package convert

import (
	"fmt"
)

// ContainerName is the name of a service's container, as docker compose
// names it: <project>-<service>-<number>.
func ContainerName(stack, service string, number int) string {
	return fmt.Sprintf("%s-%s-%d", stack, service, number)
}
