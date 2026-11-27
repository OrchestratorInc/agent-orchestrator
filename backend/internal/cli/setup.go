package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// `ao setup` takes a machine from "Orchestrator is installed" to "ready to
// take a task" in one command, so a coding agent asked to set Orchestrator up
// needs nothing else. It only calls existing building blocks: the `ao serve`
// start path, the agent readiness and probe routes, the GitHub auth check,
// project registration and the set-config route, and `ao task start`.
//
// Exit codes: 0 ready (and the task started, with --start), 10 a person must
// act first (next_actions says what), 1 failed, 2 usage error, 3 the task
// started with --start is waiting on the user, 4 that task is still queued
// when the wait ends (it will run; do not start it again).

// Step names, in the order they run. They are part of the --json contract.
const (
	setupStepDaemon   = "daemon"
	setupStepAgent    = "agent"
	setupStepGitHub   = "github"
	setupStepProject  = "project"
	setupStepFirstRun = "first-run"
	setupStepStart    = "start"
	setupStepVerify   = "verify"
)

// Step statuses. They are part of the --json contract.
const (
	setupOK         = "ok"
	setupFixed      = "fixed"
	setupSkipped    = "skipped"
	setupNeedsHuman = "needs_human"
	setupFailed     = "failed"
)

const (
	setupExitNeedsHuman = 10
	setupExitFailed     = 1
)

type setupOptions struct {
	path              string
	agent             string
	workerAgent       string
	orchestratorAgent string
	start             string
	worker            bool
	yes               bool
	check             bool
	dryRun            bool
	json              bool
}

// readOnly reports whether setup must not change anything.
func (o setupOptions) readOnly() bool { return o.check || o.dryRun }

type setupStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

type setupAgent struct {
	ID string `json:"id"`
	// Source says how the agent was chosen: flag, roles, project, caller or ready.
	Source string `json:"source"`
	// DetectedFrom names the environment variable for source "caller".
	DetectedFrom string `json:"detectedFrom,omitempty"`
	AuthStatus   string `json:"authStatus"`
	Worker       string `json:"worker"`
	Orchestrator string `json:"orchestrator"`
	// override is set when a role flag replaced one of the project's saved
	// agents.
	override bool
}

type setupProject struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	DefaultBranch string `json:"defaultBranch"`
	Reused        bool   `json:"reused"`
}

// setupResult is the stable `ao setup --json` shape. Only add fields.
type setupResult struct {
	Ready       bool          `json:"ready"`
	Agent       *setupAgent   `json:"agent"`
	Project     *setupProject `json:"project"`
	Steps       []setupStep   `json:"steps"`
	NextActions []string      `json:"next_actions"`
	Task        *taskResult   `json:"task"`
	// pending is set when a --check or --dry-run found work setup would do.
	pending bool
	// readiness and checked are the agent step's readiness reading and the
	// agents it probed, so the project step can check the agents a reused
	// project keeps.
	readiness []agentReadinessSnapshot
	checked   map[string]bool
	// agentSaveable is set when the chosen agents may be saved on the
	// project: they passed, or only wait on a person (sign in, install).
	// The project then keeps the person's choice for the next run, rather
	// than the daemon's own default agent.
	agentSaveable bool
	authPlans     map[string]setupAuthPlan
}

func newSetupCommand(ctx *commandContext) *cobra.Command {
	var opts setupOptions
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up Orchestrator on this machine and project in one command",
		Long: "Set up Orchestrator so it is ready to take a task: start (or reuse) the daemon, pick and check\n" +
			"a coding agent, check GitHub access, and register the project.\n\n" +
			"The agent is --agent if given, otherwise the agents an already registered project runs,\n" +
			"otherwise the coding agent running this command (from its environment, AI_AGENT first),\n" +
			"otherwise the first ready agent. Setup never signs anything in\n" +
			"and never installs an agent: it prints the exact command for a person to run.\n\n" +
			"With --start \"<task>\" it also starts a task, on the project's orchestrator by default or on\n" +
			"one worker with --worker, and waits until the task has started.\n\n" +
			"Exit codes: 0 ready, 10 a person must act first (see next_actions), 1 failed, 2 usage error,\n" +
			"3 the task started with --start is waiting on the user, 4 that task is still queued when\n" +
			"the wait ends (it will run; do not start it again).\n" +
			"With --json, stdout is {ready, agent, project, steps, next_actions, task}; each step is\n" +
			"{step, status, detail, fix} with status ok, fixed, skipped, needs_human or failed.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(cmd); err != nil {
				return err
			}
			res := ctx.runSetup(cmd.Context(), cmd.ErrOrStderr(), opts)
			if err := writeSetupResult(cmd.OutOrStdout(), res, opts); err != nil {
				return err
			}
			return setupExit(res, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.path, "path", "", "Repository to register (default: the git repository containing the current directory)")
	f.StringVar(&opts.agent, "agent", "", "Agent to use (default: the project's saved agents, else the calling agent, else the first ready agent)")
	f.StringVar(&opts.workerAgent, "worker-agent", "", "Agent for worker sessions (default: --agent's choice)")
	f.StringVar(&opts.orchestratorAgent, "orchestrator-agent", "", "Agent for the orchestrator (default: --agent's choice)")
	f.StringVar(&opts.start, "start", "", "Start this task once setup is ready")
	f.BoolVar(&opts.worker, "worker", false, "With --start, give the task to one worker instead of the orchestrator")
	f.BoolVar(&opts.yes, "yes", false, "Allow setup to replace agents already saved on a registered project")
	f.BoolVar(&opts.check, "check", false, "Report what is set up and what is missing; write nothing and start nothing")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Show what setup would do; write nothing")
	f.BoolVar(&opts.json, "json", false, "Print the result as JSON")
	// Registering Orchestrator with other agents (skill + MCP) is a later
	// phase and will arrive as --integrate-agents / --remove-integrations,
	// run as an "integrations" step between first-run and start.
	return cmd
}

func (o *setupOptions) validate(cmd *cobra.Command) error {
	o.path = strings.TrimSpace(o.path)
	o.agent = strings.TrimSpace(o.agent)
	o.workerAgent = strings.TrimSpace(o.workerAgent)
	o.orchestratorAgent = strings.TrimSpace(o.orchestratorAgent)
	if cmd.Flags().Changed("start") && strings.TrimSpace(o.start) == "" {
		return usageError{errors.New("--start needs a task brief")}
	}
	o.start = strings.TrimSpace(o.start)
	if o.worker && o.start == "" {
		return usageError{errors.New("--worker only applies with --start")}
	}
	if o.check && o.dryRun {
		return usageError{errors.New("--check and --dry-run cannot be used together")}
	}
	if o.check && o.start != "" {
		return usageError{errors.New("--check writes nothing, so it cannot --start a task")}
	}
	if cmd.Flags().Changed("path") && o.path == "" {
		return usageError{errors.New("--path must not be empty")}
	}
	return nil
}

