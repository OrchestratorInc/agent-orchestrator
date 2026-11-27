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
	"slices"
	"strconv"
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
// 2 usage error only (the CLI-wide meaning), 3 it is waiting on the user, 4 a
// wait ran out while the task was still queued (it will run; do not resend).
// A task outcome never goes through usageError, so these are not counted as
// usage errors.

// Task statuses reported in the `status` field of --json output.
const (
	taskStatusStarting   = "starting"
	taskStatusRunning    = "running"
	taskStatusDone       = "done"
	taskStatusFailed     = "failed"
	taskStatusNeedsInput = "needs_input"
	// taskStatusQueued: the agent accepted the task and will run it after its
	// current turn.
	taskStatusQueued = "queued"
)

// Exit codes for task statuses that are not success.
const (
	taskExitFailed     = 1
	taskExitNeedsInput = 3
	// taskExitStillQueued: a wait timed out while the task was queued. It is
	// accepted and will run, so a caller must not start it again.
	taskExitStillQueued = 4
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
	// phase is the reading behind Status (not printed), so `start --wait`
	// carries what the start already saw into the wait.
	phase string
	// waitTimedOut marks a result a wait gave up on (not printed).
	waitTimedOut bool
}

// taskExitError carries a task outcome's exit code to the process entrypoint.
type taskExitError struct {
	code int
	msg  string
}

func (e taskExitError) Error() string { return e.msg }

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
			"Exit codes: 0 started (running or done), 1 failed or timed out, 2 usage error, 3 waiting on the user,\n" +
			"4 a wait ran out while the task was still queued (it will run; do not start it again).\n" +
			"--wait and `ao task wait` confirm the task started; poll `ao task status` for completion.\n" +
			"With --json, stdout is {status, taskId, sessionIds, reason, fix}; status is one of\n" +
			"starting, queued, running, done, failed, needs_input.",
		// Runnable so a mistyped subcommand (`ao task statuz`) is a usage
		// error, exit 2, instead of help with exit 0. Bare `ao task` shows help.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError{fmt.Errorf("unknown command %q for %q; run `ao task --help`", args[0], cmd.CommandPath())}
			}
			return cmd.Help()
		},
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
			res, err := ctx.startTask(cmd.Context(), brief, opts)
			if err != nil {
				return err
			}
			if opts.wait && (res.Status == taskStatusStarting || res.Status == taskStatusQueued || res.Status == taskStatusRunning) {
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
	case taskStatusQueued:
		if res.waitTimedOut {
			return taskExitError{code: taskExitStillQueued, msg: "task is still queued: " + msg}
		}
	}
	return nil
}

func failedTask(id string, sessions []string, reason, fix string) taskResult {
	return taskResult{Status: taskStatusFailed, TaskID: id, SessionIDs: sessions, Reason: reason, Fix: fix}
}

// startTask creates the task and returns its first status. Usage errors
// (such as an ambiguous project) come back as the error; every other failure
// is a failed result so --json callers always get the same shape.
func (c *commandContext) startTask(ctx context.Context, brief string, opts taskStartOptions) (taskResult, error) {
	project, err := c.resolveTaskProject(ctx, opts.project)
	var ue usageError
	if errors.As(err, &ue) {
		return taskResult{}, err
	}
	if err != nil {
		return failedTask("", nil, err.Error(), ""), nil
	}
	res := c.createTask(ctx, project, brief, opts)
	return res, nil
}

