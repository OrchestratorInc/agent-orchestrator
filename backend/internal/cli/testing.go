package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
)

// testingCLI is the shared plumbing for `ao testing` verbs: each verb is one
// HTTP call to /api/v1/testing/... attributed to the AO_SESSION_ID worker.
type testingCLI struct {
	c      *commandContext
	asJSON bool
	leg    string
}

func verb(use, short string, args cobra.PositionalArgs, run func(*cobra.Command, []string) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: run}
}

func newTestingCommand(ctx *commandContext) *cobra.Command {
	t := &testingCLI{c: ctx}
	cmd := verb("testing", "Test a pull request: pin it, build base and head, drive the app, submit a verdict", noArgs, nil)
	cmd.PersistentFlags().BoolVar(&t.asJSON, "json", false, "print the structured response as JSON")
	cmd.PersistentFlags().StringVar(&t.leg, "leg", "", "leg a driving verb acts on (base or head); needed when both are launched")

	cmd.AddCommand(t.startCommand())
	cmd.AddCommand(verb("intake <pr-url>", "Start a testing run for a GitHub pull request (25 minutes from when the builds are ready)", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		return testingCallTimeout(t, cmd, "testing/intake", map[string]any{"prUrl": a[0]}, runLine, testingSetupCallTimeout)
	}))
	cmd.AddCommand(verb("prepare", "Prepare the base and head clones and builds", noArgs, func(cmd *cobra.Command, _ []string) error {
		return testingCallTimeout(t, cmd, "testing/prepare", map[string]any{}, runLine, prepareCallTimeout)
	}))
	cmd.AddCommand(verb("launch <base|head>", "Launch a private copy of the base or head build", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		return testingCallTimeout(t, cmd, "testing/launch", map[string]any{"leg": a[0]}, func(r litmus.Run) string { return launchLine(r, a[0]) }, launchCallTimeout)
	}))
	cmd.AddCommand(verb("status", "Show the run, its deadline and its checks", noArgs, func(cmd *cobra.Command, _ []string) error {
		return testingCall(t, cmd, "testing/status", nil, func(out litmus.RunStatus) string {
			lines := make([]string, 0, 1+len(out.Run.Legs)+len(out.Checks))
			lines = append(lines, runLine(out.Run))
			if out.OwnerLogins != "" {
				lines = append(lines, out.OwnerLogins)
			}
			for _, f := range out.InFlight {
				lines = append(lines, fmt.Sprintf("%s in flight for %s", f.Verb, (time.Duration(f.Seconds)*time.Second).String()))
			}
			for _, l := range out.Run.Legs {
				lines = append(lines, legLine(l))
			}
			for _, c := range out.Checks {
				lines = append(lines, fmt.Sprintf("%s (%d receipts)", checkLine(c), len(c.Receipts)))
			}
			return strings.Join(lines, "\n")
		})
	}))
	cmd.AddCommand(t.checkCommand(), t.compareCommand(), t.replayCommand(), t.submitCommand())
	cmd.AddCommand(verb("cleanup", "Stop the run's targets and remove its private state", noArgs, func(cmd *cobra.Command, _ []string) error {
		return testingCall(t, cmd, "testing/cleanup", map[string]any{}, runLine)
	}))
	cmd.AddCommand(t.recipeCommand(), t.waitCommand(), t.recordCommand())
	cmd.AddCommand(t.pruneCommand())
	cmd.AddCommand(t.runsCommand(), t.showCommand(), t.rerunCommand(), t.decideCommand("post", "Publish the evidence and post the comment of a run awaiting approval"),
		t.decideCommand("discard", "Close a run awaiting approval without posting its comment"), t.settingsCommand())

	for _, d := range []struct {
		use, short string
		args       cobra.PositionalArgs
	}{
		{"snapshot", "Read the UI as text with element refs", noArgs},
		{"click <ref>", "Click an element by ref", exactArgs(1)},
		{"fill <ref> <text>", "Replace an input's text", exactArgs(2)},
		{"type <ref> <text>", "Type text into an element", exactArgs(2)},
		{"press <key>", "Press a key", exactArgs(1)},
		{"screenshot [path]", "Save a screenshot", atMostOneArg},
		{"console", "Print console output", noArgs},
		{"errors", "Print page errors", noArgs},
		{"network", "Print network requests", noArgs},
		{"tab [list]", "List the target's tabs", atMostOneArg},
	} {
		action := strings.Fields(d.use)[0]
		cmd.AddCommand(verb(d.use, d.short, d.args, func(cmd *cobra.Command, a []string) error { return t.drive(cmd, action, a) }))
	}
	return cmd
}

