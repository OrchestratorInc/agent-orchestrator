package coder

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

var _ sandbox.Diagnoser = (*Client)(nil)

// diagnosticLogLines is how many trailing lines of each Coder log are kept.
const diagnosticLogLines = 40

// stageAttrs identifies the workspace a stage report is about: enough for an
// operator to run `coder show` / `coder logs` against the customer's Coder.
// Never includes the token.
func (c *Client) stageAttrs(workspaceID, agentID string) map[string]string {
	attrs := map[string]string{
		"coder_owner":    c.owner,
		"coder_template": c.templateID,
	}
	if workspaceID != "" {
		attrs["workspace_id"] = workspaceID
	}
	if c.expectedWorkspaceName != "" {
		attrs["workspace_name"] = c.expectedWorkspaceName
	}
	if agentID != "" {
		attrs["agent_id"] = agentID
	}
	if host := c.host(); host != "" {
		attrs["coder_host"] = host
	}
	return attrs
}

func (c *Client) host() string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

type buildLogLine struct {
	CreatedAt time.Time `json:"created_at"`
	Stage     string    `json:"stage"`
	Level     string    `json:"log_level"`
	Output    string    `json:"output"`
}

type agentLogLine struct {
	CreatedAt time.Time `json:"created_at"`
	Level     string    `json:"level"`
	Output    string    `json:"output"`
}

// Diagnose captures what Coder knows about a workspace that failed to start:
// the latest build's status and provisioner error, the agent's lifecycle, and
// the tails of the build log and the agent's startup-script log. Each part is
// best effort; whatever was gathered is returned with the first error.
func (c *Client) Diagnose(ctx context.Context, id sandbox.ID) (sandbox.Diagnostics, error) {
	var view workspace
	if err := c.do(ctx, http.MethodGet, "/api/v2/workspaces/"+url.PathEscape(string(id)), nil, &view); err != nil {
		return sandbox.Diagnostics{}, fmt.Errorf("coder: read workspace: %w", err)
	}
	agent, hasAgent := c.selectAgent(view)
	diagnostics := sandbox.Diagnostics{Summary: c.stageAttrs(view.ID, agent.ID)}
	summary := diagnostics.Summary
	summary["build_status"] = view.LatestBuild.Status
	summary["build_reason"] = view.LatestBuild.Reason
	if view.LatestBuild.Job.Status != "" {
		summary["job_status"] = view.LatestBuild.Job.Status
	}
	if view.LatestBuild.Job.Error != "" {
		summary["job_error"] = view.LatestBuild.Job.Error
	}
	if view.LatestBuild.Job.ErrorCode != "" {
		summary["job_error_code"] = view.LatestBuild.Job.ErrorCode
	}
	if hasAgent {
		summary["agent_status"] = agent.Status
		summary["agent_lifecycle"] = agent.LifecycleState
		summary["agent_architecture"] = agent.Architecture
	} else {
		summary["agent_status"] = "missing"
	}
	// The connection's dev-environment options shape how the worker started,
	// so an operator reading a startup failure sees them alongside the logs.
	summary["worker_user"] = c.workerUserLabel("")
	summary["checkout_reuse"] = c.checkoutReuseLabel()
	if c.readyProbe != "" {
		summary["ready_probe"] = "configured"
		summary["ready_probe_timeout_seconds"] = strconv.Itoa(int(c.readyProbeTimeout / time.Second))
	}

	var firstErr error
	if view.LatestBuild.ID != "" {
		var lines []buildLogLine
		if err := c.do(ctx, http.MethodGet,
			"/api/v2/workspacebuilds/"+url.PathEscape(view.LatestBuild.ID)+"/logs", nil, &lines); err != nil {
			firstErr = fmt.Errorf("coder: read build log: %w", err)
		} else {
			diagnostics.BuildLog = tailBuildLog(lines)
		}
	}
	if hasAgent && agent.ID != "" {
		var lines []agentLogLine
		if err := c.do(ctx, http.MethodGet,
			"/api/v2/workspaceagents/"+url.PathEscape(agent.ID)+"/logs", nil, &lines); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("coder: read agent startup log: %w", err)
			}
		} else {
			diagnostics.AgentLog = tailAgentLog(lines)
		}
	}
	return diagnostics, firstErr
}

func tailBuildLog(lines []buildLogLine) []string {
	start := max(0, len(lines)-diagnosticLogLines)
	tail := make([]string, 0, len(lines)-start)
	for _, line := range lines[start:] {
		text := strings.TrimRight(line.Output, "\r\n")
		if line.Stage != "" {
			text = "[" + line.Stage + "] " + text
		}
		tail = append(tail, text)
	}
	return tail
}

func tailAgentLog(lines []agentLogLine) []string {
	start := max(0, len(lines)-diagnosticLogLines)
	tail := make([]string, 0, len(lines)-start)
	for _, line := range lines[start:] {
		tail = append(tail, strings.TrimRight(line.Output, "\r\n"))
	}
	return tail
}