func (c *commandContext) createTask(ctx context.Context, project projectDetails, brief string, opts taskStartOptions) taskResult {
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
			ClientRequestID string `json:"clientRequestId"`
			ProjectID       string `json:"projectId"`
			Brief           string `json:"brief"`
			Agent           string `json:"agent,omitempty"`
			BaseBranch      string `json:"baseBranch,omitempty"`
		}{ClientRequestID: newTaskClientMessageID(), ProjectID: project.ID, Brief: brief, Agent: strings.TrimSpace(opts.agent), BaseBranch: strings.TrimSpace(opts.base)}
		// The desktop's new-task route: one worker, named from the brief.
		err := c.postSetupJSON(ctx, "orchestrators/delegate", req, &out)
		var apiErr apiResponseError
		if err != nil && !errors.As(err, &apiErr) && ctx.Err() == nil {
			// The daemon may have started the worker before the reply was
			// lost. The same client request id returns that worker instead of
			// starting a second one.
			err = c.postSetupJSON(ctx, "orchestrators/delegate", req, &out)
		}
		switch {
		case errors.As(err, &apiErr):
			return c.taskAPIFailure(ctx, nil, workerAgent, "start worker", err)
		case err != nil:
			return failedTask("", nil, fmt.Sprintf("could not confirm that the worker started (client request id %s): %s", req.ClientRequestID, err.Error()),
				"check `ao session ls` before starting the task again, so it does not run twice")
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
	if err := c.postSetupJSON(ctx, "orchestrators", spawnOrchestratorAPIRequest{ProjectID: project.ID}, &ensured); err != nil {
		agent := ""
		if project.Config != nil {
			agent = strings.TrimSpace(project.Config.Orchestrator.Agent)
		}
		return c.taskAPIFailure(ctx, nil, agent, "start orchestrator", err)
	}
	orchestratorID := strings.TrimSpace(ensured.Orchestrator.ID)
	if orchestratorID == "" {
		return failedTask("", nil, "daemon returned no orchestrator id", "")
	}
	sessions := []string{orchestratorID}
	sess, err := c.fetchTaskSession(ctx, orchestratorID)
	if err != nil {
		return failedTask("", sessions, err.Error(), "")
	}
	if res := preflight(sess.Harness); res != nil {
		res.SessionIDs = sessions
		return *res
	}

	message := orchestratorTaskMessage(brief, strings.TrimSpace(opts.agent), strings.TrimSpace(opts.base))
	var sent struct {
		TurnID string `json:"turnId"`
	}
	messagePath := "sessions/" + url.PathEscape(orchestratorID) + "/conversation/messages"
	clientMessageID := newTaskClientMessageID()
	body := conversationMessageAPIRequest{Text: message, ClientMessageID: clientMessageID}
	sendErr := c.postJSON(ctx, messagePath, body, &sent)
	var apiErr apiResponseError
	if sendErr != nil && !errors.As(sendErr, &apiErr) && ctx.Err() == nil {
		// The daemon may have accepted the message before the reply was lost.
		// The same client message id makes this retry idempotent: it returns
		// the turn the first attempt created instead of starting a second one.
		sendErr = c.postJSON(ctx, messagePath, body, &sent)
	}
	if errors.As(sendErr, &apiErr) && apiErr.ErrorBody.Code == "SESSION_MODE_MISMATCH" {
		// A terminal (TUI) orchestrator has no chat turns: deliver the task as
		// `ao send` does and follow the session's activity instead.
		// Same attribution as `ao send`: an agent inside a session names itself;
		// anything else is the user's own message.
		sender := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
		if err := c.postJSON(ctx, "sessions/"+url.PathEscape(orchestratorID)+"/send", sendAPIRequest{Message: message, SenderSessionID: sender, UserAuthored: sender == ""}, nil); err != nil {
			return c.taskAPIFailure(ctx, sessions, sess.Harness, "send task to orchestrator", err)
		}
		return c.taskStatus(ctx, taskID{session: orchestratorID})
	}
	switch {
	case errors.As(sendErr, &apiErr):
		return c.taskAPIFailure(ctx, sessions, sess.Harness, "send task to orchestrator", sendErr)
	case sendErr != nil:
		// No task id: a bare session id would name the orchestrator's other turns.
		return failedTask("", sessions, fmt.Sprintf("could not confirm that orchestrator %s received the task (client message id %s): %s", orchestratorID, clientMessageID, sendErr.Error()),
			fmt.Sprintf("check `ao session history %s` before starting the task again, so it is not sent twice", orchestratorID))
	case strings.TrimSpace(sent.TurnID) == "":
		return failedTask("", sessions, "daemon accepted the task but returned no turn id", "")
	}
	return c.taskStatus(ctx, taskID{session: orchestratorID, turn: strings.TrimSpace(sent.TurnID)})
}

