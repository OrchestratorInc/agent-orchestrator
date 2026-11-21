package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// `ao task` is the scripted way for another tool or coding agent to hand
// Orchestrator a task and learn, from the exit code alone, whether it really
// started. It is built only on existing daemon routes and leaves `ao spawn`,
// `ao send` and the session states unchanged.
//
// Exit codes: 0 the task started (running or done), 1 it failed or timed out,
// 2 it is waiting on the user. Usage errors also exit 2 but print no JSON.

// Task statuses reported in the `status` field of --json output.
const (
	taskStatusStarting   = "starting"
	taskStatusRunning    = "running"
	taskStatusDone       = "done"
	taskStatusFailed     = "failed"
	taskStatusNeedsInput = "needs_input"
)

// Exit codes for task statuses that are not success.
const (
	taskExitFailed     = 1
	taskExitNeedsInput = 2
)

const defaultTaskTimeout = 120 * time.Second

// taskPollInterval paces status reads while waiting.
var taskPollInterval = time.Second

// taskRunningSettle is how long a turn must stay running before `wait` calls
// the task started, so an agent that fails right after picking the task up
// (a rejected login, a missing key) still reports the failure.
var taskRunningSettle = 5 * time.Second

// taskResult is the stable --json shape. Every field is always present.
type taskResult struct {
	Status     string   `json:"status"`
	TaskID     string   `json:"taskId"`
	SessionIDs []string `json:"sessionIds"`
	Reason     string   `json:"reason"`
	Fix        string   `json:"fix"`
}

// taskExitError carries a task outcome's exit code to the process entrypoint.
type taskExitError struct {
	code int
	msg  string
}

func (e taskExitError) Error() string { return e.msg }
func (e taskExitError) ExitCode() int { return e.code }

type taskStartOptions struct {
	project string
	agent   string
	base    string
	worker  bool
	wait    bool
	timeout time.Duration
	json    bool
}

type taskWaitOptions struct {
	timeout time.Duration
	json    bool
}

func newTaskCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Start a task and check whether it really started",
		Long: "Start a task on a project and check whether the agent really picked it up.\n\n" +
			"By default the project's orchestrator receives the task, plans it, and starts workers.\n" +
			"With --worker a single worker session takes it directly.\n\n" +
			"Exit codes: 0 started (running or done), 1 failed or timed out, 2 waiting on the user.\n" +
			"With --json, stdout is {status, taskId, sessionIds, reason, fix}; status is one of\n" +
			"starting, running, done, failed, needs_input.",
	}
	cmd.AddCommand(newTaskStartCommand(ctx))
	cmd.AddCommand(newTaskStatusCommand(ctx))
	cmd.AddCommand(newTaskWaitCommand(ctx))
	return cmd
}

func newTaskStartCommand(ctx *commandContext) *cobra.Command {
	opts := taskStartOptions{timeout: defaultTaskTimeout}
	cmd := &cobra.Command{
		Use:   "start <brief>",
		Short: "Send a task to the project's orchestrator (or one worker with --worker)",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			brief := strings.TrimSpace(args[0])
			if brief == "" {
				return usageError{fmt.Errorf("task brief must not be empty")}
			}
			if opts.timeout <= 0 {
				return usageError{fmt.Errorf("--timeout must be positive")}
			}
			res := ctx.startTask(cmd.Context(), brief, opts)
			if opts.wait && (res.Status == taskStatusStarting || res.Status == taskStatusRunning) {
				res = ctx.waitTask(cmd.Context(), res, opts.timeout)
			}
			return writeTaskResult(cmd, res, opts.json)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.project, "project", "", "Project id (default: AO_PROJECT_ID or the registered project that contains the current directory)")
	f.BoolVar(&opts.worker, "worker", false, "Start one worker session on the task instead of sending it to the orchestrator")
	f.StringVar(&opts.agent, "agent", "", "Agent for the worker(s) (default: the project's worker agent)")
	f.StringVar(&opts.base, "base", "", "Base branch for the worker(s) (default: the project's default branch)")
	f.BoolVar(&opts.wait, "wait", false, "Wait until the task has started, failed, or needs the user")
	f.DurationVar(&opts.timeout, "timeout", defaultTaskTimeout, "How long --wait waits before failing")
	f.BoolVar(&opts.json, "json", false, "Print the result as JSON")
	return cmd
}

func newTaskStatusCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status <task-id>",
		Short: "Report a task's status once",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			return writeTaskResult(cmd, ctx.taskStatus(cmd.Context(), id), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the result as JSON")
	return cmd
}

func newTaskWaitCommand(ctx *commandContext) *cobra.Command {
	opts := taskWaitOptions{timeout: defaultTaskTimeout}
	cmd := &cobra.Command{
		Use:   "wait <task-id>",
		Short: "Wait until a task has started, failed, or needs the user",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			if opts.timeout <= 0 {
				return usageError{fmt.Errorf("--timeout must be positive")}
			}
			res := ctx.waitTask(cmd.Context(), taskResult{TaskID: id.String()}, opts.timeout)
			return writeTaskResult(cmd, res, opts.json)
		},
	}
	cmd.Flags().DurationVar(&opts.timeout, "timeout", defaultTaskTimeout, "How long to wait before failing")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Print the result as JSON")
	return cmd
}

// taskID names the session that runs a task and, for a task sent to a
// running session (the orchestrator), the conversation turn that carries it.
// It prints as "<session>" or "<session>:<turn>".
type taskID struct {
	session string
	turn    string
}

func (id taskID) String() string {
	if id.turn == "" {
		return id.session
	}
	return id.session + ":" + id.turn
}

func parseTaskID(raw string) (taskID, error) {
	raw = strings.TrimSpace(raw)
	session, turn, hasTurn := strings.Cut(raw, ":")
	if !sessionIDPattern.MatchString(session) || hasTurn && strings.TrimSpace(turn) == "" {
		return taskID{}, usageError{fmt.Errorf("invalid task id %q: expected <session-id> or <session-id>:<turn-id> as printed by `ao task start`", raw)}
	}
	return taskID{session: session, turn: turn}, nil
}