// setupExit maps the result to the exit code: a failure beats a task waiting
// on the user, which beats a step waiting on a person.
func setupExit(res setupResult, opts setupOptions) error {
	var failed, human []string
	for _, s := range res.Steps {
		switch s.Status {
		case setupFailed:
			failed = append(failed, s.Step+": "+s.Detail)
		case setupNeedsHuman:
			human = append(human, s.Step+": "+s.Detail)
		}
	}
	switch {
	case len(failed) > 0:
		return taskExitError{code: setupExitFailed, msg: "setup failed: " + strings.Join(failed, "; ")}
	case res.Task != nil && res.Task.Status == taskStatusNeedsInput:
		return taskExitError{code: taskExitNeedsInput, msg: "the task is waiting on you: " + res.Task.Reason}
	case res.Task != nil && res.Task.Status == taskStatusQueued && res.Task.waitTimedOut:
		return taskExitError{code: taskExitStillQueued, msg: "the task is still queued and will run; do not start it again: " + res.Task.Reason}
	case len(human) > 0:
		return taskExitError{code: setupExitNeedsHuman, msg: "setup needs you: " + strings.Join(human, "; ")}
	case opts.check && !res.Ready:
		return taskExitError{code: setupExitNeedsHuman, msg: "setup is not complete; run `ao setup`"}
	}
	return nil
}

// runSetup runs every step and never returns an error: each problem is a
// step result, so --json callers always get the same shape.
func (c *commandContext) runSetup(ctx context.Context, errOut io.Writer, opts setupOptions) setupResult {
	res := setupResult{Steps: []setupStep{}, NextActions: []string{}}
	add := func(s setupStep) setupStep {
		res.Steps = append(res.Steps, s)
		return s
	}

	daemonStep, daemonUp := c.setupDaemon(ctx, errOut, opts, &res)
	add(daemonStep)

	if !daemonUp {
		reason := "needs the daemon"
		if daemonStep.Status == setupSkipped {
			reason = "waits for the daemon"
		}
		agent := setupAgentWithoutDaemon(opts)
		res.Agent = agent
		for _, name := range []string{setupStepAgent, setupStepGitHub, setupStepProject, setupStepFirstRun} {
			add(setupStep{Step: name, Status: setupSkipped, Detail: reason})
		}
		if opts.start != "" {
			add(setupStep{Step: setupStepStart, Status: setupSkipped, Detail: reason})
		}
		finishSetup(&res, opts)
		return res
	}

	agentStep := add(c.setupAgentStep(ctx, opts, &res))
	// Save the chosen agents even when they still need a sign-in: an
	// unsupported agent or a failed check is the only reason not to.
	res.agentSaveable = agentStep.Status == setupOK || agentStep.Status == setupFixed || agentStep.Status == setupNeedsHuman
	add(c.setupGitHubStep(ctx, opts))
	projectStep := add(c.setupProjectStep(ctx, opts, &res))
	add(setupFirstRunStep(projectStep))

	if opts.start != "" {
		add(c.setupStartStep(ctx, opts, &res))
	}
	finishSetup(&res, opts)
	return res
}

// finishSetup adds the verify step, the verdict and the next actions.
func finishSetup(res *setupResult, opts setupOptions) {
	blocked := false
	seen := map[string]bool{}
	for _, s := range res.Steps {
		switch s.Status {
		case setupFailed, setupNeedsHuman:
			blocked = true
		case setupSkipped:
			// An advisory step (GitHub without a GitHub remote) names a fix
			// without blocking ready.
		default:
			continue
		}
		if s.Fix != "" && !seen[s.Fix] {
			seen[s.Fix] = true
			res.NextActions = append(res.NextActions, s.Fix)
		}
	}
	res.Ready = !blocked && !res.pending
	verify := setupStep{Step: setupStepVerify, Status: setupOK, Detail: "Orchestrator is ready to take a task"}
	switch {
	case blocked:
		verify.Status = setupNeedsHuman
		verify.Detail = "not ready: finish next_actions, then run `ao setup` again"
		for _, s := range res.Steps {
			if s.Status == setupFailed {
				verify.Status = setupFailed
				verify.Detail = fmt.Sprintf("not ready: the %s step failed; fix what its detail names, then run `ao setup` again", s.Step)
				break
			}
		}
	case res.pending && opts.check:
		verify.Status = setupSkipped
		verify.Detail = "not ready: run `ao setup` to finish"
		res.NextActions = append(res.NextActions, "ao setup")
	case res.pending:
		verify.Status = setupSkipped
		verify.Detail = "dry run: nothing changed; run `ao setup` to apply"
	}
	if opts.start != "" && res.Ready && res.Task != nil {
		verify.Detail += "; task " + res.Task.Status
	}
	res.Steps = append(res.Steps, verify)
}

// setupDaemon reuses the daemon serving this data dir or starts one through
// the `ao serve` path. It reports whether a daemon is up.
func (c *commandContext) setupDaemon(ctx context.Context, errOut io.Writer, opts setupOptions, res *setupResult) (setupStep, bool) {
	step := setupStep{Step: setupStepDaemon}
	if opts.readOnly() {
		cfg, err := config.Load()
		if err != nil {
			step.Status, step.Detail = setupFailed, err.Error()
			return step, false
		}
		st, err := c.inspectDaemon(ctx)
		if err != nil {
			step.Status, step.Detail = setupFailed, err.Error()
			return step, false
		}
		switch st.State {
		case stateReady:
			if err := checkDaemonDataDir(st, cfg.DataDir); err != nil {
				step.Status, step.Detail, step.Fix = setupFailed, err.Error(), "ao status"
				return step, false
			}
			step.Status = setupOK
			step.Detail = fmt.Sprintf("daemon running at http://%s:%d (pid %d)", config.LoopbackHost, st.Port, st.PID)
			return step, true
		case stateUnhealthy:
			step.Status = setupFailed
			step.Detail = fmt.Sprintf("a daemon (pid %d) is running but is not healthy: %s", st.PID, st.Error)
			step.Fix = "ao stop"
			return step, false
		case stateNotReady:
			res.pending = true
			step.Status = setupSkipped
			step.Detail = fmt.Sprintf("the daemon (pid %d) is still starting; `ao setup` would wait for it", st.PID)
			return step, false
		}
		res.pending = true
		step.Status = setupSkipped
		step.Detail = "no daemon is running; `ao setup` would start one in the background (as `ao serve` does)"
		return step, false
	}

	served, err := c.serve(ctx, io.Discard, errOut, serveOptions{timeout: defaultServeReadyTimeout})
	if err != nil {
		step.Status = setupFailed
		step.Detail = "could not start the daemon: " + err.Error()
		step.Fix = "ao serve"
		return step, false
	}
	step.Status = setupFixed
	step.Detail = fmt.Sprintf("started the daemon at %s (pid %d)", served.URL, served.PID)
	if served.Reused {
		step.Status = setupOK
		step.Detail = fmt.Sprintf("daemon running at %s (pid %d)", served.URL, served.PID)
	}
	return step, true
}