// startCommand spawns the worker that tests a PR. The worker, not this command, makes the run.
func (t *testingCLI) startCommand() *cobra.Command {
	var pr, projectID, harness string
	var noComment, noEvidence bool
	cmd := verb("start --pr <url>", "Spawn a worker that tests a pull request (claude-code or codex)", noArgs, func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(pr) == "" {
			return usageError{errors.New("usage: --pr is required")}
		}
		project, err := t.c.resolveSpawnProject(cmd.Context(), projectID)
		if err != nil {
			return err
		}
		var out litmus.Started
		body := withCaller(map[string]any{"projectId": project.ID, "prUrl": pr})
		if h := strings.TrimSpace(harness); h != "" { // else the daemon picks: the project's Litmus setting, its worker agent, claude-code
			if !litmus.Supported(h) {
				return usageError{fmt.Errorf("testing workers run on %s or %s, not %q", litmus.Harness, litmus.HarnessCodex, h)}
			}
			body["harness"] = h
		}
		if noComment {
			body["noComment"] = true
		}
		if noEvidence {
			body["noEvidence"] = true
		}
		if err := t.c.postJSON(cmd.Context(), "testing/start", body, &out); err != nil {
			return err
		}
		return t.show(cmd, out, fmt.Sprintf("spawned session %s %q", out.SessionID, out.Name))
	})
	f := cmd.Flags()
	f.StringVar(&pr, "pr", "", "GitHub pull request URL (or owner/repo#N) to test")
	f.StringVar(&projectID, "project", "", "Project id the worker runs in (default: AO_PROJECT_ID or the current registered repo)")
	f.StringVar(&harness, "harness", "", "Agent harness, claude-code or codex (default: the project's Litmus harness setting, then its worker agent if supported, then claude-code)")
	f.BoolVar(&noComment, "no-comment", false, "Do not post the verdict on the pull request (it is still written to comment.md in the run dir)")
	f.BoolVar(&noEvidence, "no-evidence", false, "Do not publish screenshots and GIFs to the repo's litmus/evidence branch for the comment")
	return cmd
}