func writeTaskResult(cmd *cobra.Command, res taskResult, asJSON bool) error {
	if res.SessionIDs == nil {
		res.SessionIDs = []string{}
	}
	out := cmd.OutOrStdout()
	if asJSON {
		if err := writeJSON(out, res); err != nil {
			return err
		}
	} else {
		line := fmt.Sprintf("task %s: %s", res.TaskID, res.Status)
		if res.TaskID == "" {
			line = "task: " + res.Status
		}
		if res.Reason != "" {
			line += " (" + res.Reason + ")"
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
		if len(res.SessionIDs) > 0 {
			if _, err := fmt.Fprintf(out, "  sessions: %s\n", strings.Join(res.SessionIDs, ", ")); err != nil {
				return err
			}
		}
		if res.Fix != "" {
			if _, err := fmt.Fprintf(out, "  fix: %s\n", res.Fix); err != nil {
				return err
			}
		}
	}
	msg := res.Reason
	if res.Fix != "" {
		msg += "; " + res.Fix
	}
	switch res.Status {
	case taskStatusFailed:
		return taskExitError{code: taskExitFailed, msg: "task failed: " + msg}
	case taskStatusNeedsInput:
		return taskExitError{code: taskExitNeedsInput, msg: "task is waiting on you: " + msg}
	}
	return nil
}

func failedTask(id string, sessions []string, reason, fix string) taskResult {
	return taskResult{Status: taskStatusFailed, TaskID: id, SessionIDs: sessions, Reason: reason, Fix: fix}
}

// startTask creates the task and returns its first status. Every failure is
// reported as a failed result so --json callers always get the same shape.
func (c *commandContext) startTask(ctx context.Context, brief string, opts taskStartOptions) taskResult {
	project, err := c.resolveTaskProject(ctx, opts.project)
	if err != nil {
		return failedTask("", nil, err.Error(), "")
	}
	workerAgent := strings.TrimSpace(opts.agent)
	if workerAgent == "" && project.Config != nil {
		workerAgent = strings.TrimSpace(project.Config.Worker.Agent)
	}
	probed := map[string]bool{}
	preflight := func(agent string) *taskResult {
		if agent == "" || probed[agent] {
			return nil
		}
		probed[agent] = true
		return c.preflightTaskAgent(ctx, agent)
	}

	if opts.worker {
		if res := preflight(workerAgent); res != nil {
			return *res
		}
		var out struct {
			WorkerID string `json:"workerId"`
		}
		req := struct {
			ProjectID  string `json:"projectId"`
			Brief      string `json:"brief"`
			Agent      string `json:"agent,omitempty"`
			BaseBranch string `json:"baseBranch,omitempty"`
		}{ProjectID: project.ID, Brief: brief, Agent: strings.TrimSpace(opts.agent), BaseBranch: strings.TrimSpace(opts.base)}
		// The desktop's new-task route: one worker, named from the brief.
		if err := c.postSetupJSON(ctx, "orchestrators/delegate", req, &out); err != nil {
			return c.taskAPIFailure(ctx, "", nil, workerAgent, "start worker", err)
		}
		if strings.TrimSpace(out.WorkerID) == "" {
			return failedTask("", nil, "daemon returned no worker id", "")
		}
		return c.taskStatus(ctx, taskID{session: out.WorkerID})
	}

	if project.Config != nil {
		if res := preflight(strings.TrimSpace(project.Config.Orchestrator.Agent)); res != nil {
			return *res
		}
	}
	if res := preflight(workerAgent); res != nil {
		return *res
	}
	var ensured struct {
		Orchestrator struct {
			ID string `json:"id"`
		} `json:"orchestrator"`
	}
	if err := c.postSetupJSON(ctx, "orchestrators", map[string]string{"projectId": project.ID}, &ensured); err != nil {
		agent := ""
		if project.Config != nil {
			agent = strings.TrimSpace(project.Config.Orchestrator.Agent)
		}
		return c.taskAPIFailure(ctx, "", nil, agent, "start orchestrator", err)
	}
	orchestratorID := strings.TrimSpace(ensured.Orchestrator.ID)
	if orchestratorID == "" {
		return failedTask("", nil, "daemon returned no orchestrator id", "")
	}
	sessions := []string{orchestratorID}
	sess, err := c.fetchTaskSession(ctx, orchestratorID)
	if err != nil {
		return failedTask(orchestratorID, sessions, err.Error(), "")
	}
	if res := preflight(sess.Harness); res != nil {
		res.TaskID, res.SessionIDs = orchestratorID, sessions
		return *res
	}

	message := orchestratorTaskMessage(brief, strings.TrimSpace(opts.agent), strings.TrimSpace(opts.base))
	var sent struct {
		TurnID string `json:"turnId"`
	}
	sendErr := c.postJSON(ctx, "sessions/"+url.PathEscape(orchestratorID)+"/conversation/messages",
		map[string]string{"text": message, "clientMessageId": newTaskClientMessageID()}, &sent)
	var apiErr apiResponseError
	if errors.As(sendErr, &apiErr) && apiErr.ErrorBody.Code == "SESSION_MODE_MISMATCH" {
		// A terminal (TUI) orchestrator has no chat turns: deliver the task as
		// `ao send` does and follow the session's activity instead.
		sendErr = c.postJSON(ctx, "sessions/"+url.PathEscape(orchestratorID)+"/send", map[string]string{"message": message}, nil)
		sent.TurnID = ""
	} else if sendErr == nil && strings.TrimSpace(sent.TurnID) == "" {
		return failedTask(orchestratorID, sessions, "daemon accepted the task but returned no turn id", "")
	}
	if sendErr != nil {
		return c.taskAPIFailure(ctx, orchestratorID, sessions, sess.Harness, "send task to orchestrator", sendErr)
	}
	return c.taskStatus(ctx, taskID{session: orchestratorID, turn: strings.TrimSpace(sent.TurnID)})
}

// orchestratorTaskMessage is the brief plus the worker choices the caller
// made, which the orchestrator applies when it spawns workers.
func orchestratorTaskMessage(brief, agent, base string) string {
	var notes []string
	if agent != "" {
		notes = append(notes, fmt.Sprintf("Spawn workers for this task with `--agent %s`.", agent))
	}
	if base != "" {
		notes = append(notes, fmt.Sprintf("Start workers for this task from base branch `%s` (`--base %s`).", base, base))
	}
	if len(notes) == 0 {
		return brief
	}
	return brief + "\n\n" + strings.Join(notes, "\n")
}

func newTaskClientMessageID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ao-task-%d", time.Now().UnixNano())
	}
	return "ao-task-" + hex.EncodeToString(b[:])
}

