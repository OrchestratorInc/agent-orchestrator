package workerexec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/google/uuid"
)

type codexFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type codexRPC struct {
	input          io.Writer
	writeMu        sync.Mutex
	mu             sync.Mutex
	nextID         int64
	pending        map[int64]chan codexFrame
	done           chan struct{}
	onNotification func(codexFrame)
	onRequest      func(codexFrame)
}

// codexRPCError is an app-server JSON-RPC error response.
type codexRPCError struct {
	Method  string
	Message string
	Code    int
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("Codex %s: %s (%d)", e.Method, e.Message, e.Code)
}

// codexMethodNotFound is the JSON-RPC code for a method this Codex lacks.
const codexMethodNotFound = -32601

func newCodexRPC(input io.Writer, output io.Reader) *codexRPC {
	c := &codexRPC{input: input, pending: make(map[int64]chan codexFrame), done: make(chan struct{})}
	go c.read(output)
	return c
}

func (c *codexRPC) read(output io.Reader) {
	defer close(c.done)
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var frame codexFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			continue
		}
		if len(frame.ID) > 0 && frame.Method == "" {
			var id int64
			if json.Unmarshal(frame.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			pending := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if pending != nil {
				pending <- frame
			}
			continue
		}
		if len(frame.ID) > 0 && frame.Method != "" {
			c.mu.Lock()
			callback := c.onRequest
			c.mu.Unlock()
			if callback != nil {
				go callback(frame)
			}
			continue
		}
		if frame.Method != "" {
			c.mu.Lock()
			callback := c.onNotification
			c.mu.Unlock()
			if callback != nil {
				callback(frame)
			}
		}
	}
}

func (c *codexRPC) send(frame any) error {
	encoded, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.input.Write(append(encoded, '\n'))
	return err
}

func (c *codexRPC) request(ctx context.Context, method string, params any, response any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	answer := make(chan codexFrame, 1)
	c.pending[id] = answer
	c.mu.Unlock()
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	select {
	case frame := <-answer:
		if frame.Error != nil {
			return &codexRPCError{Method: method, Message: frame.Error.Message, Code: frame.Error.Code}
		}
		if response != nil {
			return json.Unmarshal(frame.Result, response)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("Codex app-server disconnected")
	}
}

func (c *codexRPC) notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}

type codexSession struct {
	conn           *codexRPC
	threadID       string
	turnID         string
	mu             sync.Mutex
	providerTurnID string
	// lastError is the final "error" notification for this thread, which
	// explains a failed turn whose completion carries no error of its own.
	lastError *codexTurnError
}