// spawnOrchestratorAPIRequest mirrors the daemon's SpawnOrchestratorRequest
// for POST /orchestrators, which returns the project's orchestrator and
// starts it only when there is none.
type spawnOrchestratorAPIRequest struct {
	ProjectID string `json:"projectId"`
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
		return projectDetails{}, usageError{fmt.Errorf("no registered project contains %s; pass --project or register it with `ao project add --path <repo>`", cwd)}
	}
	return project, nil
}

// signInErrorCodes are daemon error codes that a fresh agent sign-in fixes.
var signInErrorCodes = map[string]bool{
	"AGENT_ACCOUNT_DEFAULT_UNUSABLE": true,
	"AGENT_ACCOUNT_SIGNED_OUT":       true,
	"AGENT_ACCOUNT_KEY_REJECTED":     true,
}

func (c *commandContext) taskAPIFailure(ctx context.Context, sessions []string, agent, action string, err error) taskResult {
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && signInErrorCodes[apiErr.ErrorBody.Code] {
		return failedTask("", sessions, fmt.Sprintf("%s: %s", action, err.Error()), c.signInFix(ctx, agent))
	}
	if errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "SESSION_DELIVERY_UNCONFIRMED" && len(sessions) > 0 && agent != "" {
		// A terminal agent that never takes the message is often sitting at a
		// sign-in screen.
		return failedTask("", sessions, fmt.Sprintf("%s: %s", action, err.Error()),
			fmt.Sprintf("check the agent's terminal with `ao session get %s`; if %s is not signed in, %s", sessions[0], agent, c.signInFix(ctx, agent)))
	}
	return failedTask("", sessions, fmt.Sprintf("%s: %s", action, err.Error()), "")
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

type taskTurn struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	RetryOfTurnID string `json:"retryOfTurnId"`
	ErrorMessage  string `json:"errorMessage"`
	FailureCode   string `json:"failureCode"`
}

type taskActivity struct {
	ID           string         `json:"id"`
	TurnID       string         `json:"turnId"`
	ActivityKind string         `json:"activityKind"`
	Status       string         `json:"status"`
	Detail       map[string]any `json:"detail"`
}

type taskBatchedDelivery struct {
	TurnID              string `json:"turnId"`
	DeliveredWithTurnID string `json:"deliveredWithTurnId"`
}

// taskConversation is the part of GET /sessions/{id}/conversation a task
// status needs.
type taskConversation struct {
	HasMoreBefore     bool                  `json:"hasMoreBefore"`
	OldestSequence    int64                 `json:"oldestSequence"`
	BatchedDeliveries []taskBatchedDelivery `json:"batchedDeliveries"`
	Turns             []taskTurn            `json:"turns"`
	Activities        []taskActivity        `json:"activities"`
}

// taskHistoryPageLimit bounds how many older conversation pages a status read
// loads while looking for the task's turn.
const taskHistoryPageLimit = 50

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

// fetchTaskConversation reads the conversation and pages back until the
// task's turn and all of its activity are loaded: until an earlier turn
// appears too, or history ends. A worker task is its session's first turn, so
// it reads back to the start.
func (c *commandContext) fetchTaskConversation(ctx context.Context, id taskID) (*taskConversation, error) {
	path := "sessions/" + url.PathEscape(id.session) + "/conversation"
	var conv taskConversation
	if err := c.getJSON(ctx, path, &conv); err != nil {
		return nil, err
	}
	for page := 0; page < taskHistoryPageLimit && conv.HasMoreBefore && conv.OldestSequence > 0; page++ {
		// Once an earlier turn is loaded too, the page boundary no longer
		// splits the task's turn, so its earliest activity (such as a worker
		// it spawned) is loaded as well.
		if id.turn != "" && conv.findTurn(id.turn) > 0 {
			break
		}
		var older taskConversation
		if err := c.getJSON(ctx, path+"?beforeSequence="+strconv.FormatInt(conv.OldestSequence, 10), &older); err != nil {
			return nil, err
		}
		if older.OldestSequence <= 0 || older.OldestSequence >= conv.OldestSequence {
			break
		}
		conv.prepend(older)
	}
	return &conv, nil
}