func (c *commandContext) resolveTaskProject(ctx context.Context, explicit string) (projectDetails, error) {
	if id := strings.TrimSpace(explicit); id != "" {
		return c.fetchProjectDetails(ctx, id)
	}
	if id := strings.TrimSpace(os.Getenv("AO_PROJECT_ID")); id != "" {
		return c.fetchProjectDetails(ctx, id)
	}
	project, ok, err := c.resolveProjectFromCWD(ctx)
	if err != nil {
		return projectDetails{}, err
	}
	if !ok {
		cwd, _ := os.Getwd()
		return projectDetails{}, fmt.Errorf("no registered project contains %s; pass --project or register it with `ao project add --path <repo>`", cwd)
	}
	return project, nil
}

// signInErrorCodes are daemon error codes that a fresh agent sign-in fixes.
var signInErrorCodes = map[string]bool{
	"AGENT_ACCOUNT_DEFAULT_UNUSABLE": true,
	"AGENT_ACCOUNT_SIGNED_OUT":       true,
	"AGENT_ACCOUNT_KEY_REJECTED":     true,
}

func (c *commandContext) taskAPIFailure(ctx context.Context, id string, sessions []string, agent, action string, err error) taskResult {
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && signInErrorCodes[apiErr.ErrorBody.Code] {
		return failedTask(id, sessions, fmt.Sprintf("%s: %s", action, err.Error()), c.signInFix(ctx, agent))
	}
	return failedTask(id, sessions, fmt.Sprintf("%s: %s", action, err.Error()), "")
}

// preflightTaskAgent re-checks an agent's sign-in now. Only a definite answer
// stops the task: "configured" and "unknown" go ahead, and the first turn is
// the authoritative check.
func (c *commandContext) preflightTaskAgent(ctx context.Context, agent string) *taskResult {
	var probe struct {
		Agent struct {
			AuthStatus string `json:"authStatus"`
		} `json:"agent"`
		Supported bool `json:"supported"`
		Installed bool `json:"installed"`
	}
	if err := c.postJSON(ctx, "agents/"+url.PathEscape(agent)+"/probe", struct{}{}, &probe); err != nil {
		res := failedTask("", nil, fmt.Sprintf("check agent %s: %s", agent, err.Error()), "")
		return &res
	}
	switch {
	case !probe.Supported:
		res := failedTask("", nil, fmt.Sprintf("agent %q is not supported by this Orchestrator", agent), "pick a supported agent from `ao agent ls` and pass --agent")
		return &res
	case !probe.Installed:
		res := failedTask("", nil, fmt.Sprintf("agent %q is not installed", agent), fmt.Sprintf("install the %s CLI, then retry", agent))
		return &res
	case probe.Agent.AuthStatus == "unauthorized":
		res := failedTask("", nil, fmt.Sprintf("agent %q is not signed in", agent), c.signInFix(ctx, agent))
		res.Reason += " (auth_required)"
		return &res
	}
	return nil
}

