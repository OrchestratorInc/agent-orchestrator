package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Operator 命令提交精确正文、触发 daemon 的 OS 人工验证，再用一次性审批执行操作。
// Agent 环境标记只做提前拒绝，真正的授权边界始终是每次 OS 原生验证。

// Agent-session environment markers. They are read, never written: their presence means "AO
// launched this process as a session/worker", which is exactly the case that must not act as the
// operator.
const (
	envAgentSessionID  = "AO_SESSION_ID"
	envAgentAttemptID  = "AO_ATTEMPT_ID"
	envAgentWorkflowID = "AO_WORKFLOW_ID"
	envAgentLease      = "AO_WORKFLOW_LEASE" //nolint:gosec // Environment variable NAME, not a value.
)

// exitAgentSession is the exit code for the hard refusal. It is 1 — the ordinary runtime failure
// code — because this is not usage misuse (2) and scripts already treat 1 as "did not happen".
const exitAgentSession = 1

// errAgentSession is the hard refusal. It is returned before any HTTP call.
var errAgentSession = errors.New(
	"ao workflow operator refuses to run inside an AO agent session: " +
		"operator actions require native OS user verification")

// operatorAction is one approval-gated workflow action the CLI can submit.
type operatorAction struct {
	// verb is the subcommand name.
	verb string
	// method is the HTTP method of the action.
	method string
	// pattern is the daemon's route pattern, used to detect that an approval can authorise it.
	pattern string
	// build renders the concrete path and the exact JSON body for the given arguments.
	build func(args operatorActionArgs) (path string, body []byte, err error)
	// summarize renders the response for a human.
	summarize func(raw []byte) string
}

// operatorActionArgs is what the command line supplies. Exactly one group is used per verb.
type operatorActionArgs struct {
	workflowID string
	attemptID  string
	requestID  string

	outcome              string
	errorCode            string
	expectedTaskRevision int64
	idempotencyKey       string

	approve          bool
	reason           string
	expectedRevision int64

	harness  string
	mode     string
	prompt   string
	repoKeys []string
}