func (t *testingCLI) checkCommand() *cobra.Command {
	check := verb("check", "Plan and bracket the checks a verdict is built from", noArgs, nil)
	var expect, result, note string
	var sameFault bool
	var shot int
	add := verb("add <name>", "Add a check with the observable you expect", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		if strings.TrimSpace(expect) == "" {
			return usageError{errors.New("usage: --expect is required")}
		}
		if t.leg != "" && t.leg != "base" && t.leg != "head" {
			return usageError{errors.New("usage: --leg must be base or head")}
		}
		body := map[string]any{"name": a[0], "expect": expect}
		if t.leg != "" {
			body["leg"] = t.leg
		}
		return testingCall(t, cmd, "testing/checks", body, checkLine)
	})
	add.Flags().StringVar(&expect, "expect", "", "Observable that proves the check passes (add it with --leg base to record what the base shows)")
	list := verb("list", "List the checks, their legs and receipts", noArgs, func(cmd *cobra.Command, _ []string) error {
		return testingCall(t, cmd, "testing/status", nil, func(out litmus.RunStatus) string {
			lines := make([]string, 0, len(out.Checks))
			for _, c := range out.Checks {
				lines = append(lines, fmt.Sprintf("%s (%d receipts): %s", checkLine(c), len(c.Receipts), c.Expect))
			}
			return strings.Join(lines, "\n")
		})
	})
	start := verb("start <id>", "Start a check; actions record receipts against it", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		return testingCall(t, cmd, "testing/checks/"+url.PathEscape(a[0])+"/start", map[string]any{}, checkLine)
	})
	end := verb("end <id>", "End a check and record its outcome", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		if result != "pass" && result != "fail" && result != "partial" && result != "absent" && result != "skipped" {
			return usageError{errors.New("usage: --result must be pass, fail, partial, absent or skipped")}
		}
		body := map[string]any{"result": result, "note": note}
		if cmd.Flags().Changed("shot") {
			if shot <= 0 {
				return usageError{errors.New("usage: --shot must be a positive receipt number")}
			}
			body["shot"] = shot
		}
		if sameFault {
			body["sameFault"] = true
		}
		return testingCall(t, cmd, "testing/checks/"+url.PathEscape(a[0])+"/end", body, func(c litmus.Check) string {
			lines := []string{checkLine(c)}
			for _, r := range c.Receipts {
				for _, shot := range r.ThemeShots {
					lines = append(lines, fmt.Sprintf("after (%s): %s", shot.Theme, shot.Path))
				}
				if r.ThemeNote != "" {
					lines = append(lines, r.ThemeNote)
				}
			}
			return strings.Join(lines, "\n")
		})
	})
	end.Flags().StringVar(&result, "result", "", "Outcome: pass, fail (the feature is there and broken), partial, absent (this build lacks the feature entirely; needs a screenshot of where it would be) or skipped (could not reach it; needs no receipt)")
	end.Flags().StringVar(&note, "note", "", "Why, in one line")
	end.Flags().BoolVar(&sameFault, "same-fault", false, "Base leg only: the failure or partial is the same fault as on the PR build")
	end.Flags().IntVar(&shot, "shot", 0, "Head receipt number whose after-shot shows the failure")
	check.AddCommand(add, list, start, end)
	return check
}

// compareCommand pixel-diffs a check's last base after-shot against the head page; it reruns nothing.
func (t *testingCLI) compareCommand() *cobra.Command {
	return verb("compare <checkID>", "Diff a check's last base and head after-shots and keep the image on the check", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		return testingCall(t, cmd, "testing/checks/"+url.PathEscape(a[0])+"/compare", map[string]any{}, prettyJSON)
	})
}

func (t *testingCLI) replayCommand() *cobra.Command {
	return verb("replay <headCheckId>", "Repeat a check already walked on the PR build against the base build", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		if t.leg != "base" {
			return usageError{errors.New("usage: replay requires --leg base")}
		}
		return testingCallTimeout(t, cmd, "testing/checks/"+url.PathEscape(a[0])+"/replay", map[string]any{"leg": t.leg}, driveText, litmus.RunBudget+time.Minute)
	})
}