// setupAgentWithoutDaemon names the agent setup would pick when no daemon
// can say which agents are installed.
func setupAgentWithoutDaemon(opts setupOptions) *setupAgent {
	if opts.agent != "" {
		return &setupAgent{ID: opts.agent, Source: "flag", AuthStatus: "unknown", Worker: firstNonEmpty(opts.workerAgent, opts.agent), Orchestrator: firstNonEmpty(opts.orchestratorAgent, opts.agent)}
	}
	if match, ok := registry.DetectCaller(os.Getenv); ok {
		id := string(match.Harness)
		return &setupAgent{ID: id, Source: "caller", DetectedFrom: match.Env, AuthStatus: "unknown", Worker: firstNonEmpty(opts.workerAgent, id), Orchestrator: firstNonEmpty(opts.orchestratorAgent, id)}
	}
	return nil
}

type setupProbe struct {
	Agent struct {
		ID         string `json:"id"`
		Label      string `json:"label"`
		AuthStatus string `json:"authStatus"`
	} `json:"agent"`
	Supported bool `json:"supported"`
	Installed bool `json:"installed"`
}

// setupAgentStep picks the agent (flag, calling agent, first ready) and
// checks that it, and any role overrides, are installed and signed in.
func (c *commandContext) setupAgentStep(ctx context.Context, opts setupOptions, res *setupResult) setupStep {
	step := setupStep{Step: setupStepAgent}
	// The display policy is enough to choose; the chosen agents are probed
	// fresh below.
	readiness, err := c.ensureAgentReadiness(ctx, nil, "display")
	if err != nil {
		step.Status, step.Detail = setupFailed, "could not read agent readiness: "+err.Error()
		return step
	}
	byID := map[string]agentReadinessSnapshot{}
	for _, a := range readiness.Agents {
		byID[a.ID] = a
	}

	agent := &setupAgent{}
	var callerNote string
	// Each role is its flag, else (without --agent) the agent a registered
	// project already saves for it. A default is needed only for a role
	// that has neither.
	worker, orchestrator := opts.workerAgent, opts.orchestratorAgent
	if saved := c.setupSavedAgents(ctx, opts); saved != nil {
		worker = firstNonEmpty(worker, saved.Worker.Agent)
		orchestrator = firstNonEmpty(orchestrator, saved.Orchestrator.Agent)
	}
	switch {
	case opts.workerAgent != "" && opts.orchestratorAgent != "":
		// Both roles are named, so no default (not even --agent) is used or
		// checked.
		agent.ID, agent.Source = opts.workerAgent, "roles"
	case opts.agent != "":
		agent.ID, agent.Source = opts.agent, "flag"
	case worker != "" && orchestrator != "":
		// A registered project keeps the agents it was set up with unless
		// the caller asks for others.
		agent.ID, agent.Source = worker, "project"
		agent.override = opts.workerAgent != "" || opts.orchestratorAgent != ""
	default:
		if match, ok := registry.DetectCaller(os.Getenv); ok {
			id := string(match.Harness)
			if _, supported := byID[id]; supported {
				agent.ID, agent.Source, agent.DetectedFrom = id, "caller", match.Env
				break
			}
			callerNote = fmt.Sprintf("the calling agent (%s from %s) is not supported by this Orchestrator; ", id, match.Env)
		}
		if id := firstReadyAgent(readiness.Agents); id != "" {
			agent.ID, agent.Source = id, "ready"
		}
	}
	if agent.ID == "" {
		step.Status = setupNeedsHuman
		step.Detail = callerNote + "no installed coding agent was found"
		step.Fix = "install a supported coding agent CLI (see `ao agent ls`) and sign it in, then run `ao setup` again"
		return step
	}
	agent.Worker = firstNonEmpty(worker, agent.ID)
	agent.Orchestrator = firstNonEmpty(orchestrator, agent.ID)
	res.Agent = agent

	// Check every distinct agent the project will run.
	res.readiness = readiness.Agents
	var problems, fixes, notes []string
	status := setupOK
	for _, id := range uniqueStrings(agent.ID, agent.Worker, agent.Orchestrator) {
		check, err := c.setupCheckAgent(ctx, id, res)
		if err != nil {
			step.Status, step.Detail = setupFailed, err.Error()
			return step
		}
		if id == agent.ID {
			agent.AuthStatus = check.auth
		}
		status = worseSetupStatus(status, check.status)
		if check.problem != "" {
			problems = append(problems, check.problem+check.alt)
			fixes = append(fixes, check.fix)
			continue
		}
		notes = append(notes, check.note)
	}
	step.Status = status
	chosen := fmt.Sprintf("using %s (%s)", agent.ID, setupAgentSource(agent))
	if agent.Worker != agent.ID || agent.Orchestrator != agent.ID {
		chosen += fmt.Sprintf("; worker %s, orchestrator %s", agent.Worker, agent.Orchestrator)
	}
	if len(problems) > 0 {
		step.Detail = callerNote + chosen + ": " + strings.Join(problems, "; ")
		step.Fix = strings.Join(fixes, "; then ")
		return step
	}
	step.Detail = callerNote + chosen + "; " + strings.Join(notes, "; ")
	return step
}

func setupAgentSource(a *setupAgent) string {
	switch a.Source {
	case "flag":
		return "from --agent"
	case "roles":
		return "from --worker-agent and --orchestrator-agent"
	case "caller":
		return "the calling agent, detected from " + a.DetectedFrom
	case "ready":
		return "the first ready agent"
	case "project":
		if a.override {
			return "the project's saved agents, with your role override"
		}
		return "the project's saved agent"
	}
	return a.Source
}

type setupAgentCheck struct {
	status, auth, problem, fix, note string
	// alt names a ready agent to use instead, when there is one.
	alt string
}

// setupSavedAgents returns the config of the registered project at the
// repository, whose saved agents fill roles the caller did not name. With
// --agent it returns nil: --agent names every role. Lookup problems are left
// to the project step to report.
func (c *commandContext) setupSavedAgents(ctx context.Context, opts setupOptions) *projectConfig {
	if opts.agent != "" || (opts.workerAgent != "" && opts.orchestratorAgent != "") {
		return nil
	}
	path, stop := c.setupRepoPath(ctx, opts)
	if stop != nil {
		return nil
	}
	project, found, err := c.findProjectByPath(ctx, path)
	if err != nil || !found || project.Config == nil {
		return nil
	}
	return project.Config
}