// operatorActions is the closed set of verbs, mapped one-to-one onto the allowlisted routes.
//
// The definition is data rather than five near-identical functions so the operation the approval
// names and the operation the CLI submits are literally the same field. A verb that could submit
// a body whose digest differs from the one approved would defeat the whole proof.
var operatorActions = map[string]operatorAction{
	"bind": {
		verb: "bind", method: "POST",
		pattern: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/bind",
		build: func(a operatorActionArgs) (string, []byte, error) {
			if strings.TrimSpace(a.workflowID) == "" || strings.TrimSpace(a.attemptID) == "" {
				return "", nil, usageError{errors.New("--workflow and --attempt are required")}
			}
			if strings.TrimSpace(a.idempotencyKey) == "" {
				return "", nil, usageError{errors.New("--idempotency-key is required")}
			}
			body := map[string]any{"idempotencyKey": a.idempotencyKey}
			if a.harness != "" {
				body["harness"] = a.harness
			}
			if a.mode != "" {
				body["mode"] = a.mode
			}
			if a.prompt != "" {
				body["prompt"] = a.prompt
			}
			if len(a.repoKeys) > 0 {
				body["repoKeys"] = a.repoKeys
			}
			raw, err := json.Marshal(body)
			return operatorAttemptPath(a.workflowID, a.attemptID, "bind"), raw, err
		},
		summarize: operatorBindSummary,
	},
	"resolve": {
		verb: "resolve", method: "POST",
		pattern: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resolve",
		build: func(a operatorActionArgs) (string, []byte, error) {
			if strings.TrimSpace(a.workflowID) == "" || strings.TrimSpace(a.attemptID) == "" {
				return "", nil, usageError{errors.New("--workflow and --attempt are required")}
			}
			if a.outcome != "succeeded" && a.outcome != "failed" {
				return "", nil, usageError{errors.New("--outcome must be succeeded or failed")}
			}
			if strings.TrimSpace(a.idempotencyKey) == "" {
				return "", nil, usageError{errors.New("--idempotency-key is required")}
			}
			if a.expectedTaskRevision <= 0 {
				return "", nil, usageError{errors.New("--expected-task-revision must be a positive revision")}
			}
			body := map[string]any{
				"outcome":              a.outcome,
				"expectedTaskRevision": a.expectedTaskRevision,
				"idempotencyKey":       a.idempotencyKey,
			}
			if a.errorCode != "" {
				body["errorCode"] = a.errorCode
			}
			raw, err := json.Marshal(body)
			return operatorAttemptPath(a.workflowID, a.attemptID, "resolve"), raw, err
		},
		summarize: operatorAttemptSummary,
	},
	"reconcile": {
		verb: "reconcile", method: "POST",
		pattern: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/reconcile",
		build: func(a operatorActionArgs) (string, []byte, error) {
			if strings.TrimSpace(a.workflowID) == "" || strings.TrimSpace(a.attemptID) == "" {
				return "", nil, usageError{errors.New("--workflow and --attempt are required")}
			}
			// The route takes no body; an empty body is the meaningful request, and its digest is
			// the digest of zero bytes, which is what the approval binds.
			return operatorAttemptPath(a.workflowID, a.attemptID, "reconcile"), nil, nil
		},
		summarize: operatorBindingSummary,
	},
	"resume": {
		verb: "resume", method: "POST",
		pattern: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/resume",
		build: func(a operatorActionArgs) (string, []byte, error) {
			if strings.TrimSpace(a.workflowID) == "" || strings.TrimSpace(a.attemptID) == "" {
				return "", nil, usageError{errors.New("--workflow and --attempt are required")}
			}
			return operatorAttemptPath(a.workflowID, a.attemptID, "resume"), nil, nil
		},
		summarize: operatorBindingSummary,
	},
	"decide": {
		verb: "decide", method: "POST",
		pattern: "/api/v1/workflows/{workflowId}/task-requests/{requestId}/decide",
		build: func(a operatorActionArgs) (string, []byte, error) {
			if strings.TrimSpace(a.workflowID) == "" || strings.TrimSpace(a.requestID) == "" {
				return "", nil, usageError{errors.New("--workflow and --request are required")}
			}
			if a.expectedRevision <= 0 {
				return "", nil, usageError{errors.New("--expected-revision must be a positive revision")}
			}
			body := map[string]any{
				"approve":          a.approve,
				"expectedRevision": a.expectedRevision,
			}
			if a.reason != "" {
				body["reason"] = a.reason
			}
			raw, err := json.Marshal(body)
			return operatorRequestPath(a.workflowID, a.requestID), raw, err
		},
		summarize: operatorDecideSummary,
	},
}

// operatorVerbs is operatorActions' key set, sorted, for help and for the refusal message.
func operatorVerbs() []string {
	verbs := make([]string, 0, len(operatorActions))
	for verb := range operatorActions {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	return verbs
}

// operatorAttemptPath builds an attempt-scoped path with every segment escaped.
func operatorAttemptPath(workflowID, attemptID, verb string) string {
	return "/api/v1/workflows/" + escapePathSegment(workflowID) +
		"/attempts/" + escapePathSegment(attemptID) + "/" + verb
}

// operatorRequestPath builds a task-request-scoped path with every segment escaped.
func operatorRequestPath(workflowID, requestID string) string {
	return "/api/v1/workflows/" + escapePathSegment(workflowID) +
		"/task-requests/" + escapePathSegment(requestID) + "/decide"
}

// escapePathSegment escapes one path segment so an id cannot add segments or a query.
func escapePathSegment(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(raw), "/", "%2F"), "?", "%3F")
}

// operatorEnvGuard reports whether this process is running inside an AO agent session, and which
// markers said so.
func operatorEnvGuard(getenv func(string) string) (bool, []string) {
	var found []string
	for _, name := range []string{envAgentSessionID, envAgentAttemptID, envAgentWorkflowID, envAgentLease} {
		if strings.TrimSpace(getenv(name)) != "" {
			found = append(found, name)
		}
	}
	return len(found) > 0, found
}