// signInFix names the agent's own login command from the daemon's auth plans.
func (c *commandContext) signInFix(ctx context.Context, agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "sign the agent in, then retry"
	}
	var plans struct {
		Plans []struct {
			AgentID          string `json:"agentId"`
			DisplayCommand   string `json:"displayCommand"`
			Guidance         string `json:"guidance"`
			DocumentationURL string `json:"documentationUrl"`
		} `json:"plans"`
	}
	if err := c.getJSON(ctx, "agents/auth-plans", &plans); err == nil {
		for _, plan := range plans.Plans {
			if plan.AgentID != agent {
				continue
			}
			switch {
			case strings.TrimSpace(plan.DisplayCommand) != "":
				return fmt.Sprintf("run `%s`, then retry", strings.TrimSpace(plan.DisplayCommand))
			case strings.TrimSpace(plan.Guidance) != "":
				return strings.TrimSpace(plan.Guidance)
			case strings.TrimSpace(plan.DocumentationURL) != "":
				return fmt.Sprintf("sign %s in (see %s), then retry", agent, strings.TrimSpace(plan.DocumentationURL))
			}
		}
	}
	return fmt.Sprintf("sign %s in, then retry", agent)
}

// taskSession is the part of GET /sessions/{id} a task status needs.
type taskSession struct {
	ID             string `json:"id"`
	Harness        string `json:"harness"`
	Mode           string `json:"mode"`
	IsTerminated   bool   `json:"isTerminated"`
	ProvisionState string `json:"provisionState"`
	ProvisionError string `json:"provisionError"`
	Activity       struct {
		State string `json:"state"`
	} `json:"activity"`
}

// taskConversation is the part of GET /sessions/{id}/conversation a task
// status needs.
type taskConversation struct {
	HasMoreBefore     bool `json:"hasMoreBefore"`
	BatchedDeliveries []struct {
		TurnID              string `json:"turnId"`
		DeliveredWithTurnID string `json:"deliveredWithTurnId"`
	} `json:"batchedDeliveries"`
	Turns []struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		RetryOfTurnID string `json:"retryOfTurnId"`
		ErrorMessage  string `json:"errorMessage"`
		FailureCode   string `json:"failureCode"`
	} `json:"turns"`
	Activities []struct {
		TurnID       string         `json:"turnId"`
		ActivityKind string         `json:"activityKind"`
		Status       string         `json:"status"`
		Detail       map[string]any `json:"detail"`
	} `json:"activities"`
}

func (c *commandContext) fetchTaskSession(ctx context.Context, id string) (taskSession, error) {
	var res struct {
		Session taskSession `json:"session"`
	}
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(id), &res); err != nil {
		var apiErr apiResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return taskSession{}, fmt.Errorf("session %s not found", id)
		}
		return taskSession{}, fmt.Errorf("read session %s: %w", id, err)
	}
	return res.Session, nil
}

// taskStatus reads the task's session and conversation once.
func (c *commandContext) taskStatus(ctx context.Context, id taskID) taskResult {
	sessions := []string{id.session}
	sess, err := c.fetchTaskSession(ctx, id.session)
	if err != nil {
		return failedTask(id.String(), sessions, err.Error(), "")
	}
	var conv *taskConversation
	if sess.Mode != "tui" && sess.ProvisionState != "provisioning" && !sess.IsTerminated {
		var snapshot taskConversation
		err := c.getJSON(ctx, "sessions/"+url.PathEscape(id.session)+"/conversation", &snapshot)
		var apiErr apiResponseError
		switch {
		case err == nil:
			conv = &snapshot
		case errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "SESSION_MODE_MISMATCH":
		default:
			return failedTask(id.String(), sessions, fmt.Sprintf("read conversation of %s: %s", id.session, err.Error()), "")
		}
	}
	eval := evaluateTask(sess, conv, id.turn)
	res := taskResult{
		Status:     eval.status,
		TaskID:     id.String(),
		SessionIDs: append([]string{id.session}, eval.spawned...),
		Reason:     eval.reason,
	}
	switch eval.failureCode {
	case "auth_required":
		res.Fix = c.signInFix(ctx, sess.Harness)
	case "provider_key_missing":
		res.Fix = fmt.Sprintf("set up the API key that %s needs in the Orchestrator app's Agent Harnesses settings, then retry", sess.Harness)
	}
	return res
}