// setupCheckAgent probes one agent now and says whether it can run: not
// supported fails, not installed or signed out needs a person.
func (c *commandContext) setupCheckAgent(ctx context.Context, id string, res *setupResult) (setupAgentCheck, error) {
	if res.checked == nil {
		res.checked = map[string]bool{}
	}
	res.checked[id] = true
	var probe setupProbe
	if err := c.postJSON(ctx, "agents/"+url.PathEscape(id)+"/probe", struct{}{}, &probe); err != nil {
		return setupAgentCheck{}, fmt.Errorf("could not check agent %s: %w", id, err)
	}
	check := setupAgentCheck{status: setupOK, auth: probe.Agent.AuthStatus}
	// The daemon's probe cannot always prove a sign-in either way (it says
	// "configured" for a saved sign-in that may be gone). Then ask the agent's
	// own status command, which can say it is signed out.
	if probe.Supported && probe.Installed && check.auth != "unauthorized" && check.auth != "authorized" && check.auth != "not_applicable" {
		switch c.setupOwnSignIn(ctx, id) {
		case setupSignedOut:
			check.auth = "unauthorized"
		case setupSignedIn:
			check.auth = "authorized"
		}
	}
	switch {
	case !probe.Supported:
		check.status = setupFailed
		check.problem = fmt.Sprintf("agent %q is not supported by this Orchestrator", id)
		check.fix = "pick a supported agent from `ao agent ls` and pass --agent <id>"
	case !probe.Installed:
		check.status = setupNeedsHuman
		check.problem = fmt.Sprintf("agent %s is not installed", id)
		check.alt = setupAlternative(res.readiness, id)
		check.fix = c.setupInstallFix(ctx, id, res)
	case check.auth == "unauthorized":
		check.status = setupNeedsHuman
		check.problem = fmt.Sprintf("agent %s is not signed in", id)
		check.alt = setupAlternative(res.readiness, id)
		check.fix = c.setupSignInFix(ctx, id, res)
	case check.auth == "authorized" || check.auth == "not_applicable":
		check.note = fmt.Sprintf("%s is signed in", id)
	case check.auth == "configured":
		check.note = fmt.Sprintf("%s has a saved sign-in that the first task will confirm", id)
	default:
		check.note = fmt.Sprintf("%s is installed; its sign-in could not be checked, so the first task will confirm it", id)
	}
	return check, nil
}

// firstReadyAgent returns the first agent the daemon calls ready, else the
// first installed agent with a saved (unproven) sign-in.
func firstReadyAgent(agents []agentReadinessSnapshot) string {
	if id := firstReadyAgentExcept(agents, ""); id != "" {
		return id
	}
	for _, a := range agents {
		if a.Installation.State == "installed" && a.Authentication.State == "configured" {
			return a.ID
		}
	}
	return ""
}

// setupReplaceHint turns an alternative into the command that replaces a
// registered project's saved agent, which needs --yes.
func setupReplaceHint(alt string) string {
	if alt == "" {
		return ""
	}
	return strings.Replace(alt, "(or use ", "(or replace it with ", 1)
}

// setupAlternative names a ready agent the person could use instead.
func setupAlternative(agents []agentReadinessSnapshot, skip string) string {
	if alt := firstReadyAgentExcept(agents, skip); alt != "" {
		// --yes: the project already saves (or will save) the agent
		// that is not ready.
		return fmt.Sprintf(" (or use `ao setup --yes --agent %s`)", alt)
	}
	return ""
}

func firstReadyAgentExcept(agents []agentReadinessSnapshot, skip string) string {
	for _, a := range agents {
		if a.ID != skip && a.EffectiveReadiness == "ready" && a.Installation.State == "installed" {
			return a.ID
		}
	}
	return ""
}

type setupAuthPlan struct {
	AgentID          string `json:"agentId"`
	DisplayCommand   string `json:"displayCommand"`
	Guidance         string `json:"guidance"`
	DocumentationURL string `json:"documentationUrl"`
}

// setupAuthPlan returns the daemon's sign-in plan for an agent, reading the
// plans once per run.
func (c *commandContext) setupAuthPlan(ctx context.Context, id string, res *setupResult) setupAuthPlan {
	if res.authPlans == nil {
		var plans struct {
			Plans []setupAuthPlan `json:"plans"`
		}
		res.authPlans = map[string]setupAuthPlan{}
		if err := c.getJSON(ctx, "agents/auth-plans", &plans); err == nil {
			for _, p := range plans.Plans {
				res.authPlans[p.AgentID] = p
			}
		}
	}
	return res.authPlans[id]
}

// setupSignInFix is the agent's own login command, verbatim, so an agent can
// show it to the person as is.
func (c *commandContext) setupSignInFix(ctx context.Context, id string, res *setupResult) string {
	plan := c.setupAuthPlan(ctx, id, res)
	switch {
	case strings.TrimSpace(plan.DisplayCommand) != "":
		return strings.TrimSpace(plan.DisplayCommand)
	case strings.TrimSpace(plan.Guidance) != "":
		return strings.TrimSpace(plan.Guidance)
	case strings.TrimSpace(plan.DocumentationURL) != "":
		return fmt.Sprintf("sign %s in (see %s)", id, strings.TrimSpace(plan.DocumentationURL))
	}
	return fmt.Sprintf("sign %s in", id)
}

type setupSignIn int

const (
	setupSignInUnknown setupSignIn = iota
	setupSignedIn
	setupSignedOut
)

// setupAuthStatusCommand is an agent's own sign-in status command. With
// loggedInField set, its JSON output names the answer; otherwise exit 0 means
// signed in, and a failure whose output contains signedOutText means signed
// out. binary is the name ResolveAgentBinary knows, or an absolute path.
type setupAuthStatusCommand struct {
	binary        string
	args          []string
	loggedInField string
	signedOutText string
}

// setupAuthStatusCommands maps registry ids to their own status commands, for
// agents whose daemon probe can report an unproven sign-in. Keep it a plain
// data list: one row per agent.
var setupAuthStatusCommands = map[string]setupAuthStatusCommand{
	"claude-code": {binary: "claude", args: []string{"auth", "status"}, loggedInField: "loggedIn"},
	"codex":       {binary: "codex", args: []string{"login", "status"}, signedOutText: "Not logged in"},
}