// findTurn returns the index of the turn that carries turnID, following a
// queued message that was batched into another turn.
func (conv *taskConversation) findTurn(turnID string) int {
	target := turnID
	for _, batched := range conv.BatchedDeliveries {
		if batched.TurnID == target && batched.DeliveredWithTurnID != "" {
			target = batched.DeliveredWithTurnID
		}
	}
	for i, turn := range conv.Turns {
		if turn.ID == target {
			return i
		}
	}
	return -1
}

// prepend merges an older page in front of the loaded one.
func (conv *taskConversation) prepend(older taskConversation) {
	seenTurns := map[string]bool{}
	for _, turn := range conv.Turns {
		seenTurns[turn.ID] = true
	}
	turns := make([]taskTurn, 0, len(older.Turns)+len(conv.Turns))
	for _, turn := range older.Turns {
		if !seenTurns[turn.ID] {
			turns = append(turns, turn)
		}
	}
	conv.Turns = append(turns, conv.Turns...)

	seenActs := map[string]bool{}
	for _, act := range conv.Activities {
		if act.ID != "" {
			seenActs[act.ID] = true
		}
	}
	acts := make([]taskActivity, 0, len(older.Activities)+len(conv.Activities))
	for _, act := range older.Activities {
		if act.ID == "" || !seenActs[act.ID] {
			acts = append(acts, act)
		}
	}
	conv.Activities = append(acts, conv.Activities...)
	conv.BatchedDeliveries = append(older.BatchedDeliveries, conv.BatchedDeliveries...)
	conv.HasMoreBefore = older.HasMoreBefore
	conv.OldestSequence = older.OldestSequence
}

// taskObservation is one status read plus what the wait loop needs from it.
type taskObservation struct {
	result  taskResult
	harness string
	phase   string
	// turnID is the turn the status describes (after retries), empty for a
	// terminal session or before the turn exists.
	turnID string
}

// taskStatus reads the task's session and conversation once.
func (c *commandContext) taskStatus(ctx context.Context, id taskID) taskResult {
	return c.observeTask(ctx, id).result
}