func (t *testingCLI) submitCommand() *cobra.Command {
	var verdictFile, reason string
	var blocked, skipBase bool
	cmd := verb("submit", "Submit the verdict; every check needs an outcome and a receipt (--blocked --reason closes a run you cannot finish)", noArgs, func(cmd *cobra.Command, _ []string) error {
		body := map[string]any{}
		switch {
		case blocked && skipBase:
			return usageError{errors.New("usage: --blocked and --skip-base cannot be combined")}
		case blocked && strings.TrimSpace(reason) == "":
			return usageError{errors.New("usage: --blocked requires a non-empty --reason")}
		case skipBase && strings.TrimSpace(reason) == "":
			return usageError{errors.New("usage: --skip-base requires a non-empty --reason")}
		case blocked && verdictFile != "":
			return usageError{errors.New("usage: --blocked takes --reason, not --verdict-file")}
		case !blocked && !skipBase && reason != "":
			return usageError{errors.New("usage: --reason requires --blocked or --skip-base")}
		case blocked:
			body["blocked"], body["reason"] = true, strings.TrimSpace(reason)
		default:
			var summary string
			if verdictFile != "" {
				data, err := os.ReadFile(verdictFile)
				if err != nil {
					return err
				}
				summary = string(data)
			}
			if summary != "" {
				body["summary"] = summary
			}
			if skipBase {
				body["skipBase"] = strings.TrimSpace(reason)
			}
		}
		return testingCallTimeout(t, cmd, "testing/submit", body, func(v litmus.Verdict) string {
			return strings.Join(append(append(append([]string{"verdict: " + string(v.Overall)}, v.Reasons...), commentLine(v.CommentState, v.CommentURL)...), evidenceLine(v.EvidenceState, v.EvidenceCommit)...), "\n")
		}, submitCallTimeout)
	})
	cmd.Flags().StringVar(&verdictFile, "verdict-file", "", "File whose text is kept as the verdict summary")
	cmd.Flags().BoolVar(&blocked, "blocked", false, "Close the run as blocked: no receipts needed, the reason becomes the verdict")
	cmd.Flags().BoolVar(&skipBase, "skip-base", false, "Submit without a base result for the checks that have none, at any time; needs --reason")
	cmd.Flags().StringVar(&reason, "reason", "", "What failed (with --blocked) or why the base was not run (with --skip-base)")
	return cmd
}

func (t *testingCLI) recipeCommand() *cobra.Command {
	recipe := verb("recipe", "Work with per-repo recipes", noArgs, nil)
	var sha string
	validate := verb("validate <path>", "Validate a recipe file; with --sha also run it against that commit", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		abs, err := filepath.Abs(a[0]) // the daemon's working directory is not the worker's
		if err != nil {
			return err
		}
		ref, err := litmus.RecipeRef(abs, sha)
		if err != nil {
			return usageError{err}
		}
		timeout := commandTimeout
		if sha != "" {
			timeout = testingSetupCallTimeout
		}
		return testingCallTimeout(t, cmd, "testing/recipe/validate", map[string]any{"path": ref}, func(r litmus.Recipe) string {
			return strings.Join(append([]string{"recipe ok: " + r.Kind}, r.Timings...), "\n")
		}, timeout)
	})
	validate.Flags().StringVar(&sha, "sha", "", "Prepare, launch, seed and clean up a head leg at this commit and print the timings")
	recipe.AddCommand(validate)
	return recipe
}

func (t *testingCLI) waitCommand() *cobra.Command {
	var text string
	cmd := verb("wait [selector|ms]", "Wait for a selector, a delay in ms, or --text", atMostOneArg, func(cmd *cobra.Command, a []string) error {
		switch {
		case text != "" && len(a) == 0:
			a = []string{"--text", text}
		case text != "" || len(a) != 1:
			return usageError{errors.New("usage: wait takes one selector or ms, or --text")}
		}
		return t.drive(cmd, "wait", a)
	})
	cmd.Flags().StringVar(&text, "text", "", "Wait until this text appears")
	return cmd
}

// launchCallTimeout covers the default recipe's launch budget plus a minute for the response.
const launchCallTimeout = litmus.DefaultLaunchBudget + time.Minute

// testingSetupCallTimeout adds a minute to intake and recipe validation.
const testingSetupCallTimeout = litmus.RecipeValidateBudget + time.Minute

const postCallTimeout = litmus.PostBudget + time.Minute
const submitCallTimeout = litmus.SubmitBudget + time.Minute

// prepareCallTimeout outlasts the daemon's 20 minute cap on one prepare: a cold
// build of both legs runs for minutes, and a call cut short by the client would
// only come back as a retry that waits for the same build.
const prepareCallTimeout = litmus.PrepareBudget + time.Minute