// setupOwnSignIn runs the agent's own status command. Anything unclear (no
// binary, a timeout, unexpected output) stays unknown.
func (c *commandContext) setupOwnSignIn(ctx context.Context, id string) setupSignIn {
	cmd, ok := setupAuthStatusCommands[id]
	if !ok {
		return setupSignInUnknown
	}
	path := cmd.binary
	if !filepath.IsAbs(path) {
		resolved, err := c.deps.ResolveAgentBinary(ctx, cmd.binary)
		if err != nil || resolved == "" {
			if resolved, err = c.deps.LookPath(cmd.binary); err != nil || resolved == "" {
				return setupSignInUnknown
			}
		}
		path = resolved
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, runErr := c.deps.CommandOutput(runCtx, path, cmd.args...)
	if runCtx.Err() != nil {
		return setupSignInUnknown
	}
	if cmd.loggedInField != "" {
		// The output can carry warning lines around the JSON object.
		text := string(out)
		start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
		if start < 0 || end < start {
			return setupSignInUnknown
		}
		var report map[string]any
		if json.Unmarshal([]byte(text[start:end+1]), &report) != nil {
			return setupSignInUnknown
		}
		switch report[cmd.loggedInField] {
		case true:
			return setupSignedIn
		case false:
			return setupSignedOut
		}
		return setupSignInUnknown
	}
	switch {
	case runErr == nil:
		return setupSignedIn
	case cmd.signedOutText != "" && strings.Contains(string(out), cmd.signedOutText):
		return setupSignedOut
	}
	return setupSignInUnknown
}

// setupInstallFix tells the person how to install an agent. Setup never
// installs one itself.
func (c *commandContext) setupInstallFix(ctx context.Context, id string, res *setupResult) string {
	label := id
	for _, a := range res.readiness {
		if a.ID == id && a.Label != "" {
			label = a.Label
		}
	}
	fix := "install the " + label
	if !strings.HasSuffix(label, " CLI") {
		fix += " CLI"
	}
	if doc := strings.TrimSpace(c.setupAuthPlan(ctx, id, res).DocumentationURL); doc != "" {
		fix += " (see " + doc + ")"
	}
	return fix + ", then run `ao setup` again"
}

type setupGitHubAuth struct {
	Satisfied bool   `json:"satisfied"`
	Detail    string `json:"detail"`
}

// setupGitHubStep reads the daemon's GitHub check. It never runs a login:
// `gh auth login` is the person's step. GitHub blocks ready only for a
// repository whose remote is on GitHub, where agents need gh to open pull
// requests. Elsewhere it is advisory, like the daemon's own check: the step
// is skipped with its fix listed in next_actions, and ready is unaffected.
func (c *commandContext) setupGitHubStep(ctx context.Context, opts setupOptions) setupStep {
	step := setupStep{Step: setupStepGitHub}
	blocking := c.setupRepoOnGitHub(ctx, opts)
	notOK := setupSkipped
	if blocking {
		notOK = setupNeedsHuman
	}
	var auth setupGitHubAuth
	if err := c.getJSON(ctx, "system/github-auth", &auth); err != nil {
		step.Status, step.Detail = setupSkipped, "could not check GitHub access: "+err.Error()
		if blocking {
			step.Status = setupFailed
		}
		return step
	}
	if auth.Satisfied {
		step.Status, step.Detail = setupOK, "GitHub CLI is signed in"
		return step
	}
	step.Status = notOK
	why := "agents use it to open pull requests"
	if !blocking {
		why += "; this repository has no GitHub remote, so setup does not need it"
	}
	if path, err := c.deps.LookPath("gh"); err != nil || path == "" {
		step.Detail = "the GitHub CLI (gh) is not installed; " + why
		step.Fix = "install the GitHub CLI (https://cli.github.com), then run `gh auth login`"
		return step
	}
	step.Detail = "the GitHub CLI is not signed in; " + why
	step.Fix = "gh auth login"
	return step
}

// setupRepoOnGitHub reports whether the repository setup will register has a
// remote on GitHub. Anything unclear counts as not.
func (c *commandContext) setupRepoOnGitHub(ctx context.Context, opts setupOptions) bool {
	path, stop := c.setupRepoPath(ctx, opts)
	if stop != nil {
		return false
	}
	out, err := c.deps.CommandOutputInDir(ctx, path, "git", "remote", "-v")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && isGitHubHost(remoteHost(fields[1])) {
			return true
		}
	}
	return false
}

// remoteHost returns the host of a git remote URL: scheme URLs
// (https://host/..., ssh://git@host:22/...) and scp-style ones (git@host:path).
func remoteHost(remote string) string {
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	host, _, ok := strings.Cut(remote, ":")
	if !ok || strings.Contains(host, "/") {
		return "" // a local path
	}
	if _, after, found := strings.Cut(host, "@"); found {
		host = after
	}
	return strings.ToLower(host)
}

func isGitHubHost(host string) bool {
	return host == "github.com" || host == "ssh.github.com" || host == "www.github.com"
}