func (c *commandContext) observeTask(ctx context.Context, id taskID) taskObservation {
	sessions := []string{id.session}
	sess, err := c.fetchTaskSession(ctx, id.session)
	if err != nil {
		return taskObservation{result: failedTask(id.String(), sessions, err.Error(), "")}
	}
	var conv *taskConversation
	if sess.Mode != "tui" && sess.ProvisionState != "provisioning" && !sess.IsTerminated {
		conv, err = c.fetchTaskConversation(ctx, id)
		var apiErr apiResponseError
		switch {
		case err == nil:
		case errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "SESSION_MODE_MISMATCH":
			conv = nil
		default:
			return taskObservation{
				result:  failedTask(id.String(), sessions, fmt.Sprintf("read conversation of %s: %s", id.session, err.Error()), ""),
				harness: sess.Harness,
			}
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
	res.phase = eval.phase
	return taskObservation{result: res, harness: sess.Harness, turnID: eval.turnID, phase: eval.phase}
}

type taskEvaluation struct {
	status      string
	reason      string
	failureCode string
	spawned     []string
	turnID      string
	// phase says why a task is still starting, so a timeout can name the
	// likely cause instead of always suggesting sign-in.
	phase string
}

// Why a task has not started yet.
const (
	taskPhaseProvisioning   = "provisioning"
	taskPhaseQueued         = "queued"
	taskPhaseAwaitingTurn   = "awaiting_turn"
	taskPhaseTerminalPrompt = "terminal_prompt"
	taskPhaseTerminalIdle   = "terminal_idle"
	taskPhaseTerminalActive = "terminal_active"
)

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
		return taskEvaluation{status: taskStatusStarting, reason: "preparing the workspace", phase: taskPhaseProvisioning}
	}
	if conv == nil {
		return evaluateTerminalTask(sess)
	}

	idx := -1
	switch {
	case turnID != "":
		idx = conv.findTurn(turnID)
	case len(conv.Turns) > 0 && !conv.HasMoreBefore:
		idx = 0
	}
	if idx < 0 {
		for _, act := range conv.Activities {
			if act.ActivityKind == "system" && detailString(act.Detail, "event") == "auth.reauth_required" {
				return taskEvaluation{status: taskStatusFailed, reason: "the agent needs to sign in again (auth_required)", failureCode: "auth_required"}
			}
		}
		if conv.HasMoreBefore {
			return taskEvaluation{status: taskStatusStarting, reason: "the task's turn is not in the loaded session history yet", phase: taskPhaseAwaitingTurn}
		}
		return taskEvaluation{status: taskStatusStarting, reason: "waiting for the agent to pick up the task", phase: taskPhaseAwaitingTurn}
	}

	// A failed turn the user retried continues as its retry. The whole chain
	// is the task: its approvals and the workers it started all count.
	chain := map[string]bool{conv.Turns[idx].ID: true}
	for {
		next := -1
		for i, turn := range conv.Turns {
			if turn.RetryOfTurnID != "" && turn.RetryOfTurnID == conv.Turns[idx].ID {
				next = i
			}
		}
		if next < 0 || chain[conv.Turns[next].ID] {
			break
		}
		idx = next
		chain[conv.Turns[idx].ID] = true
	}
	turn := conv.Turns[idx]

	var spawned []string
	for _, act := range conv.Activities {
		if chain[act.TurnID] && act.ActivityKind == "ao_action" && detailString(act.Detail, "action") == "session.spawned" {
			if target := detailString(act.Detail, "targetSessionId"); target != "" && target != sess.ID && !slices.Contains(spawned, target) {
				spawned = append(spawned, target)
			}
		}
	}
	eval := func(status, reason string) taskEvaluation {
		return taskEvaluation{status: status, reason: reason, spawned: spawned, turnID: turn.ID}
	}

	// Only the live attempt can still be answered: an approval left pending on
	// an earlier, failed attempt is stale. The session-wide activity state is
	// not used here, since it is read before the conversation and can describe
	// another turn.
	if turn.State == "running" {
		for _, act := range conv.Activities {
			if (act.ActivityKind == "approval" || act.ActivityKind == "user_input") && act.Status == "pending" && act.TurnID == turn.ID {
				return eval(taskStatusNeedsInput, "the agent is waiting for your answer or approval")
			}
		}
	}

	switch turn.State {
	case "queued":
		res := eval(taskStatusQueued, "queued behind the agent's current turn")
		res.phase = taskPhaseQueued
		return res
	case "running":
		return eval(taskStatusRunning, "the agent is working on it")
	case "completed", "recovered":
		return eval(taskStatusDone, "the agent finished its turn")
	case "failed":
		reason := strings.TrimSpace(turn.ErrorMessage)
		if reason == "" {
			reason = "the agent's turn failed"
		}
		if turn.FailureCode != "" {
			reason += " (" + turn.FailureCode + ")"
		}
		res := eval(taskStatusFailed, reason)
		res.failureCode = turn.FailureCode
		return res
	case "interrupted":
		return eval(taskStatusFailed, "the agent's turn was interrupted before it finished")
	case "cancelled":
		return eval(taskStatusFailed, "the task was cancelled before the agent started it")
	}
	return eval(taskStatusStarting, fmt.Sprintf("turn state %q", turn.State))
}

// evaluateTerminalTask reads a terminal (TUI) session, which has no chat
// turns, from its reported activity. Its prompt state looks the same before
// the agent reads the task and after it finishes, so it is never "done".
func evaluateTerminalTask(sess taskSession) taskEvaluation {
	switch sess.Activity.State {
	case "active":
		return taskEvaluation{status: taskStatusRunning, reason: "the agent is working (terminal session)", phase: taskPhaseTerminalActive}
	case "blocked":
		return taskEvaluation{status: taskStatusNeedsInput, reason: "the agent is waiting for your approval (terminal session)"}
	case "exited":
		return taskEvaluation{status: taskStatusFailed, reason: "the agent process exited (terminal session)"}
	case "waiting_input":
		return taskEvaluation{status: taskStatusStarting, reason: "the agent is at its prompt; a terminal session does not say whether it has taken this task yet", phase: taskPhaseTerminalPrompt}
	}
	return taskEvaluation{status: taskStatusStarting, reason: "waiting for the agent to pick up the task (terminal session)", phase: taskPhaseTerminalIdle}
}

