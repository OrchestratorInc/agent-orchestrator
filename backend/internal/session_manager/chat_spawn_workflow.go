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
	// EnvWorkflowAttemptID names the attempt the agent acts as. It is the identity
	// the daemon checks the capability and lease against.
	EnvWorkflowAttemptID = "AO_ATTEMPT_ID"
	// EnvWorkflowLease carries the attempt's lease token. It is a secret: never
	// logged, never serialized, never shown in a tool result.
	EnvWorkflowLease = "AO_WORKFLOW_LEASE" //nolint:gosec // Environment variable name, not a credential.
)

// applyWorkflowMCPEnvToServers sets the session capability on every MCP server env
// this launch will hand to a provider. It exists because the capability is minted
// after the server list is built, and the list is what the driver reads.
func applyWorkflowMCPEnvToServers(servers []ports.ChatMCPServerConfig, capability string) {
	if capability == "" {
		return
	}
	for i := range servers {
		if servers[i].Env == nil {
			servers[i].Env = make(map[string]string, 1)
		}
		servers[i].Env[EnvBrowserCapability] = capability
	}
}

// applyWorkflowMCPEnv adds the attempt identity and lease to one MCP server env.
//
// capability is the session capability from the prepared launch environment. It is
// passed in rather than read from a global because the token is minted per launch:
// the caller must hand over the one that will actually be in the agent's process.
func applyWorkflowMCPEnv(serverEnv map[string]string, workflow *ports.WorkflowSpawnContext, capability string) {
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
	if workflow.LeaseToken != "" {
		serverEnv[EnvWorkflowLease] = workflow.LeaseToken
	}
	if capability != "" {
		serverEnv[EnvBrowserCapability] = capability
	}
}