func (t *testingCLI) recordCommand() *cobra.Command {
	record := verb("record", "Record the target's window to a video", noArgs, nil)
	record.AddCommand(verb("start <path>", "Start recording to path", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		return t.drive(cmd, "record", []string{"start", a[0]})
	}), verb("stop", "Stop recording", noArgs, func(cmd *cobra.Command, _ []string) error {
		return t.drive(cmd, "record", []string{"stop"})
	}))
	return record
}

// testingCall sends one request for the current session (a POST of body, or a
// GET when body is nil), decodes the daemon's answer as T and prints it with text.
func testingCall[T any](t *testingCLI, cmd *cobra.Command, path string, body map[string]any, text func(T) string) error {
	return testingCallTimeout(t, cmd, path, body, text, commandTimeout)
}

// testingCallTimeout is testingCall with its own bound on the daemon call, for a verb that outlasts commandTimeout.
func testingCallTimeout[T any](t *testingCLI, cmd *cobra.Command, path string, body map[string]any, text func(T) string, timeout time.Duration) error {
	sessionID, err := testingSessionID()
	if err != nil {
		return err
	}
	var out T
	if body == nil {
		err = t.c.getJSON(cmd.Context(), path+"?sessionId="+url.QueryEscape(sessionID), &out)
	} else {
		body["sessionId"] = sessionID
		err = t.c.doJSONPathWithHeadersAndTimeout(cmd.Context(), http.MethodPost, "/api/v1/"+path, body, &out, nil, timeout)
	}
	if err != nil {
		return err
	}
	return t.show(cmd, out, text(out))
}

// drive sends one engine action and prints its compact result.
func (t *testingCLI) drive(cmd *cobra.Command, action string, args []string) error {
	if t.leg != "" {
		args = append(args[:len(args):len(args)], "--leg", t.leg)
	}
	return testingCall(t, cmd, "testing/drive", map[string]any{"action": action, "args": args}, driveText)
}

func driveText(out map[string]any) string {
	if text, ok := out["text"].(string); ok {
		return text
	}
	return prettyJSON(out)
}

func prettyJSON(out map[string]any) string {
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

func (t *testingCLI) show(cmd *cobra.Command, v any, text string) error {
	if t.asJSON {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
	return err
}

// withCaller names the calling session (AO_SESSION_ID) in the body of a verb only the user may run, so the daemon can
// refuse it from a Litmus tester's own session whatever its harness. A user's terminal has no session id.
func withCaller(body map[string]any) map[string]any {
	if id := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); id != "" {
		body["sessionId"] = id
	}
	return body
}

func testingSessionID() (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{errors.New("usage: AO_SESSION_ID is required")}
	}
	return sessionID, nil
}

func runLine(r litmus.Run) string {
	line := fmt.Sprintf("run %s %s %s#%d deadline %s", r.ID, r.State, r.Repo, r.PRNumber, r.Deadline.Format("15:04:05"))
	if r.Error != "" {
		line += "\n" + r.Error
	}
	return strings.Join(append(append([]string{line}, commentLine(r.CommentState, r.CommentURL)...), evidenceLine(r.EvidenceState, r.EvidenceCommit)...), "\n")
}

// launchLine is runLine plus any seed step that failed on this leg: its sample content is missing, which the
// tester must know before it judges a check.
func launchLine(r litmus.Run, leg string) string {
	lines := []string{runLine(r)}
	prefix := "The " + map[string]string{"head": "PR", "base": "main"}[leg] + " build started without its sample content (seed step "
	for _, g := range r.Gaps {
		if strings.HasPrefix(g, prefix) {
			lines = append(lines, g)
		}
	}
	return strings.Join(lines, "\n")
}

// commentLine says what became of the PR comment: its URL, or the state when there is none (off, failed:<reason>).
// A run that has not submitted has neither and gets no line.
func commentLine(state, link string) []string {
	switch {
	case link != "":
		return []string{"comment " + state + ": " + link}
	case state != "":
		return []string{"comment " + state}
	}
	return nil
}

