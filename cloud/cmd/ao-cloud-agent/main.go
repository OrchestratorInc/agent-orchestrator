// ao-cloud-agent is the session-local AO CLI installed as `ao` in Cloud
// workers. It is deliberately a thin client: every orchestration operation is
// authenticated and executed by the control plane.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/notificationoutbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

const (
	maxResponseBody = 2 << 20
	maxHookPayload  = 64 << 10
)

type client struct {
	baseURL   string
	tokenFile string
	http      *http.Client
}

type session struct {
	ID               string        `json:"id"`
	Kind             string        `json:"kind"`
	Harness          string        `json:"harness"`
	DisplayName      string        `json:"displayName"`
	Branch           string        `json:"branch"`
	Status           string        `json:"status"`
	ActivityState    string        `json:"activityState"`
	RuntimeConnected bool          `json:"runtimeConnected"`
	IsTerminated     bool          `json:"isTerminated"`
	PRs              []pullRequest `json:"prs"`
	// The artifact fields are present while cloud artifacts are on.
	ArtifactCount      *int       `json:"artifactCount,omitempty"`
	ArtifactsUpdatedAt *time.Time `json:"artifactsUpdatedAt,omitempty"`
	// ArtifactFiles names the most recently changed files, at most ten;
	// ArtifactFilesTruncated is set when the worker has more.
	ArtifactFiles          []artifactFile `json:"artifactFiles,omitempty"`
	ArtifactFilesTruncated bool           `json:"artifactFilesTruncated,omitempty"`
}

// artifactFile is one committed file in a worker's artifact directory.
type artifactFile struct {
	Path      string    `json:"path"`
	Kind      string    `json:"kind"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type pullRequest struct {
	URL          string `json:"url"`
	Number       int    `json:"number"`
	State        string `json:"state"`
	CI           string `json:"ci"`
	Review       string `json:"review"`
	Mergeability string `json:"mergeability"`
	SourceBranch string `json:"sourceBranch,omitempty"`
	TargetBranch string `json:"targetBranch,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ao:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(os.Stdout)
		return nil
	}
	// The browser runs beside the agent and needs no control-plane credential.
	if args[0] == "browser" {
		return runBrowser(args[1:])
	}
	apiURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AO_CLOUD_WORKER_API_URL")), "/")
	tokenFile := strings.TrimSpace(os.Getenv("AO_CLOUD_WORKER_TOKEN_FILE"))
	if apiURL == "" {
		publicURL := strings.TrimRight(
			strings.TrimSpace(os.Getenv("AO_CLOUD_PUBLIC_URL")), "/",
		)
		if publicURL != "" {
			apiURL = publicURL + "/api/cloud/v1"
		}
	}
	if tokenFile == "" {
		dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
		if dataDir != "" {
			tokenFile = filepath.Join(dataDir, "worker-token")
		}
	}
	if apiURL == "" || tokenFile == "" {
		return errors.New("this ao command must run inside an AO Cloud worker")
	}
	c := &client{
		baseURL: apiURL, tokenFile: tokenFile,
		http: &http.Client{Timeout: 30 * time.Second},
	}
	ctx := context.Background()
	switch args[0] {
	case "hooks":
		return runHook(ctx, c, args[1:], os.Stdin)
	case "spawn":
		return runSpawn(ctx, c, args[1:])
	case "list", "ls", "status":
		return runList(ctx, c, args[1:])
	case "send":
		return runSend(ctx, c, args[1:])
	case "report":
		return runReport(ctx, c, args[1:])
	case "kill", "delete", "rm":
		return runDelete(ctx, c, args[1:])
	case "claim-pr":
		return runClaimPullRequest(ctx, c, args[1:])
	case "review":
		return runReview(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown command %q; run `ao help`", args[0])
	}
}

