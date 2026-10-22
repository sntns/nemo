package docker

import (
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/moby/moby/api/types/container"
)

// Healthcheck converts the healthcheck of a service.
func Healthcheck(check *types.HealthCheckConfig) *container.HealthConfig {
	switch {
	case check == nil:
		return nil
	case check.Disable:
		return &container.HealthConfig{Test: []string{"NONE"}}
	}
	config := &container.HealthConfig{Test: check.Test}
	if check.Timeout != nil {
		config.Timeout = time.Duration(*check.Timeout)
	}
	if check.Interval != nil {
		config.Interval = time.Duration(*check.Interval)
	}
	if check.StartPeriod != nil {
		config.StartPeriod = time.Duration(*check.StartPeriod)
	}
	if check.Retries != nil {
		config.Retries = int(*check.Retries)
	}
	return config
}
