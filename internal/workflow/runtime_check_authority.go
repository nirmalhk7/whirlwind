package workflow

import (
	"path/filepath"
	"whirlwind/internal/execution"
)

func runtimeJobLifecycleMatches(job runtimeCheckJob, lifecycle execution.SandboxLifecycle) bool {
	return runtimeJobExecutionIdentityMatches(job, lifecycle) && runtimeJobAuthorityPresent(job, lifecycle) && runtimeJobPinnedAuthorityMatches(job, lifecycle)

}

func runtimeJobAuthorityPresent(job runtimeCheckJob, lifecycle execution.SandboxLifecycle) bool {
	if filepath.Base(job.LifecycleFile) == "nono.json" {
		return lifecycle.GatewayName == "" && lifecycle.GatewayEndpoint == "" && lifecycle.Workspace == ""
	}
	return lifecycle.GatewayName != "" && lifecycle.GatewayEndpoint != "" && lifecycle.Workspace != ""
}

func runtimeJobExecutionIdentityMatches(job runtimeCheckJob, lifecycle execution.SandboxLifecycle) bool {
	executionMatches := lifecycle.ExecutionID == job.ExecutionID && lifecycle.ExecutionGeneration == job.ExecutionGeneration && lifecycle.ExecutionLabelValue == job.ExecutionLabelValue
	return executionMatches && lifecycle.DeadlineAt == job.DeadlineAt && lifecycle.SandboxName == job.SandboxName
}
func runtimeJobPinnedAuthorityMatches(job runtimeCheckJob, lifecycle execution.SandboxLifecycle) bool {
	gatewayMatches := (job.GatewayName == "" || job.GatewayName == lifecycle.GatewayName) && (job.GatewayEndpoint == "" || job.GatewayEndpoint == lifecycle.GatewayEndpoint)
	resourceMatches := (job.Workspace == "" || job.Workspace == lifecycle.Workspace) && (job.SandboxID == "" || job.SandboxID == lifecycle.SandboxID)
	return gatewayMatches && resourceMatches
}