// newWorkflowOperatorCommand builds `ao workflow operator <verb>`.
func newWorkflowOperatorCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Approve and perform operator-only workflow actions from this terminal",
		Long: "Perform an operator-only workflow action after the user approves it through native OS verification.\n\n" +
			"This command never reads the operator token and never runs inside an AO agent session. " +
			"It requests an approval for the exact operation and payload, prints the approval id and code, " +
			"opens native OS verification, then submits the action once.",
		Example: "  ao workflow operator bind --workflow w1 --attempt a1 --idempotency-key bind-1\n" +
			"  ao workflow operator resolve --workflow w1 --attempt a1 --outcome succeeded \\\n" +
			"      --expected-task-revision 3 --idempotency-key resolve-1\n" +
			"  ao workflow operator decide --workflow w1 --request req-1 --approve --expected-revision 2\n" +
			"  ao workflow operator reconcile --workflow w1 --attempt a1\n" +
			"  ao workflow operator resume --workflow w1 --attempt a1",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usageError{fmt.Errorf("choose a verb: %s", strings.Join(operatorVerbs(), ", "))}
		},
	}
	for _, verb := range operatorVerbs() {
		action := operatorActions[verb]
		cmd.AddCommand(newWorkflowOperatorVerb(ctx, action))
	}
	cmd.AddCommand(newOperatorRequestCommand(ctx))
	return cmd
}

// newWorkflowOperatorVerb builds one verb subcommand.
func newWorkflowOperatorVerb(ctx *commandContext, action operatorAction) *cobra.Command {
	var (
		args   operatorActionArgs
		asJSON bool
	)
	verb := &cobra.Command{
		Use:   action.verb,
		Short: "Request human approval, then " + action.verb + " through the operator route",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, body, err := action.build(args)
			if err != nil {
				return err
			}
			result, err := ctx.performOperatorAction(cmd.Context(), action, path, body)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(ctx.deps.Out, json.RawMessage(result.raw))
			}
			_, err = fmt.Fprintln(ctx.deps.Out, action.summarize(result.raw))
			return err
		},
	}
	verb.Flags().StringVar(&args.workflowID, "workflow", "", "workflow id")
	verb.Flags().StringVar(&args.attemptID, "attempt", "", "attempt id (bind, resolve, reconcile, resume)")
	verb.Flags().StringVar(&args.requestID, "request", "", "task request id (decide)")
	verb.Flags().StringVar(&args.outcome, "outcome", "", "resolve outcome: succeeded or failed")
	verb.Flags().StringVar(&args.errorCode, "error-code", "", "resolve error code (optional)")
	verb.Flags().Int64Var(&args.expectedTaskRevision, "expected-task-revision", 0, "the task revision this resolve expects (resolve)")
	verb.Flags().StringVar(&args.idempotencyKey, "idempotency-key", "", "idempotency key (bind, resolve)")
	verb.Flags().BoolVar(&args.approve, "approve", false, "approve the request (decide); omit to reject")
	verb.Flags().StringVar(&args.reason, "reason", "", "decision reason (decide, optional)")
	verb.Flags().Int64Var(&args.expectedRevision, "expected-revision", 0, "the request revision this decision expects (decide)")
	verb.Flags().StringVar(&args.harness, "harness", "", "harness override (bind, optional)")
	verb.Flags().StringVar(&args.mode, "mode", "", "session mode (bind, optional)")
	verb.Flags().StringVar(&args.prompt, "prompt", "", "task prompt (bind, optional)")
	verb.Flags().StringSliceVar(&args.repoKeys, "repo-key", nil, "narrow the bind to these repo keys (bind, optional)")
	verb.Flags().BoolVar(&asJSON, "json", false, "print the daemon's JSON response")
	return verb
}

// operatorActionResult carries what the action returned.
type operatorActionResult struct {
	raw []byte
}

// performOperatorAction 顺序执行正文绑定、OS 验证和同字节提交，任何一步失败都不执行操作。
func (c *commandContext) performOperatorAction(
	ctx context.Context,
	action operatorAction,
	path string,
	body []byte,
) (operatorActionResult, error) {
	if isAgent, markers := operatorEnvGuard(os.Getenv); isAgent {
		return operatorActionResult{}, fmt.Errorf(
			"%w (environment: %s)", errAgentSession, strings.Join(markers, ", "))
	}
	digest := principal.PayloadDigest(body)
	operation := principal.OperatorOperation{Method: action.method, Path: path}
	if _, ok := principal.MatchOperatorOperation(operation.Method, operation.Path); !ok {
		// Defence in depth: the CLI will not mint an approval for a path the daemon does not
		// gate, so a bug here surfaces as a CLI error rather than a confusing 400.
		return operatorActionResult{}, fmt.Errorf(
			"internal error: %s %s is not an approval-gated operator route", operation.Method, operation.Path)
	}

	approval, err := c.createOperatorApproval(ctx, operation, digest, string(body))
	if err != nil {
		return operatorActionResult{}, err
	}
	if err := c.printApprovalPrompt(operation, digest, string(body), approval); err != nil {
		return operatorActionResult{}, err
	}
	if err := c.confirmOperatorApproval(ctx, approval, digest); err != nil {
		return operatorActionResult{}, err
	}
	return c.submitOperatorAction(ctx, action, operation, body, approval.Approval.ApprovalID)
}