func runHook(ctx context.Context, c *client, args []string, input io.Reader) error {
	hookAt := time.Now()
	if len(args) != 2 {
		return nil
	}
	// A reviewer is a separate, short-lived harness process inside the same
	// sandbox. Its hooks inherit the parent worker credential, but publishing
	// them as parent-agent activity leaves that session stuck active after the
	// reviewer exits and prevents queued feedback from reaching the real agent.
	if os.Getenv(worker.ReviewTerminalEnv) == "1" {
		return nil
	}
	// A turn end (Stop, or StopFailure when an API error ended it after it
	// edited files) drives durable-restore checkpointing: poke the worker's
	// checkpoint bridge so it captures the transcript and any uncommitted
	// work. Event-driven, best-effort, and fire-and-forget so a turn end never
	// waits on git or the network.
	if args[1] == "stop" || args[1] == "stop-failure" {
		worker.PokeCheckpoint(ctx, os.Getenv("AO_CHECKPOINT_SOCKET"))
	}
	payload, err := io.ReadAll(io.LimitReader(input, maxHookPayload+1))
	if err != nil || len(payload) > maxHookPayload {
		return nil
	}
	activity, ok := worker.ActivityEventFromHook(args[0], args[1], payload)
	if !ok {
		return nil
	}
	activity.SourceInterface = strings.TrimSpace(os.Getenv("AO_CLOUD_SOURCE_INTERFACE"))
	if activity.SourceInterface == "tui" && activity.State == contract.ActivityIdle &&
		(activity.Event == "stop" || activity.Event == "stop-failure" || activity.Event == "interrupt") {
		// Keep the provider's native idle proof beside the worker. The durable
		// control-plane projection can lag terminal input and is not a safe
		// handoff fence on its own.
		_ = worker.RecordTUIStop(strings.TrimSpace(os.Getenv("AO_DATA_DIR")), hookAt)
	}
	if activity.Harness == "claude-code" && activity.SourceInterface == "tui" && activity.AgentSessionID != "" {
		// The worker follows the terminal Claude's transcript for interrupts.
		// /clear or /resume moves that process to another conversation, and
		// CLAUDE_PID tells the terminal's process from a headless claude the
		// agent runs from a tool (it shares this environment and these hooks).
		_ = worker.RecordClaudeConversation(strings.TrimSpace(os.Getenv("AO_DATA_DIR")),
			strings.TrimSpace(os.Getenv("CLAUDE_PID")), activity.AgentSessionID, hookAt)
	}
	if activity.Harness == "codex" && activity.Event == "stop" && activity.LatestAssistantUpdate == "" {
		activity.LatestAssistantUpdate = latestCodexAssistantMessage(
			strings.TrimSpace(os.Getenv("CODEX_HOME")), activity.AgentSessionID,
		)
	}
	hookCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_ = c.request(
		hookCtx,
		http.MethodPost,
		"/worker/events",
		worker.EventRequest{
			Type:    "agent.activity",
			Payload: activity,
		},
		false,
		nil,
	)
	if event, ok := notificationFromActivity(
		strings.TrimSpace(os.Getenv("AO_SESSION_ID")),
		workerEpochFromEnvironment(),
		activity,
	); ok {
		dataDir := strings.TrimSpace(os.Getenv("AO_DATA_DIR"))
		if dataDir != "" {
			if outbox, err := notificationoutbox.Open(filepath.Join(dataDir, "notification-outbox.db")); err == nil {
				_ = outbox.Enqueue(hookCtx, event)
				_ = outbox.Close()
			}
		}
	}
	// Hook delivery is best-effort and must never break the coding agent.
	return nil
}

// workerEpochFromEnvironment is the epoch of the worker now running beside
// this agent. An agent that survived a worker restart (Freestyle wake) still
// carries its launch epoch in AO_CLOUD_WORKER_EPOCH, so the epoch the current
// worker recorded in the data directory wins; the environment is the fallback
// for workers that do not record it.
func workerEpochFromEnvironment() int64 {
	epoch, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("AO_CLOUD_WORKER_EPOCH")), 10, 64)
	return notificationoutbox.CurrentWorkerEpoch(os.Getenv("AO_DATA_DIR"), epoch)
}