// evidenceLine says what became of the comment's images: published at a commit, off, or why not.
func evidenceLine(state, commit string) []string {
	switch {
	case commit != "":
		return []string{"evidence " + state + ": " + commit[:min(len(commit), 7)]}
	case state != "":
		return []string{"evidence " + state}
	}
	return nil
}

func legLine(l litmus.Leg) string {
	line := fmt.Sprintf("  %s %s", l.Name, l.State)
	if l.State == "running" {
		line += fmt.Sprintf(" daemon :%d debug :%d", l.DaemonPort, l.DebugPort)
	}
	return line
}

// pruneCommand asks the daemon to apply the disk retention rules; it needs no session.
func (t *testingCLI) pruneCommand() *cobra.Command {
	var keepRuns, keepCheckouts int
	var dryRun bool
	cmd := verb("prune", "Delete old checkouts and run dirs; keeps those of unfinished runs", noArgs, func(cmd *cobra.Command, _ []string) error {
		body := withCaller(map[string]any{})
		if cmd.Flags().Changed("keep-runs") {
			body["keepRuns"] = keepRuns
		}
		if cmd.Flags().Changed("keep-checkouts") {
			body["keepCheckouts"] = keepCheckouts
		}
		if dryRun {
			body["dryRun"] = true
		}
		var out litmus.PruneReport
		if err := t.c.postJSON(cmd.Context(), "testing/prune", body, &out); err != nil {
			return err
		}
		return t.show(cmd, out, strings.Join(out.Lines, "\n"))
	})
	f := cmd.Flags()
	f.IntVar(&keepRuns, "keep-runs", litmus.DefaultKeepRuns, "Finished run dirs to keep besides unfinished runs and the current one; 0 keeps only the current and unfinished runs")
	f.IntVar(&keepCheckouts, "keep-checkouts", litmus.DefaultKeepCheckouts, "Most recent finished runs whose checkouts are kept besides those of unfinished runs")
	f.BoolVar(&dryRun, "dry-run", false, "Report what would be deleted and delete nothing")
	return cmd
}

// projectFlag is the project a panel verb acts on: --project, else AO_PROJECT_ID, else none.
func projectFlag(explicit string) string {
	if id := strings.TrimSpace(explicit); id != "" {
		return id
	}
	return strings.TrimSpace(os.Getenv("AO_PROJECT_ID"))
}

// runsCommand lists a project's runs. Like prune it needs no session.
func (t *testingCLI) runsCommand() *cobra.Command {
	var project string
	var limit int
	cmd := verb("runs", "List a project's testing runs, newest first", noArgs, func(cmd *cobra.Command, _ []string) error {
		q := url.Values{}
		if id := projectFlag(project); id != "" {
			q.Set("projectId", id)
		}
		if cmd.Flags().Changed("limit") {
			q.Set("limit", strconv.Itoa(limit))
		}
		path := "testing/runs"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		var out litmus.RunList
		if err := t.c.getJSON(cmd.Context(), path, &out); err != nil {
			return err
		}
		lines := make([]string, 0, len(out.Runs))
		for _, r := range out.Runs {
			lines = append(lines, runRowLine(r))
		}
		return t.show(cmd, out, cmp.Or(strings.Join(lines, "\n"), "no runs"))
	})
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: AO_PROJECT_ID, else every project)")
	cmd.Flags().IntVar(&limit, "limit", litmus.DefaultRunsLimit, "Most runs to list")
	return cmd
}