// setupProjectStep registers the repository, or reuses its registration, and
// gives a repository with no remote a default branch.
func (c *commandContext) setupProjectStep(ctx context.Context, opts setupOptions, res *setupResult) setupStep {
	step := setupStep{Step: setupStepProject}
	path, fixStep := c.setupRepoPath(ctx, opts)
	if fixStep != nil {
		return *fixStep
	}

	existing, found, err := c.findProjectByPath(ctx, path)
	if err != nil {
		step.Status, step.Detail = setupFailed, "could not list projects: "+err.Error()
		return step
	}
	var actions []string
	project := existing
	reused := found
	if !found {
		if opts.readOnly() {
			if repoStep := c.setupCheckRepo(ctx, path); repoStep != nil {
				return *repoStep
			}
			res.pending = true
			step.Status = setupSkipped
			step.Detail = fmt.Sprintf("%s is not registered; `ao setup` would register it", path)
			if res.Agent != nil && res.agentSaveable {
				step.Detail += fmt.Sprintf(" with worker %s and orchestrator %s", res.Agent.Worker, res.Agent.Orchestrator)
			}
			res.Project = &setupProject{Path: path}
			// Preview the base-branch rule the real run applies after
			// registering, so the preview needs the same person the run does.
			preview := projectDetails{Path: path}
			if origin, err := c.deps.CommandOutputInDir(ctx, path, "git", "remote", "get-url", "origin"); err == nil {
				preview.Repo = strings.TrimSpace(string(origin))
			}
			if plan := c.setupBaseBranch(ctx, preview); plan.why != "" {
				if plan.branch == "" {
					step.Status = setupNeedsHuman
					step.Detail = fmt.Sprintf("%s is not registered; %s, so Orchestrator could not pick a base branch", path, plan.why)
					step.Fix = plan.previewFix(path)
					return step
				}
				step.Detail += fmt.Sprintf(", and set its default branch to %s (%s)", plan.branch, plan.why)
			}
			return step
		}
		req := addProjectRequest{Path: path}
		if res.Agent != nil && res.agentSaveable {
			req.Config = &projectConfig{
				Worker:       roleOverride{Agent: res.Agent.Worker},
				Orchestrator: roleOverride{Agent: res.Agent.Orchestrator},
			}
		}
		var added projectResult
		err := c.postJSON(ctx, "projects", req, &added)
		var apiErr apiResponseError
		switch {
		case errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "PATH_ALREADY_REGISTERED":
			// Registered under another spelling of the path (a symlink, a
			// different case): find and reuse it.
			existing, found, err = c.findProjectByPath(ctx, path)
			if err != nil || !found {
				step.Status, step.Detail = setupFailed, "the path is already registered, but its project could not be found: "+apiErr.Error()
				step.Fix = "ao project ls"
				return step
			}
			project, reused = existing, true
		case errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "NOT_A_GIT_REPO":
			step.Status = setupNeedsHuman
			step.Detail = fmt.Sprintf("%s is not a git repository", path)
			step.Fix = fmt.Sprintf("git -C %s init && git -C %s commit --allow-empty -m \"initial commit\"", shellQuote(path), shellQuote(path))
			return step
		case errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "PROJECT_UNBORN":
			step.Status = setupNeedsHuman
			step.Detail = fmt.Sprintf("%s has no commits yet; Orchestrator needs one to create agent workspaces", path)
			step.Fix = fmt.Sprintf("git -C %s commit --allow-empty -m \"initial commit\"", shellQuote(path))
			return step
		case err != nil:
			step.Status, step.Detail = setupFailed, "could not register the project: "+err.Error()
			return step
		default:
			project = added.Project
			actions = append(actions, fmt.Sprintf("registered %s as project %s", project.Path, project.ID))
		}
	}

	if reused && res.agentSaveable {
		agentStep, changed := c.setupReconcileAgents(ctx, opts, res, &project)
		if agentStep != nil {
			return *agentStep
		}
		if changed != "" {
			actions = append(actions, changed)
		}
	}

	// An "auto" default branch is resolved from origin's HEAD when a session
	// starts. When that cannot work, setupBaseBranch picks one the way
	// `ao project set-config --default-branch` would set it, or asks.
	if project.DefaultBranch == "" || project.DefaultBranch == "auto" {
		if plan := c.setupBaseBranch(ctx, project); plan.why != "" {
			branch, why := plan.branch, plan.why
			switch {
			case branch == "":
				step.Status = setupNeedsHuman
				step.Detail = fmt.Sprintf("project %s: %s, so Orchestrator cannot pick a base branch", project.ID, why)
				step.Fix = fmt.Sprintf("ao project set-config %s --default-branch <branch>", shellQuote(project.ID))
				res.Project = setupProjectFrom(project, reused)
				return step
			case opts.readOnly():
				res.pending = true
				step.Status = setupSkipped
				step.Detail = fmt.Sprintf("project %s: %s; `ao setup` would set its default branch to %s", project.ID, why, branch)
				res.Project = setupProjectFrom(project, reused)
				return step
			}
			cfg, err := c.currentProjectConfig(ctx, project.ID)
			if err != nil {
				step.Status, step.Detail = setupFailed, "could not read the project config: "+err.Error()
				step.Fix = fmt.Sprintf("ao project set-config %s --default-branch %s", shellQuote(project.ID), shellQuote(branch))
				return step
			}
			cfg.DefaultBranch = branch
			var updated projectResult
			if err := c.putJSON(ctx, "projects/"+url.PathEscape(project.ID)+"/config", setConfigRequest{Config: cfg}, &updated); err != nil {
				step.Status, step.Detail = setupFailed, "could not set the default branch: "+err.Error()
				step.Fix = fmt.Sprintf("ao project set-config %s --default-branch %s", shellQuote(project.ID), shellQuote(branch))
				return step
			}
			project.DefaultBranch = branch
			if updated.Project.ID != "" && updated.Project.DefaultBranch != "" {
				project.DefaultBranch = updated.Project.DefaultBranch
			}
			actions = append(actions, fmt.Sprintf("set default branch to %s (%s)", branch, why))
		}
	}

	res.Project = setupProjectFrom(project, reused)
	switch {
	case len(actions) > 0 && opts.readOnly():
		step.Status = setupSkipped
		step.Detail = fmt.Sprintf("project %s is already registered; `ao setup` would ", project.ID) + strings.TrimPrefix(strings.Join(actions, "; "), "would ")
		return step
	case len(actions) > 0:
		step.Status = setupFixed
		step.Detail = strings.Join(actions, "; ")
		if reused {
			step.Detail = fmt.Sprintf("project %s is already registered; ", project.ID) + step.Detail
		}
	default:
		step.Status = setupOK
		step.Detail = fmt.Sprintf("project %s is already registered at %s", project.ID, project.Path)
	}
	return step
}

func setupProjectFrom(p projectDetails, reused bool) *setupProject {
	return &setupProject{ID: p.ID, Path: p.Path, DefaultBranch: p.DefaultBranch, Reused: reused}
}

// setupReconcileAgents fills in agents a registered project does not name
// yet. It replaces agents the project already names only when the caller
// asked for different ones with --agent/--worker-agent/--orchestrator-agent
// and passed --yes. It returns a step only when setup must stop.
func (c *commandContext) setupReconcileAgents(ctx context.Context, opts setupOptions, res *setupResult, project *projectDetails) (*setupStep, string) {
	if res.Agent == nil {
		return nil, ""
	}
	saved := projectConfig{}
	if project.Config != nil {
		saved = *project.Config
	}
	explicitWorker := opts.workerAgent != "" || opts.agent != ""
	explicitOrch := opts.orchestratorAgent != "" || opts.agent != ""
	want := saved
	var changes, conflicts []string
	consider := func(role string, field *string, value string, explicit bool) {
		switch {
		case *field == value:
		case *field == "":
			*field = value
			changes = append(changes, fmt.Sprintf("%s agent %s", role, value))
		case explicit && opts.yes:
			changes = append(changes, fmt.Sprintf("%s agent %s (was %s)", role, value, *field))
			*field = value
		case explicit:
			conflicts = append(conflicts, fmt.Sprintf("its %s agent is %s, not %s", role, *field, value))
		}
	}
	consider("worker", &want.Worker.Agent, res.Agent.Worker, explicitWorker)
	consider("orchestrator", &want.Orchestrator.Agent, res.Agent.Orchestrator, explicitOrch)
	// Report what the project will really run.
	res.Agent.Worker = firstNonEmpty(want.Worker.Agent, res.Agent.Worker)
	res.Agent.Orchestrator = firstNonEmpty(want.Orchestrator.Agent, res.Agent.Orchestrator)

	if len(conflicts) == 0 {
		// The project keeps agents the agent step did not check: check them
		// too, so ready means the project's own agents can run.
		for _, id := range uniqueStrings(res.Agent.Worker, res.Agent.Orchestrator) {
			if res.checked[id] {
				continue
			}
			check, err := c.setupCheckAgent(ctx, id, res)
			if err != nil {
				step := setupStep{Step: setupStepProject, Status: setupFailed, Detail: err.Error()}
				return &step, ""
			}
			if check.problem != "" {
				step := setupStep{
					Step:   setupStepProject,
					Status: check.status,
					Detail: fmt.Sprintf("project %s is already registered and runs %s: %s", project.ID, id, check.problem) + setupReplaceHint(check.alt),
					Fix:    check.fix,
				}
				res.Project = setupProjectFrom(*project, true)
				return &step, ""
			}
		}
	}
	if len(conflicts) > 0 {
		step := setupStep{
			Step:   setupStepProject,
			Status: setupNeedsHuman,
			Detail: fmt.Sprintf("project %s is already registered and %s", project.ID, strings.Join(conflicts, "; ")),
			Fix:    "run the same `ao setup` command with --yes to replace the saved agents, or leave out the agent flags to keep them",
		}
		res.Project = setupProjectFrom(*project, true)
		return &step, ""
	}
	if len(changes) == 0 {
		return nil, ""
	}
	summary := "set " + strings.Join(changes, " and ")
	if opts.readOnly() {
		res.pending = true
		return nil, "would " + summary
	}
	if project.Config == nil {
		// A degraded project reports no config; writing over it would wipe it.
		cfg, err := c.currentProjectConfig(ctx, project.ID)
		if err != nil {
			step := setupStep{Step: setupStepProject, Status: setupFailed, Detail: "could not read the project config: " + err.Error()}
			return &step, ""
		}
		cfg.Worker.Agent, cfg.Orchestrator.Agent = want.Worker.Agent, want.Orchestrator.Agent
		want = cfg
	}
	var updated projectResult
	if err := c.putJSON(ctx, "projects/"+url.PathEscape(project.ID)+"/config", setConfigRequest{Config: want}, &updated); err != nil {
		step := setupStep{Step: setupStepProject, Status: setupFailed, Detail: "could not save the project's agents: " + err.Error()}
		return &step, ""
	}
	project.Config = &want
	return nil, summary
}