func detailString(detail map[string]any, key string) string {
	value, _ := detail[key].(string)
	return strings.TrimSpace(value)
}

// waitTask polls until the task has started, failed, or needs the user. A
// running turn must stay running for taskRunningSettle, counted from when that
// same turn was first seen running, and must settle before the deadline.
func (c *commandContext) waitTask(ctx context.Context, res taskResult, timeout time.Duration) taskResult {
	id, err := parseTaskID(res.TaskID)
	if err != nil {
		return res
	}
	deadline := c.deps.Now().Add(timeout)
	last := res
	harness, phase := "", ""
	sawTerminalWork := res.phase == taskPhaseTerminalActive
	var runningTurn string
	var runningSince time.Time
	for {
		remaining := deadline.Sub(c.deps.Now())
		if remaining <= 0 {
			break
		}
		readCtx, cancel := context.WithTimeout(ctx, remaining)
		obs := c.observeTask(readCtx, id)
		readErr := readCtx.Err()
		cancel()
		if err := ctx.Err(); err != nil {
			return failedTask(id.String(), last.SessionIDs, "interrupted while waiting: "+err.Error(), "")
		}
		if readErr != nil {
			break // the read ran into the deadline
		}
		if obs.harness != "" {
			harness = obs.harness
		}
		last, phase = obs.result, obs.phase
		now := c.deps.Now()
		if obs.phase == taskPhaseTerminalActive {
			sawTerminalWork = true
		}
		if obs.phase == taskPhaseTerminalPrompt && sawTerminalWork {
			// This wait saw the terminal agent working and then return to its
			// prompt, so it took the task and finished it.
			last.Status, last.Reason = taskStatusDone, "the agent worked on the task and is back at its prompt (terminal session)"
			return last
		}
		switch last.Status {
		case taskStatusFailed, taskStatusNeedsInput, taskStatusDone:
			return last
		case taskStatusRunning:
			if runningSince.IsZero() || obs.turnID != runningTurn {
				runningTurn, runningSince = obs.turnID, now
			}
			if now.Sub(runningSince) >= taskRunningSettle {
				return last
			}
		default:
			runningSince = time.Time{}
		}
		remaining = deadline.Sub(now)
		if remaining <= 0 {
			break
		}
		c.deps.Sleep(min(taskPollInterval, remaining))
	}

	// Nothing settled in time. A queued task is accepted and will still run,
	// so it is reported apart from a failure: retrying it would send it twice.
	if last.Status == taskStatusQueued {
		last.waitTimedOut = true
		last.Reason = fmt.Sprintf("timed out after %s while still queued behind the agent's current turn; the task is accepted and will run, so do not start it again", timeout)
		last.Fix = fmt.Sprintf("wait longer with `ao task wait %s`, or check `ao session get %s`", id, id.session)
		return last
	}
	// Otherwise name the likely cause from the last reading.
	reason := fmt.Sprintf("timed out after %s: the task has not started", timeout)
	if last.Status == taskStatusRunning {
		reason = fmt.Sprintf("timed out after %s: the agent had only just started the task", timeout)
	}
	if last.Reason != "" {
		reason += " (" + last.Reason + ")"
	}
	check := fmt.Sprintf("`ao session get %s`", id.session)
	var fix string
	switch {
	case last.Status == taskStatusRunning:
		fix = fmt.Sprintf("the agent is working; wait longer with `ao task wait %s`, or check %s", id, check)
	case phase == taskPhaseProvisioning:
		fix = "workspace setup had not finished; check the project's setup steps and " + check
	case phase == taskPhaseTerminalPrompt:
		reason = fmt.Sprintf("timed out after %s: a terminal session does not report whether it took this task (%s)", timeout, last.Reason)
		fix = "this wait never saw the agent working, so it may have finished quickly or not started; check " + check + " or open the session in the Orchestrator app"
	case harness != "":
		fix = "if " + harness + " is not signed in, " + c.signInFix(ctx, harness) + "; otherwise check " + check
	default:
		fix = "check " + check
	}
	return failedTask(id.String(), last.SessionIDs, reason, fix)
}