func (t *testingCLI) showCommand() *cobra.Command {
	return verb("show <runId>", "Show a run's verdict and drafted comments", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		var out litmus.RunDetail
		if err := t.c.getJSON(cmd.Context(), "testing/runs/"+url.PathEscape(a[0]), &out); err != nil {
			return err
		}
		if t.asJSON {
			return writeJSON(cmd.OutOrStdout(), out)
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		drafts, err := litmus.NewStore(cfg.DataDir).DraftComments(a[0])
		if err != nil {
			return err
		}
		parts := []string{fmt.Sprintf("run %s %s#%d %s verdict %s", out.ID, out.Repo, out.PR.Number, out.State, out.Verdict)}
		if out.Error != "" {
			parts = append(parts, out.Error)
		}
		if table := verdictChecksTable(out.VerdictMD); table != "" {
			parts = append(parts, table)
		}
		dir := filepath.Join(cfg.DataDir, "litmus", "runs", a[0], "comments")
		for _, draft := range drafts {
			parts = append(parts, filepath.Join(dir, strconv.Itoa(draft.Number)+".md")+"\n"+draft.Body)
			for _, match := range draftMediaLinks.FindAllStringSubmatch(draft.Body, -1) {
				link, err := url.Parse(match[1])
				if err == nil && !link.IsAbs() && link.Host == "" && link.Path != "" {
					parts = append(parts, "media: "+filepath.Join(dir, filepath.FromSlash(link.Path)))
				}
			}
		}
		if len(drafts) == 0 && out.CommentMD != "" {
			parts = append(parts, out.CommentMD)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.Join(parts, "\n\n"))
		return err
	})
}

var draftMediaLinks = regexp.MustCompile(`!?\[[^\]\n]*\]\(([^)\n]+)\)`)

func verdictChecksTable(md string) string {
	var rows []string
	inChecks := false
	for _, line := range strings.Split(md, "\n") {
		if strings.TrimSpace(line) == "## Checks" {
			inChecks = true
			continue
		}
		if !inChecks {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			rows = append(rows, line)
		} else if len(rows) > 0 || strings.HasPrefix(line, "## ") {
			break
		}
	}
	return strings.Join(rows, "\n")
}

// runRowLine is one run of the list: id, verdict, state, PR, duration and what became of the comment.
func runRowLine(r litmus.RunSummary) string {
	line := fmt.Sprintf("%s %s %s %s#%d %s", r.ID, r.Verdict, r.State, r.Repo, r.PR.Number, (time.Duration(r.DurationSeconds) * time.Second).String())
	if r.Error != "" {
		line += " | " + r.Error
	}
	return strings.Join(append([]string{line}, commentLine(r.CommentState, r.CommentURL)...), " | ")
}

// rerunCommand starts a new tester on the PR of an earlier run.
func (t *testingCLI) rerunCommand() *cobra.Command {
	var project string
	cmd := verb("rerun <runId>", "Start a new tester on the pull request of an earlier run", exactArgs(1), func(cmd *cobra.Command, a []string) error {
		body := withCaller(map[string]any{"runId": a[0]})
		if id := projectFlag(project); id != "" {
			body["projectId"] = id
		}
		var out litmus.Rerun
		if err := t.c.postJSON(cmd.Context(), "testing/rerun", body, &out); err != nil {
			return err
		}
		return t.show(cmd, out, fmt.Sprintf("spawned session %s %q, run %s", out.SessionID, out.Name, out.RunID))
	})
	cmd.Flags().StringVar(&project, "project", "", "Project id, only for a run that did not record its project (default: AO_PROJECT_ID)")
	return cmd
}

// decideCommand is post or discard: the verdict on a run that waits for approval.
func (t *testingCLI) decideCommand(name, short string) *cobra.Command {
	return verb(name+" <runId>", short, exactArgs(1), func(cmd *cobra.Command, a []string) error {
		var out litmus.Run
		timeout := commandTimeout
		if name == "post" {
			timeout = postCallTimeout
		}
		if err := t.c.doJSONPathWithHeadersAndTimeout(cmd.Context(), http.MethodPost, "/api/v1/testing/runs/"+url.PathEscape(a[0])+"/"+name, withCaller(map[string]any{}), &out, nil, timeout); err != nil {
			return err
		}
		return t.show(cmd, out, runLine(out))
	})
}