// setupRepoPath resolves --path, or the git repository around the current
// directory, to an absolute repository root.
func (c *commandContext) setupRepoPath(ctx context.Context, opts setupOptions) (string, *setupStep) {
	start := opts.path
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", &setupStep{Step: setupStepProject, Status: setupFailed, Detail: "could not read the current directory: " + err.Error()}
		}
		start = cwd
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", &setupStep{Step: setupStepProject, Status: setupFailed, Detail: err.Error()}
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", &setupStep{Step: setupStepProject, Status: setupNeedsHuman, Detail: fmt.Sprintf("%s is not a directory", abs), Fix: "pass --path <repo> with the repository to set up"}
	}
	out, err := c.deps.CommandOutputInDir(ctx, abs, "git", "rev-parse", "--show-toplevel")
	top := strings.TrimSpace(string(out))
	if err != nil || top == "" {
		if opts.path != "" {
			// Let the daemon decide: it reports NOT_A_GIT_REPO with its own wording.
			return abs, nil
		}
		return "", &setupStep{
			Step:   setupStepProject,
			Status: setupNeedsHuman,
			Detail: fmt.Sprintf("%s is not inside a git repository", abs),
			Fix:    "run `ao setup --path <repo>` with the repository to set up, or `git init` and make a first commit here",
		}
	}
	top = filepath.Clean(top)
	// A repository at the home directory or the filesystem root is almost
	// always a dotfiles repo around the folder the person meant.
	if home, err := os.UserHomeDir(); (err == nil && sameDir(top, home)) || top == filepath.Dir(top) {
		return "", &setupStep{
			Step:   setupStepProject,
			Status: setupNeedsHuman,
			Detail: fmt.Sprintf("%s is inside the git repository at %s, which looks like a home-folder or system repository, not a project", abs, top),
			Fix:    "run `ao setup --path <repo>` with the repository to set up",
		}
	}
	return top, nil
}

// setupCheckRepo reports, without registering anything, what the daemon
// would refuse: a folder that is not a git repository, or one with no commit.
func (c *commandContext) setupCheckRepo(ctx context.Context, path string) *setupStep {
	if out, err := c.deps.CommandOutputInDir(ctx, path, "git", "rev-parse", "--show-toplevel"); err != nil || strings.TrimSpace(string(out)) == "" {
		return &setupStep{Step: setupStepProject, Status: setupNeedsHuman,
			Detail: fmt.Sprintf("%s is not a git repository", path),
			Fix:    fmt.Sprintf("git -C %s init && git -C %s commit --allow-empty -m \"initial commit\"", shellQuote(path), shellQuote(path))}
	}
	if _, err := c.deps.CommandOutputInDir(ctx, path, "git", "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return &setupStep{Step: setupStepProject, Status: setupNeedsHuman,
			Detail: fmt.Sprintf("%s has no commits yet; Orchestrator needs one to create agent workspaces", path),
			Fix:    fmt.Sprintf("git -C %s commit --allow-empty -m \"initial commit\"", shellQuote(path))}
	}
	return nil
}

