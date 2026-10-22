package convert

import (
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
)

// restartPolicy takes restart, or deploy.restart_policy when restart is not set, as docker compose does.
func restartPolicy(service types.ServiceConfig) container.RestartPolicy {
	if service.Restart != "" {
		name, count, _ := strings.Cut(service.Restart, ":")
		retries, _ := strconv.Atoi(count)
		return container.RestartPolicy{Name: container.RestartPolicyMode(name), MaximumRetryCount: retries}
	}
	if service.Deploy == nil || service.Deploy.RestartPolicy == nil {
		return container.RestartPolicy{}
	}
	policy := service.Deploy.RestartPolicy
	retries := 0
	if policy.MaxAttempts != nil {
		retries = int(*policy.MaxAttempts)
	}
	switch policy.Condition {
	case "none":
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	case "on-failure":
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure, MaximumRetryCount: retries}
	}
	return container.RestartPolicy{Name: container.RestartPolicyAlways}
}
