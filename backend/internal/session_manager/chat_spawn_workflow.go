package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Env vars the agent's `ao mcp` subprocess reads to act as its workflow attempt.
//
// They are set only for a workflow-originated session, and only in the MCP server's
// own environment (ports.ChatMCPServerConfig.Env). That is a deliberately narrow
// channel: the same subprocess already receives AO_SESSION_ID and
// AO_BROWSER_CAPABILITY, so carrying the attempt identity here creates no privilege
// the agent process did not already hold. None of these values is ever written to
// the prompt, and the CLI never echoes them in a tool result.
const (
	// EnvWorkflowID names the workflow this attempt belongs to.
	EnvWorkflowID = "AO_WORKFLOW_ID"
	// EnvWorkflowTaskID names the task this attempt is executing.
	EnvWorkflowTaskID = "AO_TASK_ID"
	// EnvWorkflowAttemptID 是服务端与 Session 持久绑定核对的非秘密标识。
	EnvWorkflowAttemptID = "AO_ATTEMPT_ID"
	// EnvWorkflowLease 仅用于清除旧环境残留，不能写入新进程环境。
	EnvWorkflowLease = "AO_WORKFLOW_LEASE" //nolint:gosec // Environment variable name, not a credential.
)

// applyWorkflowMCPEnvToServers sets the session capability on every MCP server env
// this launch will hand to a provider. It exists because the capability is minted
// after the server list is built, and the list is what the driver reads.
func applyWorkflowMCPEnvToServers(servers []ports.ChatMCPServerConfig, capability string) {
	for i := range servers {
		deleteProtectedEnv(servers[i].Env, EnvWorkflowLease, envKeysCaseInsensitive)
		if capability == "" {
			continue
		}
		if servers[i].Env == nil {
			servers[i].Env = make(map[string]string, 1)
		}
		servers[i].Env[EnvBrowserCapability] = capability
	}
}

// applyWorkflowMCPEnv 清除旧租约，只向 MCP 传递非秘密身份与 Session capability。
//
// capability is the session capability from the prepared launch environment. It is
// passed in rather than read from a global because the token is minted per launch:
// the caller must hand over the one that will actually be in the agent's process.
func applyWorkflowMCPEnv(serverEnv map[string]string, workflow *ports.WorkflowSpawnContext, capability string) {
	deleteProtectedEnv(serverEnv, EnvWorkflowLease, envKeysCaseInsensitive)
	if workflow == nil {
		return
	}
	if value := string(workflow.WorkflowID); value != "" {
		serverEnv[EnvWorkflowID] = value
	}
	if value := string(workflow.TaskID); value != "" {
		serverEnv[EnvWorkflowTaskID] = value
	}
	if value := string(workflow.AttemptID); value != "" {
		serverEnv[EnvWorkflowAttemptID] = value
	}
	if capability != "" {
		serverEnv[EnvBrowserCapability] = capability
	}
}