// setupCurrentBranch returns the checked-out branch, or "" when HEAD is
// detached.
func (c *commandContext) setupCurrentBranch(ctx context.Context, path string) string {
	out, err := c.deps.CommandOutputInDir(ctx, path, "git", "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// setupRemoteHeadTimeout bounds the one network call setup makes to learn a
// remote's default branch.
var setupRemoteHeadTimeout = 15 * time.Second

// setupBranchPlan is what setup does about a project's "auto" default branch:
// nothing (why is empty), set branch, or ask a person (why set, branch empty).
type setupBranchPlan struct {
	branch string
	why    string
	remote bool
}

// previewFix is the command a person runs before setup registers the repo.
func (p setupBranchPlan) previewFix(path string) string {
	if p.remote {
		return fmt.Sprintf("git -C %s remote set-head origin <branch>", shellQuote(path))
	}
	return fmt.Sprintf("git -C %s switch <branch>", shellQuote(path))
}

// setupBaseBranch decides the base branch for an "auto" default branch, which
// a session start resolves from origin's HEAD.
//   - No remote: the checked-out branch is the only base there is.
//   - A remote whose HEAD resolves (cached, or ls-remote answers): nothing to
//     do, the session start resolves it the same way.
//   - A remote whose HEAD does not resolve: origin/main, else origin/master,
//     when the repo has them locally. Never the checked-out branch, which may
//     be a feature branch every worker would then start from. Otherwise a
//     person must say.
func (c *commandContext) setupBaseBranch(ctx context.Context, project projectDetails) setupBranchPlan {
	if project.Repo == "" {
		branch := c.setupCurrentBranch(ctx, project.Path)
		why := "the repository has no remote"
		if branch == "" {
			why += " and no branch is checked out"
		}
		return setupBranchPlan{branch: branch, why: why}
	}
	if _, err := c.deps.CommandOutputInDir(ctx, project.Path, "git", "rev-parse", "-q", "--verify", "refs/remotes/origin/HEAD"); err == nil {
		return setupBranchPlan{}
	}
	if setupRemoteHeadResolves(ctx, project.Path) {
		return setupBranchPlan{}
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := c.deps.CommandOutputInDir(ctx, project.Path, "git", "rev-parse", "-q", "--verify", "refs/remotes/origin/"+candidate); err == nil {
			return setupBranchPlan{branch: candidate, why: "origin's default branch could not be resolved; using origin/" + candidate, remote: true}
		}
	}
	return setupBranchPlan{why: "origin's default branch could not be resolved (the remote was never fetched or is unreachable)", remote: true}
}

// setupRemoteHeadResolves asks origin for its HEAD without ever prompting:
// no terminal credential prompt, and SSH in batch mode (unless the person set
// their own SSH command, which is kept).
func setupRemoteHeadResolves(ctx context.Context, path string) bool {
	runCtx, cancel := context.WithTimeout(ctx, setupRemoteHeadTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(runCtx, "git", "-c", "core.askPass=true", "ls-remote", "--symref", "--", "origin", "HEAD")
	cmd.Dir = path
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		if out, err := aoprocess.CommandContext(runCtx, "git", "-C", path, "config", "core.sshCommand").Output(); err != nil || strings.TrimSpace(string(out)) == "" {
			cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
		}
	}
	out, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(string(out), "ref: refs/heads/")
}

// findProjectByPath returns the registered project whose path is the same
// directory as path.
func (c *commandContext) findProjectByPath(ctx context.Context, path string) (projectDetails, bool, error) {
	// The list carries each project's path, so only a match is read in full.
	// A summary without one (an older daemon) is read to compare.
	var list struct {
		Projects []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"projects"`
	}
	if err := c.getJSON(ctx, "projects", &list); err != nil {
		return projectDetails{}, false, err
	}
	for _, summary := range list.Projects {
		if summary.Path != "" && !sameDir(summary.Path, path) {
			continue
		}
		project, err := c.fetchProjectDetails(ctx, summary.ID)
		if err != nil {
			return projectDetails{}, false, err
		}
		if project.Path != "" && sameDir(project.Path, path) {
			return project, true, nil
		}
	}
	return projectDetails{}, false, nil
}

// setupFirstRunStep reports the desktop app's first-run state: a registered
// project makes the app skip its first-run screens, so there is nothing to
// write.
func setupFirstRunStep(project setupStep) setupStep {
	step := setupStep{Step: setupStepFirstRun}
	switch project.Status {
	case setupOK, setupFixed:
		step.Status = setupOK
		step.Detail = "a project is registered, so the Orchestrator app skips its first-run screens"
		if app := darwinApplicationsBundle(); app != "" {
			if _, err := os.Stat(app); err == nil {
				step.Detail += fmt.Sprintf("; open it with: open %q", app)
			}
		}
	default:
		step.Status = setupSkipped
		step.Detail = "waits for a registered project"
	}
	return step
}

// setupStartStep starts the task through the `ao task start` code and waits
// until it has started, failed or needs the user.
func (c *commandContext) setupStartStep(ctx context.Context, opts setupOptions, res *setupResult) setupStep {
	step := setupStep{Step: setupStepStart}
	for _, s := range res.Steps {
		if s.Status == setupFailed || s.Status == setupNeedsHuman {
			step.Status, step.Detail = setupSkipped, "setup is not ready, so the task was not started"
			return step
		}
	}
	target := "the project's orchestrator"
	if opts.worker {
		target = "one worker"
	}
	if opts.dryRun || res.pending || res.Project == nil || res.Project.ID == "" {
		res.pending = true
		step.Status, step.Detail = setupSkipped, "dry run: would start the task on "+target
		return step
	}
	project, err := c.fetchProjectDetails(ctx, res.Project.ID)
	if err != nil {
		step.Status, step.Detail = setupFailed, "could not read the project: "+err.Error()
		return step
	}
	topts := taskStartOptions{project: project.ID, worker: opts.worker, wait: true, timeout: defaultTaskTimeout}
	task := c.createTask(ctx, project, opts.start, topts)
	if task.Status == taskStatusStarting || task.Status == taskStatusQueued || task.Status == taskStatusRunning {
		task = c.waitTask(ctx, task, topts.timeout)
	}
	if task.SessionIDs == nil {
		task.SessionIDs = []string{}
	}
	res.Task = &task
	switch task.Status {
	case taskStatusFailed:
		// The exit code comes from the task (1); the step stays out of
		// next_actions unless the task names a fix.
		step.Status, step.Detail, step.Fix = setupFailed, "the task failed: "+task.Reason, task.Fix
		// `ao task` builds the fix from the harness that really failed (a
		// reused orchestrator may run another agent than the project saves).
		// Keep that, and show its login command verbatim, as the agent step
		// does.
		if cmd, ok := strings.CutPrefix(task.Fix, "run `"); ok {
			if cmd, ok = strings.CutSuffix(cmd, "`, then retry"); ok && !strings.Contains(cmd, "`") {
				step.Fix = cmd
			}
		}
	case taskStatusNeedsInput:
		step.Status, step.Detail, step.Fix = setupNeedsHuman, "the task is waiting on you: "+task.Reason, task.Fix
		if step.Fix == "" && len(task.SessionIDs) > 0 {
			// The task already exists: starting it again would run it twice.
			step.Fix = fmt.Sprintf("answer the agent in Orchestrator (session %s); do not start the task again", task.SessionIDs[0])
		}
	case taskStatusQueued:
		// Accepted behind the agent's current turn: it will run, so it is
		// not a failure and must not be started again (exit 4, as ao task).
		step.Status = setupFixed
		step.Detail = fmt.Sprintf("task %s is queued on %s behind the agent's current turn and will run; do not start it again (follow it with `ao task wait %s`)", task.TaskID, target, task.TaskID)
	default:
		step.Status = setupFixed
		step.Detail = fmt.Sprintf("started task %s on %s (%s)", task.TaskID, target, task.Status)
	}
	return step
}

func writeSetupResult(w io.Writer, res setupResult, opts setupOptions) error {
	if opts.json {
		return writeJSON(w, res)
	}
	var b strings.Builder
	for _, s := range res.Steps {
		fmt.Fprintf(&b, "%-13s %-10s %s\n", "["+s.Status+"]", s.Step, s.Detail)
		if s.Fix != "" && (s.Status == setupNeedsHuman || s.Status == setupFailed || s.Status == setupSkipped) {
			fmt.Fprintf(&b, "%-13s %-10s fix: %s\n", "", "", s.Fix)
		}
	}
	if res.Task != nil && res.Task.TaskID != "" {
		fmt.Fprintf(&b, "\ntask %s: %s\n", res.Task.TaskID, res.Task.Status)
		fmt.Fprintf(&b, "  follow it with: ao task status %s\n", res.Task.TaskID)
	}
	switch {
	case res.Ready:
		b.WriteString("\nOrchestrator is ready.")
		if opts.start == "" {
			b.WriteString(" Start a task with: ao task start \"<task>\"")
		}
		b.WriteString("\n")
		if len(res.NextActions) > 0 {
			b.WriteString("Optional:\n")
			for i, a := range res.NextActions {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, a)
			}
		}
	case len(res.NextActions) > 0:
		b.WriteString("\nNot ready yet. Next steps:\n")
		for i, a := range res.NextActions {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, a)
		}
	case opts.readOnly():
		b.WriteString("\nNothing was changed. Run `ao setup` to apply.\n")
	default:
		b.WriteString("\nNot ready yet: see the failed step above.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func worseSetupStatus(a, b string) string {
	rank := map[string]int{setupOK: 0, setupFixed: 0, setupSkipped: 1, setupNeedsHuman: 2, setupFailed: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func uniqueStrings(values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// shellQuote quotes a path for a command a person will paste.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