type taskEvaluation struct {
	status      string
	reason      string
	failureCode string
	spawned     []string
}

// evaluateTask turns a session and its conversation into a task status. With
// no turn id the task is the session's first turn (a worker's prompt).
func evaluateTask(sess taskSession, conv *taskConversation, turnID string) taskEvaluation {
	switch {
	case sess.IsTerminated:
		return taskEvaluation{status: taskStatusFailed, reason: fmt.Sprintf("session %s was archived", sess.ID)}
	case sess.ProvisionState == "failed":
		reason := strings.TrimSpace(sess.ProvisionError)
		if reason == "" {
			reason = "unknown error"
		}
		return taskEvaluation{status: taskStatusFailed, reason: "session failed to start: " + reason}
	case sess.ProvisionState == "provisioning":
		return taskEvaluation{status: taskStatusStarting, reason: "preparing the workspace"}
	}
	if conv == nil {
		return evaluateTerminalTask(sess)
	}

	idx := -1
	if turnID == "" {
		if len(conv.Turns) > 0 && !conv.HasMoreBefore {
			idx = 0
		}
	} else {
		target := turnID
		for _, batched := range conv.BatchedDeliveries {
			if batched.TurnID == target && batched.DeliveredWithTurnID != "" {
				target = batched.DeliveredWithTurnID
			}
		}
		for i, turn := range conv.Turns {
			if turn.ID == target {
				idx = i
				break
			}
		}
	}
	// A failed turn the user retried continues as its retry.
	for idx >= 0 {
		next := -1
		for i, turn := range conv.Turns {
			if turn.RetryOfTurnID != "" && turn.RetryOfTurnID == conv.Turns[idx].ID {
				next = i
			}
		}
		if next < 0 {
			break
		}
		idx = next
	}

	for _, act := range conv.Activities {
		if (act.ActivityKind == "approval" || act.ActivityKind == "user_input") && act.Status == "pending" {
			return taskEvaluation{status: taskStatusNeedsInput, reason: "the agent is waiting for your answer or approval"}
		}
	}
	if sess.Activity.State == "blocked" {
		return taskEvaluation{status: taskStatusNeedsInput, reason: "the agent is waiting for your approval"}
	}

	if idx < 0 {
		for _, act := range conv.Activities {
			if act.ActivityKind == "system" && detailString(act.Detail, "event") == "auth.reauth_required" {
				return taskEvaluation{status: taskStatusFailed, reason: "the agent needs to sign in again (auth_required)", failureCode: "auth_required"}
			}
		}
		if len(conv.Turns) > 0 && conv.HasMoreBefore {
			// The task's turn is older than the loaded page, so it ran long ago.
			return taskEvaluation{status: taskStatusRunning, reason: "the task's turn is older than the session's recent history"}
		}
		return taskEvaluation{status: taskStatusStarting, reason: "waiting for the agent to pick up the task"}
	}

	turn := conv.Turns[idx]
	var spawned []string
	for _, act := range conv.Activities {
		if act.TurnID == turn.ID && act.ActivityKind == "ao_action" && detailString(act.Detail, "action") == "session.spawned" {
			if target := detailString(act.Detail, "targetSessionId"); target != "" && target != sess.ID {
				spawned = append(spawned, target)
			}
		}
	}
	switch turn.State {
	case "queued":
		return taskEvaluation{status: taskStatusStarting, reason: "queued behind the agent's current turn", spawned: spawned}
	case "running":
		return taskEvaluation{status: taskStatusRunning, reason: "the agent is working on it", spawned: spawned}
	case "completed", "recovered":
		return taskEvaluation{status: taskStatusDone, reason: "the agent finished its turn", spawned: spawned}
	case "failed":
		reason := strings.TrimSpace(turn.ErrorMessage)
		if reason == "" {
			reason = "the agent's turn failed"
		}
		if turn.FailureCode != "" {
			reason += " (" + turn.FailureCode + ")"
		}
		return taskEvaluation{status: taskStatusFailed, reason: reason, failureCode: turn.FailureCode, spawned: spawned}
	case "interrupted":
		return taskEvaluation{status: taskStatusFailed, reason: "the agent's turn was interrupted before it finished", spawned: spawned}
	case "cancelled":
		return taskEvaluation{status: taskStatusFailed, reason: "the task was cancelled before the agent started it", spawned: spawned}
	}
	return taskEvaluation{status: taskStatusStarting, reason: fmt.Sprintf("turn state %q", turn.State), spawned: spawned}
}