// settingsCommand shows or sets a project's Litmus settings. The API replaces the whole record, so a set reads the
// current one first and changes only the flags given.
func (t *testingCLI) settingsCommand() *cobra.Command {
	var project, ask, review, harness, model, effort string
	cmd := verb("settings", "Show or set a project's Litmus settings", noArgs, func(cmd *cobra.Command, _ []string) error {
		id := projectFlag(project)
		if id == "" {
			return usageError{errors.New("usage: --project is required")}
		}
		flags := cmd.Flags()
		askOn, reviewOn := false, false
		for _, o := range []struct {
			name, val string
			on        *bool
		}{{"ask-before-posting", ask, &askOn}, {"review", review, &reviewOn}} {
			if !flags.Changed(o.name) {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(o.val)) {
			case "on", "true":
				*o.on = true
			case "off", "false":
			default:
				return usageError{fmt.Errorf("usage: --%s must be on or off", o.name)}
			}
		}
		path := "testing/projects/" + url.PathEscape(id) + "/settings"
		var out litmus.ProjectSettings
		if err := t.c.getJSON(cmd.Context(), path, &out); err != nil {
			return err
		}
		if flags.Changed("ask-before-posting") || flags.Changed("review") || flags.Changed("harness") || flags.Changed("model") || flags.Changed("effort") {
			if flags.Changed("ask-before-posting") {
				out.AskBeforePosting = askOn
			}
			if flags.Changed("review") {
				out.Review = reviewOn
			}
			if flags.Changed("harness") {
				if h := strings.TrimSpace(harness); h != out.Harness && out.Model != "" && !flags.Changed("model") {
					return usageError{fmt.Errorf("usage: the stored model %q belongs to the current harness and may not exist on %q; pass --model in the same call (--model \"\" for the default)", out.Model, cmp.Or(h, "the default harness"))}
				}
				out.Harness = strings.TrimSpace(harness)
			}
			if flags.Changed("model") {
				out.Model = strings.TrimSpace(model)
			}
			if flags.Changed("effort") {
				out.Effort = strings.TrimSpace(effort)
			}
			var saved litmus.ProjectSettings
			if err := t.c.putJSON(cmd.Context(), path, out, &saved); err != nil {
				return err
			}
			out = saved
		}
		return t.show(cmd, out, "ask-before-posting "+map[bool]string{true: "on", false: "off"}[out.AskBeforePosting]+" | review "+map[bool]string{true: "on", false: "off"}[out.Review]+
			" | harness "+cmp.Or(out.Harness, "(default)")+" | model "+cmp.Or(out.Model, "(default)")+" | effort "+cmp.Or(out.Effort, "(default)"))
	})
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: AO_PROJECT_ID)")
	cmd.Flags().StringVar(&ask, "ask-before-posting", "", "on: a finished run waits for `ao testing post <runId>` instead of commenting on the PR; off: it posts at once")
	cmd.Flags().StringVar(&review, "review", "", "on: a failing verdict also submits a REQUEST_CHANGES review beside the comment; off (default): the comment alone")
	cmd.Flags().StringVar(&harness, "harness", "", "Agent the project's testers run on: claude-code or codex (empty: the project's worker agent if supported, then claude-code)")
	cmd.Flags().StringVar(&model, "model", "", "Model the project's testers run on, e.g. opus, or gpt-6.1-sol with --harness codex (empty: the project's worker model, then sonnet on claude-code or Codex's default)")
	cmd.Flags().StringVar(&effort, "effort", "", "Reasoning effort the project's testers run at, one the model supports (empty: the project's worker effort, then high)")
	return cmd
}

func checkLine(c litmus.Check) string {
	line := fmt.Sprintf("%s %s %s", c.ID, c.State, c.Name)
	if c.Leg != "" {
		line += " [" + c.Leg + "]"
	}
	return line
}