func notificationFromActivity(sessionID string, epoch int64, activity worker.ActivityEvent) (notificationoutbox.Event, bool) {
	if sessionID == "" || epoch <= 0 {
		return notificationoutbox.Event{}, false
	}
	eventType := ""
	activityID := strings.TrimSpace(activity.ToolUseID)
	if activityID == "" {
		activityID = strings.TrimSpace(activity.AgentSessionID)
	}
	message := ""
	switch {
	case activity.State == contract.ActivityWaitingInput || activity.State == contract.ActivityBlocked:
		eventType = "needs_input"
		if activityID == "" {
			activityID = hookNotificationID(activity.Harness, activity.Event, activity.AgentSessionID)
		}
		message = "Agent needs your input"
		if activity.ToolName != "" {
			message = activity.ToolName + " requires your input"
		}
	case activity.Event == "session-end" && activity.State == contract.ActivityExited:
		eventType = "agent_failed"
		message = "Agent session ended unexpectedly"
	default:
		return notificationoutbox.Event{}, false
	}
	payload, err := json.Marshal(map[string]string{
		"activityId": activityID,
		"message":    message,
		"harness":    activity.Harness,
		"event":      activity.Event,
	})
	if err != nil {
		return notificationoutbox.Event{}, false
	}
	identity := strings.Join([]string{sessionID, strconv.FormatInt(epoch, 10), eventType, activity.Harness, activity.Event, activityID}, "\x00")
	hash := sha256.Sum256([]byte(identity))
	return notificationoutbox.Event{
		EventID: "evt_" + hex.EncodeToString(hash[:16]), EventType: eventType,
		Payload: payload, OccurredAt: time.Now().UTC(), WorkerEpoch: epoch,
	}, true
}

func hookNotificationID(values ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(hash[:16])
}

func runSpawn(ctx context.Context, c *client, args []string) error {
	flags := flag.NewFlagSet("spawn", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var harness, name, prompt, mode, branch, providerConnection string
	// Empty by default so the control plane can fill in the project's configured
	// worker agent (config.worker.agent); an explicit value here still wins.
	flags.StringVar(&harness, "harness", "", "agent harness (default: the project's configured worker agent)")
	flags.StringVar(&harness, "agent", "", "alias for --harness")
	flags.StringVar(&name, "name", "", "child display name")
	flags.StringVar(&prompt, "prompt", "", "initial child prompt")
	flags.StringVar(&mode, "mode", "trusted", "standard or trusted")
	flags.StringVar(
		&branch, "branch", "",
		"git branch for the worker; one that already exists on the remote is continued (default: a new branch)",
	)
	flags.StringVar(
		&providerConnection, "sandbox-provider-connection", "",
		"sandbox provider connection id",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("spawn requires --name")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return errors.New("spawn requires --prompt")
	}
	if mode != "standard" && mode != "trusted" {
		return errors.New("--mode must be standard or trusted")
	}
	body := map[string]any{
		"harness": harness, "displayName": name, "prompt": prompt, "mode": mode,
	}
	if branch = strings.TrimSpace(branch); branch != "" {
		body["branch"] = branch
	}
	if providerConnection != "" {
		body["sandboxProviderConnectionId"] = providerConnection
	}
	var response struct {
		Session session `json:"session"`
	}
	if err := c.request(ctx, http.MethodPost, "/worker/children", body, true, &response); err != nil {
		return err
	}
	fmt.Printf("spawned %s (%s)\n", response.Session.ID, response.Session.Status)
	return nil
}

func runList(ctx context.Context, c *client, args []string) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	all := flags.Bool("all", false, "include terminated workers")
	if err := flags.Parse(args); err != nil {
		return err
	}
	items, err := listChildren(ctx, c, *all)
	if err != nil {
		return err
	}
	if items == nil {
		// A childless listing must print [], not null: agents parse this.
		items = []session{}
	}
	if *asJSON {
		encoded, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}
	out := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(out, "ID\tNAME\tHARNESS\tBRANCH\tSTATUS\tCONNECTED\tPR\tARTIFACTS")
	for _, child := range items {
		fmt.Fprintf(
			out, "%s\t%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			child.ID, child.DisplayName, child.Harness, child.Branch, child.Status,
			child.RuntimeConnected, pullRequestSummary(child.PRs), artifactSummary(child.ArtifactFiles, artifactCount(child)),
		)
	}
	return out.Flush()
}

// listChildren follows the listing's cursor so an orchestrator with more than
// one page of workers still sees all of them; the page bound is a runaway
// backstop, not an expected limit.
func listChildren(ctx context.Context, c *client, includeTerminated bool) ([]session, error) {
	const maxPages = 5
	var items []session
	cursor := ""
	for page := 0; page < maxPages; page++ {
		path := "/worker/children?limit=100"
		if includeTerminated {
			path += "&includeTerminated=true"
		}
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var response struct {
			Items []session `json:"items"`
			Page  struct {
				HasMore    bool   `json:"hasMore"`
				NextCursor string `json:"nextCursor"`
			} `json:"page"`
		}
		if err := c.request(ctx, http.MethodGet, path, nil, false, &response); err != nil {
			return nil, err
		}
		items = append(items, response.Items...)
		if !response.Page.HasMore || response.Page.NextCursor == "" {
			return items, nil
		}
		cursor = response.Page.NextCursor
	}
	fmt.Fprintln(os.Stderr, "ao: worker list truncated after 500 entries")
	return items, nil
}

