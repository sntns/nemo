package swarm

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/sntns/nemo/pkg/installer"
	"github.com/sntns/nemo/pkg/installer/swarm/convert"
)

// validate reports what of the project Swarm can't honour, as errors, and
// what it ignores, as warnings.
func validate(project *types.Project) installer.Problems {
	problems := installer.NewProblems()
	validateServices(problems, project)
	validateNetworks(problems, project)
	validateConfigs(problems, project)
	validateSecrets(problems, project)
	return problems
}

func validateFileObjects[T any](problems installer.Problems, kind string, objects map[string]T, convert func(T) types.FileObjectConfig) {
	for _, name := range slices.Sorted(maps.Keys(objects)) {
		object := convert(objects[name])
		resource := problems.At(kind, name)

		resource.At("driver").AddIf(
			object.Driver != "" || len(object.DriverOpts) > 0,
			unsupported,
		)

		resource.At("template_driver").AddIf(
			object.TemplateDriver != "",
			unsupported,
		)

		resource.At("name").AddIf(
			!bool(object.External) && len(object.Name)+15 > 64,
			fmt.Sprintf(
				"%s takes %d characters with its content hash: Swarm allows 64, shorten the stack name",
				object.Name,
				len(object.Name)+15,
			),
		)
	}
}

const (
	useResources           = "not supported by Swarm: use deploy.resources"
	useResourcesLimitsPids = "not supported by Swarm: use deploy.resources.limits.pids"
	useRestartPolicy       = "not supported by Swarm: use deploy.restart_policy"
	useLogging             = "not supported by Swarm: use logging"
	unsupported            = "not supported by Swarm"
)

func validateServices(problems installer.Problems, project *types.Project) {
	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		serviceConfig := project.Services[name]
		serviceProblems := problems.At("services", name)

		validateServiceOptions(serviceProblems, serviceConfig)
		validatePorts(serviceProblems, serviceConfig)
		validateVolumes(serviceProblems, serviceConfig)
		validateServiceNetworks(serviceProblems, serviceConfig)
		validateDeploy(serviceProblems, serviceConfig)
		validateServiceName(serviceProblems, project, name)
	}
}

func validateServiceOptions(problems installer.Problems, service types.ServiceConfig) {
	problems.At("restart").AddIf(service.Restart != "", useRestartPolicy)

	problems.At("container_name").AddIf(
		service.ContainerName != "",
		"not supported by Swarm: tasks are named by Swarm",
	)

	problems.At("links").AddIf(len(service.Links) > 0, unsupported)
	problems.At("external_links").AddIf(len(service.ExternalLinks) > 0, unsupported)

	problems.At("network_mode").AddIf(
		service.Net != "" || service.NetworkMode != "",
		unsupported,
	)

	problems.At("annotations").AddIf(len(service.Annotations) > 0, unsupported)
	problems.At("blkio_config").AddIf(service.BlkioConfig != nil, unsupported)
	problems.At("cgroup_parent").AddIf(service.CgroupParent != "", unsupported)
	problems.At("cgroup").AddIf(service.Cgroup != "", unsupported)

	problems.At("cpu_count").AddIf(service.CPUCount != 0, unsupported)
	problems.At("cpu_percent").AddIf(service.CPUPercent != 0, unsupported)
	problems.At("cpu_period").AddIf(service.CPUPeriod != 0, unsupported)
	problems.At("cpu_quota").AddIf(service.CPUQuota != 0, unsupported)
	problems.At("cpu_rt_period").AddIf(service.CPURTPeriod != 0, unsupported)
	problems.At("cpu_rt_runtime").AddIf(service.CPURTRuntime != 0, unsupported)
	problems.At("cpu_shares").AddIf(service.CPUShares != 0, unsupported)
	problems.At("cpuset").AddIf(service.CPUSet != "", unsupported)

	problems.At("cpus").AddIf(service.CPUS != 0, useResources)
	problems.At("mem_limit").AddIf(service.MemLimit != 0, useResources)
	problems.At("mem_reservation").AddIf(service.MemReservation != 0, useResources)
	problems.At("memswap_limit").AddIf(service.MemSwapLimit != 0, useResources)
	problems.At("mem_swappiness").AddIf(service.MemSwappiness != 0, useResources)

	problems.At("pids_limit").AddIf(service.PidsLimit != 0, useResourcesLimitsPids)

	problems.At("credential_spec").AddIf(service.CredentialSpec != nil, unsupported)
	problems.At("device_cgroup_rules").AddIf(len(service.DeviceCgroupRules) > 0, unsupported)
	problems.At("devices").AddIf(len(service.Devices) > 0, unsupported)
	problems.At("domainname").AddIf(service.DomainName != "", unsupported)
	problems.At("gpus").AddIf(len(service.Gpus) > 0, unsupported)
	problems.At("ipc").AddIf(service.Ipc != "", unsupported)
	problems.At("pid").AddIf(service.Pid != "", unsupported)
	problems.At("uts").AddIf(service.Uts != "", unsupported)
	problems.At("userns_mode").AddIf(service.UserNSMode != "", unsupported)

	problems.At("log_driver").AddIf(service.LogDriver != "", useLogging)
	problems.At("log_opt").AddIf(len(service.LogOpt) > 0, useLogging)

	problems.At("mac_address").AddIf(service.MacAddress != "", unsupported)
	problems.At("oom_kill_disable").AddIf(service.OomKillDisable, unsupported)
	problems.At("oom_score_adj").AddIf(service.OomScoreAdj != 0, unsupported)
	problems.At("privileged").AddIf(service.Privileged, unsupported)

	problems.At("pull_policy").AddIf(
		!slices.Contains([]string{
			"",
			types.PullPolicyAlways,
			types.PullPolicyMissing,
			types.PullPolicyIfNotPresent,
		}, service.PullPolicy),
		"not supported by Swarm: nodes pull images themselves",
	)

	problems.At("pull_refresh_after").AddIf(service.PullRefreshAfter != "", unsupported)
	problems.At("runtime").AddIf(service.Runtime != "", unsupported)
	problems.At("security_opt").AddIf(len(service.SecurityOpt) > 0, unsupported)
	problems.At("shm_size").AddIf(service.ShmSize != 0, unsupported)
	problems.At("storage_opt").AddIf(len(service.StorageOpt) > 0, unsupported)
	problems.At("use_api_socket").AddIf(service.UseAPISocket, unsupported)
	problems.At("volume_driver").AddIf(service.VolumeDriver != "", unsupported)
	problems.At("volumes_from").AddIf(len(service.VolumesFrom) > 0, unsupported)

	problems.At("healthcheck.start_interval").AddIf(
		service.HealthCheck != nil && service.HealthCheck.StartInterval != nil,
		unsupported,
	)

	for i, tmpfs := range service.Tmpfs {
		problems.At(fmt.Sprintf("tmpfs[%d]", i)).AddIf(
			strings.Contains(tmpfs, ":"),
			"options are not supported by Swarm: use a tmpfs volume",
		)
	}
}