// operatorApprovalDTO mirrors the daemon's approval envelope.
type operatorApprovalDTO struct {
	Approval struct {
		ApprovalID string `json:"approvalId"`
		Code       string `json:"code"`
		State      string `json:"state"`
		Operation  struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"operation"`
		PayloadDigest string     `json:"payloadDigest"`
		ExpiresAt     time.Time  `json:"expiresAt"`
		ConfirmedAt   *time.Time `json:"confirmedAt"`
	} `json:"approval"`
}

// createOperatorApproval requests a pending approval for one operation and payload.
func (c *commandContext) createOperatorApproval(
	ctx context.Context,
	operation principal.OperatorOperation,
	digest, payload string,
) (operatorApprovalDTO, error) {
	var out operatorApprovalDTO
	err := c.doJSONPath(ctx, "POST", "/api/v1/operator/approvals", map[string]string{
		"operationMethod": operation.Method,
		"operationPath":   operation.Path,
		"payloadDigest":   digest,
		"payload":         payload,
	}, &out)
	if err != nil {
		return operatorApprovalDTO{}, operatorCreateError(err)
	}
	if out.Approval.ApprovalID == "" || out.Approval.Code == "" || out.Approval.State != "pending" ||
		out.Approval.Operation.Method != operation.Method || out.Approval.Operation.Path != operation.Path ||
		out.Approval.PayloadDigest != digest {
		return operatorApprovalDTO{}, errors.New("daemon returned an approval for a different operation or payload")
	}
	return out, nil
}

// operatorCreateError explains why an approval could not be created. A daemon that cannot issue
// approvals is a "this cannot happen here" answer, not a retry, so it is reported plainly.
func operatorCreateError(err error) error {
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotImplemented {
		return fmt.Errorf("this daemon cannot issue operator approvals; update AO and restart the daemon: %w", err)
	}
	return fmt.Errorf("cannot request operator approval: %w", err)
}

// printApprovalPrompt tells the human exactly what to approve.
//
// The operation and digest are printed because the desktop app shows the same two facts: a person
// comparing them is the actual control, so the CLI must not hide them behind a friendly summary.
func (c *commandContext) printApprovalPrompt(
	operation principal.OperatorOperation,
	digest, payload string,
	approval operatorApprovalDTO,
) error {
	expiresIn := time.Until(approval.Approval.ExpiresAt.UTC()).Round(time.Second)
	if expiresIn < 0 {
		expiresIn = 0
	}
	_, err := fmt.Fprintf(c.deps.Out,
		"Review this action in the native OS verification window:\n"+
			"  operation: %s %s\n"+
			"  digest:    %s\n"+
			"  payload:   %s\n"+
			"  approval:  %s\n"+
			"  code:      %s\n"+
			"  expires:   %s (%s)\n"+
			"Waiting for confirmation…\n",
		operation.Method, operation.Path, digest, payload,
		approval.Approval.ApprovalID, approval.Approval.Code,
		approval.Approval.ExpiresAt.UTC().Format(time.RFC3339), expiresIn)
	return err
}

// confirmOperatorApproval 由 daemon 发起原生人工验证；响应必须仍绑定本次操作和正文。
func (c *commandContext) confirmOperatorApproval(ctx context.Context, approval operatorApprovalDTO, digest string) error {
	if !approval.Approval.ExpiresAt.After(c.deps.Now()) {
		return errors.New("the approval expired; nothing was submitted")
	}
	verificationCtx, cancel := context.WithDeadline(ctx, approval.Approval.ExpiresAt)
	defer cancel()
	var confirmed operatorApprovalDTO
	err := c.doJSONPath(verificationCtx, "POST",
		"/api/v1/operator/approvals/"+escapePathSegment(approval.Approval.ApprovalID)+"/confirm",
		map[string]string{"code": approval.Approval.Code, "payloadDigest": digest}, &confirmed)
	if err != nil {
		return fmt.Errorf("native operator verification failed: %w", err)
	}
	if confirmed.Approval.State != "confirmed" ||
		confirmed.Approval.ApprovalID != approval.Approval.ApprovalID ||
		confirmed.Approval.PayloadDigest != digest ||
		confirmed.Approval.Operation != approval.Approval.Operation {
		return errors.New("the daemon did not confirm this exact operation and payload; nothing was submitted")
	}
	return nil
}

// submitOperatorAction sends the action once, with the approval header.
//
// The body is the SAME byte slice that was digested. Re-marshalling here would be a silent break
// of the binding: the approval covers bytes, not a JSON value.
func (c *commandContext) submitOperatorAction(
	ctx context.Context,
	action operatorAction,
	operation principal.OperatorOperation,
	body []byte,
	approvalID string,
) (operatorActionResult, error) {
	var out operatorRawResult
	headers := map[string]string{principal.HeaderOperatorApproval: approvalID}
	if err := c.doRawJSONWithHeadersAndTimeout(
		ctx, action.method, operation.Path, body, &out, headers, commandTimeout,
	); err != nil {
		return operatorActionResult{}, operatorSubmitError(err)
	}
	return operatorActionResult{raw: out.raw}, nil
}

// operatorRawResult captures the raw response bytes of an action.
type operatorRawResult struct {
	raw json.RawMessage
}

// UnmarshalJSON keeps the exact bytes so the CLI can print them without a lossy round trip.
func (r *operatorRawResult) UnmarshalJSON(data []byte) error {
	r.raw = append(json.RawMessage(nil), data...)
	return nil
}

// operatorSubmitError renders a refusal that happened after the human did their part.
func operatorSubmitError(err error) error {
	var apiErr apiResponseError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorBody.Code {
		case principal.CodeApprovalInvalid:
			return fmt.Errorf(
				"the daemon refused the approval; request a new approval and try again: %w", err)
		case principal.CodeApprovalRequired:
			return fmt.Errorf("the daemon requires an operator approval: %w", err)
		}
	}
	return err
}

// --- response summaries -----------------------------------------------------
//
// Each summarizer reads only fields the operator needs to know the action happened. They never
// echo a credential and never invent a success: an absent field is reported as absent.

func operatorBindSummary(raw []byte) string {
	var out struct {
		Binding struct {
			AttemptID string `json:"attemptId"`
			State     string `json:"state"`
			SessionID string `json:"sessionId"`
		} `json:"binding"`
		Spawned    bool   `json:"spawned"`
		Frozen     bool   `json:"frozen"`
		ReasonCode string `json:"reasonCode"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return fmt.Sprintf("binding: %s state=%s session=%s spawned=%t frozen=%t reason=%s",
		out.Binding.AttemptID, out.Binding.State, out.Binding.SessionID,
		out.Spawned, out.Frozen, out.ReasonCode)
}

func operatorBindingSummary(raw []byte) string {
	var out struct {
		Binding struct {
			AttemptID string `json:"attemptId"`
			State     string `json:"state"`
		} `json:"binding"`
		Spawned    bool   `json:"spawned"`
		Frozen     bool   `json:"frozen"`
		ReasonCode string `json:"reasonCode"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return fmt.Sprintf("binding: %s state=%s spawned=%t frozen=%t reason=%s",
		out.Binding.AttemptID, out.Binding.State, out.Spawned, out.Frozen, out.ReasonCode)
}

func operatorAttemptSummary(raw []byte) string {
	var out struct {
		Attempt struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"attempt"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return fmt.Sprintf("attempt: %s state=%s", out.Attempt.ID, out.Attempt.State)
}

func operatorDecideSummary(raw []byte) string {
	var out struct {
		Request struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"request"`
		Materialization string `json:"materialization"`
		Message         string `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	line := fmt.Sprintf("request: %s state=%s materialization=%s",
		out.Request.ID, out.Request.State, out.Materialization)
	if out.Message != "" {
		line += " — " + out.Message
	}
	return line
}