// artifactSummary renders the row's ARTIFACTS column: up to two file paths
// and how many more the worker has in total, e.g. "report.html, notes.md (+3)".
func artifactSummary(files []artifactFile, count int) string {
	if len(files) == 0 {
		return "-"
	}
	const shown = 2
	paths := make([]string, 0, shown)
	for _, file := range files[:min(shown, len(files))] {
		paths = append(paths, file.Path)
	}
	summary := strings.Join(paths, ", ")
	if more := max(count, len(files)) - len(paths); more > 0 {
		summary += fmt.Sprintf(" (+%d)", more)
	}
	return summary
}

// artifactCount is the worker's total artifact files, or 0 when not reported.
func artifactCount(child session) int {
	if child.ArtifactCount == nil {
		return 0
	}
	return *child.ArtifactCount
}

// pullRequestSummary renders the row's PR column, e.g. "#12 ci:failing".
func pullRequestSummary(prs []pullRequest) string {
	if len(prs) == 0 {
		return "-"
	}
	pr := prs[0]
	summary := fmt.Sprintf("#%d %s", pr.Number, pr.State)
	if pr.CI != "" && pr.CI != "unknown" {
		summary += " ci:" + pr.CI
	}
	if len(prs) > 1 {
		summary += fmt.Sprintf(" (+%d)", len(prs)-1)
	}
	return summary
}

func runSend(ctx context.Context, c *client, args []string) error {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sessionID := flags.String("session", "", "child session id")
	message := flags.String("message", "", "message text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	remaining := flags.Args()
	if *sessionID == "" && len(remaining) > 0 {
		*sessionID, remaining = remaining[0], remaining[1:]
	}
	if *message == "" && len(remaining) > 0 {
		*message = strings.Join(remaining, " ")
	}
	if strings.TrimSpace(*sessionID) == "" || strings.TrimSpace(*message) == "" {
		return errors.New("send requires a child session id and message")
	}
	path := "/worker/children/" + url.PathEscape(*sessionID) + "/messages"
	if err := c.request(
		ctx, http.MethodPost, path, map[string]string{"text": *message}, true, nil,
	); err != nil {
		return err
	}
	fmt.Println("message queued (delivered when the child agent is ready)")
	return nil
}

func runReport(ctx context.Context, c *client, args []string) error {
	message := strings.TrimSpace(strings.Join(args, " "))
	if message == "" {
		return errors.New("report requires a message")
	}
	if err := c.request(
		ctx, http.MethodPost, "/worker/parent/messages",
		map[string]string{"text": message}, true, nil,
	); err != nil {
		return err
	}
	fmt.Println("reported to orchestrator")
	return nil
}

func runDelete(ctx context.Context, c *client, args []string) error {
	flags := flag.NewFlagSet("kill", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sessionID := flags.String("session", "", "child session id")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *sessionID == "" && len(flags.Args()) > 0 {
		*sessionID = flags.Args()[0]
	}
	if strings.TrimSpace(*sessionID) == "" {
		return errors.New("kill requires a child session id")
	}
	if err := c.request(
		ctx, http.MethodDelete,
		"/worker/children/"+url.PathEscape(*sessionID), nil, false, nil,
	); err != nil {
		return err
	}
	fmt.Println("session deletion requested")
	return nil
}

func runClaimPullRequest(ctx context.Context, c *client, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("claim-pr requires a pull request number or URL")
	}
	var response worker.ClaimPullRequestResponse
	if err := c.request(
		ctx,
		http.MethodPost,
		"/worker/pull-requests/claim",
		worker.ClaimPullRequestRequest{Reference: pullRequestReference(ctx, strings.TrimSpace(args[0]))},
		true,
		&response,
	); err != nil {
		return err
	}
	if response.ID == "" || response.Number <= 0 || response.HTMLURL == "" {
		return errors.New("control plane returned an incomplete pull request response")
	}
	fmt.Printf("claimed PR #%d %s\n", response.Number, response.HTMLURL)
	return nil
}

// pullRequestReference turns a bare pull request number into the pull
// request's URL on the repository checked out in the working directory, so
// `ao claim-pr 12` run in one of the session's additional repositories claims
// that repository's pull request rather than the main repository's. It does so
// only when that repository is one of the session's (worker.SessionRepositoriesEnv);
// a URL, a number in any other checkout (a fork, say) or a worker that does not
// list its repositories sends the reference as given, which the control plane
// resolves against the main repository.
func pullRequestReference(ctx context.Context, reference string) string {
	number := strings.TrimPrefix(reference, "#")
	if _, err := strconv.Atoi(number); err != nil {
		return reference
	}
	output, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		return reference
	}
	fullName, ok := githubFullName(strings.TrimSpace(string(output)))
	if !ok || !sessionRepository(os.Getenv(worker.SessionRepositoriesEnv), fullName) {
		return reference
	}
	return "https://github.com/" + fullName + "/pull/" + number
}