func validatePorts(problems installer.Problems, service types.ServiceConfig) {
	for i, port := range service.Ports {
		path := fmt.Sprintf("ports[%d]", i)

		problems.At(path, "host_ip").AddIf(port.HostIP != "", unsupported)
		problems.At(path, "app_protocol").AddIf(port.AppProtocol != "", unsupported)
	}
}

func validateVolumes(problems installer.Problems, service types.ServiceConfig) {
	for i, volume := range service.Volumes {
		path := fmt.Sprintf("volumes[%d]", i)

		problems.At(path, "type").AddIf(
			volume.Type == types.VolumeTypeImage ||
				volume.Type == types.VolumeTypeCluster,
			unsupported,
		)

		problems.At(path, "bind").AddIf(
			volume.Bind != nil &&
				(volume.Bind.SELinux != "" || volume.Bind.Recursive != ""),
			"selinux and recursive are not supported by Swarm",
		)
	}
}

func validateServiceNetworks(problems installer.Problems, service types.ServiceConfig) {
	for _, name := range slices.Sorted(maps.Keys(service.Networks)) {
		config := service.Networks[name]

		problems.At("networks", name).AddIf(
			config != nil &&
				(config.Ipv4Address != "" ||
					config.Ipv6Address != "" ||
					len(config.LinkLocalIPs) > 0 ||
					config.MacAddress != "" ||
					config.InterfaceName != "" ||
					config.GatewayPriority != 0 ||
					config.Priority != 0),
			"only aliases and driver_opts are supported by Swarm",
		)
	}
}

func validateDeploy(problems installer.Problems, service types.ServiceConfig) {
	if service.Deploy == nil {
		return
	}

	deployConfig := service.Deploy
	deployProblems := problems.At("deploy")

	deployProblems.At("resources.reservations.devices").AddIf(
		deployConfig.Resources.Reservations != nil &&
			len(deployConfig.Resources.Reservations.Devices) > 0,
		unsupported,
	)

	deployProblems.At("resources.limits").AddIf(
		deployConfig.Resources.Limits != nil &&
			(len(deployConfig.Resources.Limits.Devices) > 0 ||
				len(deployConfig.Resources.Limits.GenericResources) > 0),
		"only cpus, memory and pids are supported by Swarm",
	)
}

func validateServiceName(problems installer.Problems, project *types.Project, name string) {
	service := convert.NewNamespace(project.Name).Scope(name)

	problems.At("name").AddIf(
		len(service) > 63,
		fmt.Sprintf(
			"the service is named %s, %d characters: Swarm allows 63, shorten the stack name",
			service,
			len(service),
		),
	)
}

func validateNetworks(problems installer.Problems, project *types.Project) {
	for _, name := range slices.Sorted(maps.Keys(project.Networks)) {
		network := project.Networks[name]
		networkProblems := problems.At("networks", name)

		networkProblems.At("enable_ipv4").AddIf(network.EnableIPv4 != nil, unsupported)
		networkProblems.At("enable_ipv6").AddIf(network.EnableIPv6 != nil, unsupported)

		for i, pool := range network.Ipam.Config {
			networkProblems.At(fmt.Sprintf("ipam.config[%d].aux_addresses", i)).AddIf(
				pool != nil && len(pool.AuxiliaryAddresses) > 0,
				unsupported,
			)
		}
	}
}

func validateConfigs(problems installer.Problems, project *types.Project) {
	validateFileObjects(
		problems,
		"configs",
		project.Configs,
		func(config types.ConfigObjConfig) types.FileObjectConfig {
			return types.FileObjectConfig(config)
		},
	)
}

func validateSecrets(problems installer.Problems, project *types.Project) {
	validateFileObjects(
		problems,
		"secrets",
		project.Secrets,
		func(secret types.SecretConfig) types.FileObjectConfig {
			return types.FileObjectConfig(secret)
		},
	)
}
