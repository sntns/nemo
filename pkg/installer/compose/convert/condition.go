package convert

import "github.com/compose-spec/compose-go/v2/types"

// Condition is the state a container must reach before the services depending on it start.
type Condition string

const (
	Started   Condition = "running"
	Healthy   Condition = "healthy"
	Completed Condition = "completed"
)

var strength = map[Condition]int{Started: 0, Healthy: 1, Completed: 2}

// Conditions gives each service the strongest state its dependants ask for. A service nothing depends
// on is waited for until it runs, and healthy when it has a healthcheck, as docker compose up --wait.
func Conditions(project *types.Project) map[string]Condition {
	result := map[string]Condition{}
	for name, service := range project.Services {
		result[name] = Started
		if service.HealthCheck != nil && !service.HealthCheck.Disable {
			result[name] = Healthy
		}
	}
	for _, service := range project.Services {
		for dependencyName, dependency := range service.DependsOn {
			wanted := Started
			switch dependency.Condition {
			case types.ServiceConditionHealthy:
				wanted = Healthy
			case types.ServiceConditionCompletedSuccessfully:
				wanted = Completed
			}
			if current, ok := result[dependencyName]; ok && strength[wanted] > strength[current] {
				result[dependencyName] = wanted
			}
		}
	}
	return result
}