// sessionRepository reports whether fullName is in the comma-separated list.
func sessionRepository(list, fullName string) bool {
	for _, entry := range strings.Split(list, ",") {
		if entry = strings.TrimSpace(entry); entry != "" && strings.EqualFold(entry, fullName) {
			return true
		}
	}
	return false
}

// githubFullName extracts owner/name from a GitHub https or ssh remote URL.
func githubFullName(remote string) (string, bool) {
	path := ""
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "https://github.com/"):
		path = strings.TrimPrefix(remote, "https://github.com/")
	default:
		return "", false
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// runReview starts AO's reviewer on this session's open pull requests, the
// same review the desktop Reviews panel starts.
func runReview(ctx context.Context, c *client, args []string) error {
	if len(args) != 1 || args[0] != "trigger" {
		return errors.New("usage: ao review trigger")
	}
	// A reviewer process inherits the worker credential; it submits a verdict
	// and must never start reviews of its own.
	if os.Getenv(worker.ReviewTerminalEnv) == "1" {
		return errors.New("a reviewer cannot start another review; submit your verdict with $AO_REVIEW_HELP")
	}
	var response worker.TriggerReviewResponse
	if err := c.request(ctx, http.MethodPost, "/worker/reviews/trigger", nil, true, &response); err != nil {
		return err
	}
	if len(response.Reviews) == 0 {
		fmt.Println("no open pull request to review; open one first")
		return nil
	}
	for _, review := range response.Reviews {
		if review.Started {
			fmt.Printf("started AO review of PR #%d %s\n", review.Number, review.URL)
		} else {
			fmt.Printf("AO review of PR #%d is already running %s\n", review.Number, review.URL)
		}
	}
	return nil
}

func (c *client) request(
	ctx context.Context,
	method, path string,
	body any,
	idempotent bool,
	out any,
) error {
	token, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return fmt.Errorf("read rotating worker credential: %w", err)
	}
	if len(token) == 0 || len(token) > 16<<10 {
		return errors.New("rotating worker credential is invalid")
	}
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(
		ctx, method, c.baseURL+path, bytes.NewReader(encoded),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Worker "+strings.TrimSpace(string(token)))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotent {
		request.Header.Set("Idempotency-Key", newIdempotencyKey())
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		value, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		var envelope struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if json.Unmarshal(value, &envelope) == nil && envelope.Message != "" {
			return fmt.Errorf("%s: %s", envelope.Code, envelope.Message)
		}
		return fmt.Errorf("control plane returned HTTP %d", response.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(out)
}

func newIdempotencyKey() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("ao-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `AO Cloud orchestration commands:
  ao spawn --name NAME --prompt TEXT [--agent AGENT] [--mode standard|trusted] [--branch BRANCH]
    (--agent defaults to the project's configured worker agent; --branch
    continues a branch that already exists on the remote, and fails with
    branch_in_use while another live worker holds it)
  ao list [--json] [--all]
  ao send SESSION_ID MESSAGE
  ao report MESSAGE
  ao kill SESSION_ID
  ao claim-pr NUMBER_OR_URL
  ao review trigger
  ao browser COMMAND [ARGS...]
    (agent-browser commands: open, snapshot, click, fill, screenshot, ...; the
    person sees and can use the same pages in AO's Browser panel)

All commands except browser are authenticated through the control plane.
spawn/list/send/kill require an orchestrator session; report requires an
orchestrator parent. Child workers never connect directly to one another.`)
}