// evaluateTerminalTask reads a terminal (TUI) session, which has no chat
// turns, from its reported activity.
func evaluateTerminalTask(sess taskSession) taskEvaluation {
	switch sess.Activity.State {
	case "active":
		return taskEvaluation{status: taskStatusRunning, reason: "the agent is working on it (terminal session)"}
	case "blocked":
		return taskEvaluation{status: taskStatusNeedsInput, reason: "the agent is waiting for your approval (terminal session)"}
	case "waiting_input":
		return taskEvaluation{status: taskStatusDone, reason: "the agent is waiting for its next instruction (terminal session)"}
	case "exited":
		return taskEvaluation{status: taskStatusFailed, reason: "the agent process exited (terminal session)"}
	}
	return taskEvaluation{status: taskStatusStarting, reason: "waiting for the agent to pick up the task (terminal session)"}
}

func detailString(detail map[string]any, key string) string {
	value, _ := detail[key].(string)
	return strings.TrimSpace(value)
}

// waitTask polls until the task has started, failed, or needs the user. A
// running turn must stay running for taskRunningSettle first.
func (c *commandContext) waitTask(ctx context.Context, res taskResult, timeout time.Duration) taskResult {
	id, err := parseTaskID(res.TaskID)
	if err != nil {
		return res
	}
	deadline := c.deps.Now().Add(timeout)
	var runningSince time.Time
	for {
		if res.Status == "" || res.Status == taskStatusStarting || res.Status == taskStatusRunning {
			res = c.taskStatus(ctx, id)
		}
		now := c.deps.Now()
		switch res.Status {
		case taskStatusFailed, taskStatusNeedsInput, taskStatusDone:
			return res
		case taskStatusRunning:
			if runningSince.IsZero() {
				runningSince = now
			}
			if now.Sub(runningSince) >= taskRunningSettle || !now.Before(deadline) {
				return res
			}
		default:
			runningSince = time.Time{}
		}
		if !now.Before(deadline) {
			// Nothing has run yet: the usual cause is an agent that is not signed in.
			agent := ""
			if sess, err := c.fetchTaskSession(ctx, id.session); err == nil {
				agent = sess.Harness
			}
			fix := "check the session in Orchestrator"
			if agent != "" {
				fix = "if " + agent + " is not signed in, " + c.signInFix(ctx, agent)
			}
			return failedTask(res.TaskID, res.SessionIDs, fmt.Sprintf("timed out after %s: the task has not started (%s)", timeout, res.Reason), fix)
		}
		if err := ctx.Err(); err != nil {
			return failedTask(res.TaskID, res.SessionIDs, "interrupted while waiting: "+err.Error(), "")
		}
		c.deps.Sleep(taskPollInterval)
		res.Status = taskStatusStarting
	}
}