// reason is the turn's own error, or else the last final error Codex sent.
func (s *codexSession) reason(turnErr *codexTurnError) *codexTurnError {
	if turnErr != nil && strings.TrimSpace(turnErr.Message+turnErr.AdditionalDetails) != "" {
		return turnErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastError
}

func (s *codexSession) Steer(ctx context.Context, turnID, text string) error {
	if turnID != s.turnID || strings.TrimSpace(text) == "" {
		return errors.New("the steer does not target this Codex turn")
	}
	s.mu.Lock()
	providerID := s.providerTurnID
	s.mu.Unlock()
	if providerID == "" {
		return errors.New("Codex has not started the turn yet")
	}
	var result struct {
		TurnID string `json:"turnId"`
	}
	err := s.conn.request(ctx, "turn/steer", map[string]any{
		"threadId":       s.threadID,
		"expectedTurnId": providerID,
		"input":          []map[string]any{{"type": "text", "text": text}},
	}, &result)
	if err != nil {
		return err
	}
	if result.TurnID != "" && result.TurnID != providerID {
		return errors.New("Codex steered a different turn")
	}
	return nil
}

// Codex may finish a turn without a completion notification reaching this
// connection. Its durable thread state is authoritative for that exact turn.
func readCodexTurnStatus(ctx context.Context, conn *codexRPC, threadID, turnID string) (string, *codexTurnError, error) {
	var result struct {
		Thread struct {
			ID    string `json:"id"`
			Turns []struct {
				ID     string          `json:"id"`
				Status string          `json:"status"`
				Error  *codexTurnError `json:"error"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := conn.request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": true}, &result); err != nil {
		return "", nil, err
	}
	if result.Thread.ID != threadID {
		return "", nil, errors.New("Codex returned a different thread")
	}
	for _, providerTurn := range result.Thread.Turns {
		if providerTurn.ID == turnID {
			return providerTurn.Status, providerTurn.Error, nil
		}
	}
	return "", nil, nil
}

// codexTurnError is the error Codex attaches to a failed turn, on
// turn/completed, in thread/read, and in an "error" notification (the
// app-server's TurnError). Message is Codex's own explanation.
type codexTurnError struct {
	Message           string          `json:"message"`
	AdditionalDetails string          `json:"additionalDetails"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo"`
}

// codexListedModels returns the models this Codex lists for its current
// sign-in (model/list, hidden ones included), by id and by model name. ok is
// false when the list is unavailable, so the requested model is used as is.
func codexListedModels(ctx context.Context, conn *codexRPC) (map[string]bool, string, bool) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	listed := map[string]bool{}
	defaultModel := ""
	cursor := ""
	for page := 0; page < 10; page++ {
		params := map[string]any{"includeHidden": true}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Data []struct {
				ID        string `json:"id"`
				Model     string `json:"model"`
				IsDefault bool   `json:"isDefault"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := conn.request(listCtx, "model/list", params, &result); err != nil {
			return nil, "", false
		}
		for _, model := range result.Data {
			if model.IsDefault && defaultModel == "" {
				defaultModel = strings.TrimSpace(model.ID)
			}
			if id := strings.TrimSpace(model.ID); id != "" {
				listed[id] = true
			}
			if name := strings.TrimSpace(model.Model); name != "" {
				listed[name] = true
			}
		}
		if result.NextCursor == nil || *result.NextCursor == "" {
			break
		}
		cursor = *result.NextCursor
	}
	return listed, defaultModel, len(listed) > 0
}

// codexTurnModel is the model a Chat turn runs on: the requested one when this
// Codex lists it for its sign-in (or the list is unavailable), else Codex's
// default model, with a notice naming the model that was dropped. The thread
// gets the same model, so a thread created on the unlisted model (for example
// by the terminal's --model) is moved off it on resume.
func codexTurnModel(ctx context.Context, conn *codexRPC, requested string) (model, notice string) {
	if requested == "" {
		return "", ""
	}
	listed, defaultModel, ok := codexListedModels(ctx, conn)
	if !ok || listed[requested] {
		return requested, ""
	}
	return defaultModel, fmt.Sprintf("AO: %s is not available with the Codex sign-in this session uses, so this turn runs on Codex's default model. Pick a model from Codex's list to choose another.\n\n", requested)
}

// codexTurnErrorMaxLen bounds the provider text a failed turn shows.
const codexTurnErrorMaxLen = 600

// codexTurnFailure is the error a turn that ended as status reports: Codex's
// own reason when it gave one, and what to do about a lapsed sign-in. Without
// a reason it still says the turn failed, so the timeline is never blank.
func codexTurnFailure(status string, turnErr *codexTurnError) error {
	if turnErr == nil || strings.TrimSpace(turnErr.Message+turnErr.AdditionalDetails) == "" {
		return fmt.Errorf("Codex turn ended with %s", status)
	}
	reason := strings.TrimSpace(turnErr.Message)
	if details := strings.TrimSpace(turnErr.AdditionalDetails); details != "" && details != reason {
		if reason == "" {
			reason = details
		} else {
			reason += " (" + details + ")"
		}
	}
	if len(reason) > codexTurnErrorMaxLen {
		reason = strings.TrimSpace(reason[:codexTurnErrorMaxLen]) + "..."
	}
	message := fmt.Sprintf("Codex turn %s: %s", status, reason)
	if codexSignInExpired(turnErr) {
		message += ". Sign in to Codex again for Cloud. A ChatGPT login shared between this computer and the cloud stops working when either side uses it to refresh."
	}
	return errors.New(message)
}

// codexSignInExpired reports a failure Codex attributes to its sign-in: an
// explicit unauthorized reason, or the refresh-token and 401 messages Codex
// gives when a ChatGPT login was refreshed elsewhere.
func codexSignInExpired(turnErr *codexTurnError) bool {
	if strings.TrimSpace(string(turnErr.CodexErrorInfo)) == `"unauthorized"` {
		return true
	}
	text := strings.ToLower(turnErr.Message + " " + turnErr.AdditionalDetails)
	return strings.Contains(text, "refresh token") || strings.Contains(text, "401 unauthorized") ||
		strings.Contains(text, "sign in again") || strings.Contains(text, "log out and sign in")
}

func (s *Supervisor) runCodex(ctx context.Context, turn worker.Turn, command Command, publish func(Output) error, identity func(string) error) error {
	control, ok := s.Control.(approvalControl)
	if !ok {
		return errors.New("Cloud approval control is unavailable")
	}
	if command.Path == codexSpec.binary {
		// A fresh workspace may still be installing the pinned Codex.
		ensureCtx, cancel := context.WithTimeout(ctx, claudeCodeTurnWait)
		command.Path = EnsureCodex(ensureCtx)
		cancel()
	}
	process := exec.CommandContext(ctx, command.Path, append(codexSecretFileGuardArgs(command.SecretFileGuard), "app-server")...)
	configureProviderProcess(process)
	process.Dir = command.Dir
	process.Env = mergedEnvironment(command.Env)
	stdin, err := process.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr lockedBuffer
	process.Stderr = &stderr
	if err := process.Start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	defer func() { _ = stopProviderProcess(process); _ = process.Wait() }()
	conn := newCodexRPC(stdin, stdout)
	openCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := conn.request(openCtx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "ao-cloud", "title": "AO Cloud", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}, nil); err != nil {
		return fmt.Errorf("initialize Codex: %w: %s", err, boundedError(stderr.String()))
	}
	if err := conn.notify("initialized", map[string]any{}); err != nil {
		return err
	}
	if command.SecretFileGuard != "" {
		if err := trustCodexSecretFileGuard(openCtx, conn, command.Dir, command.SecretFileGuard); errors.Is(err, errCodexHooksUnsupported) {
			// A template's older Codex without session hooks keeps working, as
			// it did before the guard existed; its file tools were never
			// covered by deny rules either.
			s.Logger.Warn("Codex in this workspace cannot run AO's secret-file guard; Chat runs without it", "error", err)
		} else if err != nil {
			return fmt.Errorf("protect secret files in Codex: %w", err)
		}
	}
	policy, sandbox, reviewer := codexApprovalSettings(turn)
	threadParams := map[string]any{
		"cwd": command.Dir, "approvalPolicy": policy, "approvalsReviewer": reviewer, "sandbox": sandbox,
	}
	if command.SystemPrompt != "" {
		threadParams["developerInstructions"] = command.SystemPrompt
	}
	model, modelNotice := codexTurnModel(openCtx, conn, turn.Model)
	if model != "" {
		threadParams["model"] = model
	}
	if turn.ReasoningEffort != "" {
		threadParams["config"] = map[string]string{"model_reasoning_effort": turn.ReasoningEffort}
	}
	threadID := turn.AgentSessionID
	if threadID != "" {
		threadParams["threadId"] = threadID
		if err := conn.request(openCtx, "thread/resume", threadParams, nil); err != nil {
			return fmt.Errorf("resume Codex thread: %w", err)
		}
	} else {
		var result struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := conn.request(openCtx, "thread/start", threadParams, &result); err != nil {
			return fmt.Errorf("start Codex thread: %w", err)
		}
		threadID = result.Thread.ID
		if threadID == "" {
			return errors.New("Codex returned no thread id")
		}
		if err := identity(threadID); err != nil {
			return err
		}
	}
	session := &codexSession{conn: conn, threadID: threadID, turnID: turn.ID}
	completed := make(chan error, 1)
	conn.mu.Lock()
	conn.onNotification = func(frame codexFrame) {
		switch frame.Method {
		case "turn/started":
			var event struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			if json.Unmarshal(frame.Params, &event) == nil && event.ThreadID == threadID {
				session.mu.Lock()
				session.providerTurnID = event.Turn.ID
				session.mu.Unlock()
			}
		case "item/agentMessage/delta", "item/started", "item/completed", "item/commandExecution/outputDelta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded", "turn/diff/updated":
			for _, output := range projectCodexNotification(frame, threadID) {
				if err := publish(output); err != nil {
					select {
					case completed <- err:
					default:
					}
					break
				}
			}
		case "error":
			// A final error (willRetry false) explains the failed turn that
			// follows; keep it in case turn/completed carries none.
			var event struct {
				ThreadID  string         `json:"threadId"`
				Error     codexTurnError `json:"error"`
				WillRetry bool           `json:"willRetry"`
			}
			if json.Unmarshal(frame.Params, &event) == nil && event.ThreadID == threadID && !event.WillRetry {
				session.mu.Lock()
				session.lastError = &event.Error
				session.mu.Unlock()
			}
		case "turn/completed":
			var event struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID     string          `json:"id"`
					Status string          `json:"status"`
					Error  *codexTurnError `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(frame.Params, &event) == nil && event.ThreadID == threadID {
				var result error
				if event.Turn.Status != "completed" {
					result = codexTurnFailure(event.Turn.Status, session.reason(event.Turn.Error))
				}
				select {
				case completed <- result:
				default:
				}
			}
		}
	}
	conn.onRequest = func(frame codexFrame) { handleCodexApproval(ctx, conn, control, turn, frame) }
	conn.mu.Unlock()
	turnParams := map[string]any{
		"threadId": threadID, "input": []map[string]any{{"type": "text", "text": turn.Prompt}},
		"approvalPolicy": policy, "approvalsReviewer": reviewer,
		"sandboxPolicy": codexTurnSandbox(sandbox, command.WritableRoots),
	}
	// The desktop's model list comes from the local Codex, which may sign in
	// differently: a ChatGPT login cannot run every API model, and Codex fails
	// such a turn with a 400. codexTurnModel moved an unlisted model to
	// Codex's default; say so in the turn.
	if modelNotice != "" {
		if err := publish(Output{Stream: "stdout", Text: modelNotice, ItemID: "ao-model-notice-" + turn.ID}); err != nil {
			return err
		}
	}
	if model != "" {
		turnParams["model"] = model
	}
	if turn.ReasoningEffort != "" {
		turnParams["effort"] = turn.ReasoningEffort
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := conn.request(ctx, "turn/start", turnParams, &started); err != nil {
		return fmt.Errorf("start Codex turn: %w", err)
	}
	if started.Turn.ID == "" {
		return errors.New("Codex returned no turn id")
	}
	s.activeMu.Lock()
	s.activeCodex = session
	s.activeMu.Unlock()
	defer func() {
		s.activeMu.Lock()
		if s.activeCodex == session {
			s.activeCodex = nil
		}
		s.activeMu.Unlock()
	}()
	if publisher, ok := s.Control.(capabilityPublisher); ok {
		if err := publisher.PublishTurnCapabilities(ctx, turn.ID, turn.Attempt, true); err != nil {
			return err
		}
	}
	completionPoll := time.NewTicker(2 * time.Second)
	defer completionPoll.Stop()
	for {
		select {
		case err := <-completed:
			return err
		case <-completionPoll.C:
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			status, turnErr, readErr := readCodexTurnStatus(readCtx, conn, threadID, started.Turn.ID)
			cancel()
			if readErr != nil {
				continue
			}
			switch status {
			case "completed":
				return nil
			case "failed", "interrupted":
				return codexTurnFailure(status, session.reason(turnErr))
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-conn.done:
			return errors.New("Codex app-server disconnected before completing the turn")
		}
	}
}

// codexApprovalModes are the approval modes a Codex turn runs
// (codexApprovalSettings). Codex applies each as its own approval policy and
// sandbox whatever the model, so every Codex model offers all of them.
var codexApprovalModes = []string{"default", "accept-edits", "auto", "bypass-permissions"}

func codexApprovalSettings(turn worker.Turn) (policy, sandbox, reviewer string) {
	reviewer = "user"
	if turn.Mode == "read-only" {
		return "never", "read-only", reviewer
	}
	approval := turn.ApprovalMode
	if approval == "" {
		if turn.Mode == "standard" {
			approval = "auto"
		} else {
			approval = "default"
		}
	}
	switch approval {
	case "accept-edits":
		return "on-request", "workspace-write", reviewer
	case "auto":
		return "on-request", "workspace-write", "auto_review"
	default:
		return "never", "danger-full-access", reviewer
	}
}

// codexTurnSandbox is a turn's sandbox policy. A workspace-write sandbox also
// lets the turn write writableRoots, the session's checkouts outside its
// working directory.
func codexTurnSandbox(sandbox string, writableRoots []string) map[string]any {
	switch sandbox {
	case "read-only":
		return map[string]any{"type": "readOnly"}
	case "workspace-write":
		policy := map[string]any{"type": "workspaceWrite"}
		if len(writableRoots) > 0 {
			policy["writableRoots"] = writableRoots
		}
		return policy
	default:
		return map[string]any{"type": "dangerFullAccess"}
	}
}

func handleCodexApproval(ctx context.Context, conn *codexRPC, control approvalControl, turn worker.Turn, frame codexFrame) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if frame.Method != "item/commandExecution/requestApproval" && frame.Method != "item/fileChange/requestApproval" && frame.Method != "item/permissions/requestApproval" {
		_ = conn.send(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32601, "message": "unsupported request"}})
		return
	}
	var request struct {
		Command            string            `json:"command"`
		AvailableDecisions []json.RawMessage `json:"availableDecisions"`
	}
	if json.Unmarshal(frame.Params, &request) != nil || len(request.AvailableDecisions) == 0 {
		_ = conn.send(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32602, "message": "invalid approval choices"}})
		return
	}
	options := make([]map[string]string, 0, len(request.AvailableDecisions))
	offered := make(map[string]json.RawMessage)
	for _, raw := range request.AvailableDecisions {
		id := ""
		if json.Unmarshal(raw, &id) != nil {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil || len(object) != 1 {
				continue
			}
			for key := range object {
				id = key
			}
		}
		if id == "" {
			continue
		}
		offered[id] = raw
		kind := ""
		if strings.HasPrefix(id, "accept") {
			kind = "allow_once"
		}
		if id == "acceptForSession" {
			kind = "allow_always"
		}
		if id == "decline" || id == "cancel" {
			kind = "reject_once"
		}
		options = append(options, map[string]string{"id": id, "label": id, "kind": kind})
	}
	if len(options) == 0 {
		_ = conn.send(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32602, "message": "no supported approval choices"}})
		return
	}
	encoded, _ := json.Marshal(options)
	summary := "Apply file changes"
	if request.Command != "" {
		summary = "Run " + request.Command
	}
	if len(summary) > 4096 {
		summary = summary[:4096]
	}
	requestID := uuid.NewString()
	if err := control.CreateChatApproval(ctx, worker.ChatApproval{RequestID: requestID, TurnID: turn.ID, Attempt: turn.Attempt, Summary: summary, Decisions: encoded}); err != nil {
		_ = conn.send(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32000, "message": "approval could not be recorded"}})
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		decision, err := control.ChatApprovalDecision(ctx, turn.ID, turn.Attempt, requestID)
		if err != nil {
			_ = conn.send(map[string]any{"id": frame.ID, "error": map[string]any{"code": -32000, "message": "approval decision unavailable"}})
			return
		}
		if raw, ok := offered[decision]; ok {
			_ = conn.send(map[string]any{"id": frame.ID, "result": map[string]any{"decision": raw}})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
