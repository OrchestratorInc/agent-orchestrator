package agentauth

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const loginLifetime = 15 * time.Minute

// LoginMethod describes a reviewed native login method, never a command.
type LoginMethod struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

func loginMethods(agentID string) []LoginMethod {
	switch agentID {
	case "claude-code":
		return []LoginMethod{{"browser", "Claude subscription", false}, {"console", "Anthropic Console", false}, {"sso", "Organization SSO", false}}
	case "codex":
		return []LoginMethod{{"browser", "ChatGPT in browser", false}, {"device_code", "Device code", false}, {"api_key", "OpenAI API key", true}, {"access_token", "Access token", true}}
	default:
		return nil
	}
}

// LoginSnapshot contains only recognized prompts and process state. A successful
// process must still be checked through the harness's authentication probe.
type LoginSnapshot struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agentId"`
	Method      string    `json:"method"`
	Status      string    `json:"status" enum:"running,succeeded,failed,cancelled,timed_out"`
	LoginURL    string    `json:"loginUrl,omitempty"`
	DeviceCode  string    `json:"deviceCode,omitempty"`
	AcceptsCode bool      `json:"acceptsCode,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type loginOperation struct {
	snapshot      LoginSnapshot
	cancel        context.CancelFunc
	done          chan struct{}
	stdin         io.WriteCloser
	codeSubmitted bool
}

// StartLogin runs a fixed native login in the background. Secrets travel only
// through stdin; they never enter command arguments, terminal replay, or logs.
func (s *Service) StartLogin(ctx context.Context, agentID, method, secret string) (LoginSnapshot, error) {
	args, needsSecret, err := loginArgs(agentID, method)
	if err != nil {
		return LoginSnapshot{}, err
	}
	secret = strings.TrimSpace(secret)
	if (needsSecret && (secret == "" || len(secret) > 16*1024 || strings.ContainsAny(secret, "\r\n\x00"))) || (!needsSecret && secret != "") {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Supply a credential only for a credential login method, on a single line.", nil)
	}
	plan, err := s.Plan(ctx, agentID)
	if err != nil {
		return LoginSnapshot{}, err
	}
	if !plan.Available {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_UNAVAILABLE", plan.Reason, nil)
	}
	if err := ctx.Err(); err != nil {
		return LoginSnapshot{}, err
	}
	release, err := s.reserveLogin(ctx, agentID)
	if err != nil {
		return LoginSnapshot{}, err
	}
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Authentication service is stopping.")
	}
	// Retain only the latest operation per harness. A stale tab cannot cancel
	// its replacement, and repeated logins cannot grow the registry unbounded.
	for id, op := range s.operations {
		if op.snapshot.AgentID == agentID {
			delete(s.operations, id)
		}
	}
	expires := time.Now().Add(s.loginTimeout)
	loginCtx, cancel := context.WithDeadline(context.Background(), expires)
	op := &loginOperation{snapshot: LoginSnapshot{ID: uuid.NewString(), AgentID: agentID, Method: method, Status: "running", ExpiresAt: expires}, cancel: cancel, done: make(chan struct{})}
	cmd := s.commandContext(loginCtx, plan.command[0], args...)
	process.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 3 * time.Second
	if s.dataDir != "" {
		cmd.Dir = filepath.Join(s.dataDir, authWorkspaceRootName, "ui-"+agentID)
		if err := os.MkdirAll(cmd.Dir, 0o700); err != nil {
			cancel()
			return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_WORKSPACE_UNAVAILABLE", "The login workspace could not be prepared.")
		}
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	if needsSecret {
		cmd.Stdin = strings.NewReader(secret + "\n")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	} else {
		if agentID == "claude-code" {
			stdin, pipeErr := cmd.StdinPipe()
			if pipeErr != nil {
				cancel()
				return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_START_FAILED", "The native login input could not be prepared.")
			}
			op.stdin = stdin
		}
		output := &loginOutput{service: s, operation: op}
		cmd.Stdout, cmd.Stderr = output, output
	}
	if err := cmd.Start(); err != nil {
		cancel()
		if op.stdin != nil {
			_ = op.stdin.Close()
		}
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_START_FAILED", "The native login could not start. Try the terminal login.")
	}
	s.operations[op.snapshot.ID] = op
	go func() {
		err := cmd.Wait()
		if op.stdin != nil {
			_ = op.stdin.Close()
		}
		s.mu.Lock()
		if op.snapshot.Status == "running" {
			switch {
			case loginCtx.Err() != nil:
				op.snapshot.Status = "timed_out"
			case err != nil:
				op.snapshot.Status = "failed"
			default:
				op.snapshot.Status = "succeeded"
			}
		}
		op.snapshot.LoginURL, op.snapshot.DeviceCode = "", ""
		op.snapshot.AcceptsCode = false
		s.mu.Unlock()
		cancel()
		close(op.done)
	}()
	return op.snapshot, nil
}

// SubmitLoginCode forwards one Claude fallback code to the existing native
// process. The pipe write never holds the registry/output lock.
func (s *Service) SubmitLoginCode(ctx context.Context, agentID, id, code string) (LoginSnapshot, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\r\n\x00") {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_CODE_INVALID", "Supply a sign-in code on a single line.", nil)
	}
	if err := ctx.Err(); err != nil {
		return LoginSnapshot{}, err
	}
	s.mu.Lock()
	op := s.operations[id]
	if op == nil || op.snapshot.AgentID != agentID {
		s.mu.Unlock()
		return LoginSnapshot{}, apierr.NotFound("AGENT_AUTH_OPERATION_NOT_FOUND", "Login operation not found.")
	}
	if agentID != "claude-code" || op.stdin == nil || op.snapshot.Status != "running" || !op.snapshot.AcceptsCode {
		s.mu.Unlock()
		return LoginSnapshot{}, apierr.Conflict("AGENT_AUTH_CODE_UNAVAILABLE", "This sign-in no longer accepts a code.", nil)
	}
	// Claim the single submission before releasing the lock. Stop parsing output
	// now as a native CLI could echo the submitted code.
	op.snapshot.AcceptsCode = false
	op.codeSubmitted = true
	op.snapshot.LoginURL = ""
	s.mu.Unlock()
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	defer cancelWrite()
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(op.stdin, code+"\n")
		written <- err
	}()
	var writeErr error
	select {
	case writeErr = <-written:
	case <-writeCtx.Done():
		writeErr = writeCtx.Err()
		op.cancel()
		_ = op.stdin.Close()
		<-written
	}
	if writeErr != nil {
		s.mu.Lock()
		if op.snapshot.Status == "running" {
			op.snapshot.Status = "failed"
			op.snapshot.LoginURL, op.snapshot.DeviceCode = "", ""
		}
		s.mu.Unlock()
		op.cancel()
		_ = op.stdin.Close()
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_CODE_FAILED", "The code could not be submitted. Start sign-in again.")
	}
	return s.Login(ctx, agentID, id)
}

func loginArgs(agentID, method string) ([]string, bool, error) {
	if agentID == "claude-code" {
		switch method {
		case "browser":
			return []string{"auth", "login", "--claudeai"}, false, nil
		case "console":
			return []string{"auth", "login", "--console"}, false, nil
		case "sso":
			return []string{"auth", "login", "--sso"}, false, nil
		}
	}
	if agentID == "codex" {
		switch method {
		case "browser":
			return []string{"login"}, false, nil
		case "device_code":
			return []string{"login", "--device-auth"}, false, nil
		case "api_key":
			return []string{"login", "--with-api-key"}, true, nil
		case "access_token":
			return []string{"login", "--with-access-token"}, true, nil
		}
	}
	return nil, false, apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This harness does not support that UI login method. Use terminal login instead.", nil)
}

// Login returns an operation only when it belongs to the selected harness.
func (s *Service) Login(_ context.Context, agentID, id string) (LoginSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.operations[id]
	if op == nil || op.snapshot.AgentID != agentID {
		return LoginSnapshot{}, apierr.NotFound("AGENT_AUTH_OPERATION_NOT_FOUND", "Login operation not found.")
	}
	return op.snapshot, nil
}

// CancelLogin waits for the child tree to stop before a terminal fallback can
// start and write to the same native credential store.
func (s *Service) CancelLogin(ctx context.Context, agentID, id string) (LoginSnapshot, error) {
	s.mu.Lock()
	op := s.operations[id]
	if op == nil || op.snapshot.AgentID != agentID {
		s.mu.Unlock()
		return LoginSnapshot{}, apierr.NotFound("AGENT_AUTH_OPERATION_NOT_FOUND", "Login operation not found.")
	}
	if op.snapshot.Status == "running" {
		op.snapshot.Status = "cancelled"
		op.snapshot.LoginURL, op.snapshot.DeviceCode = "", ""
		op.snapshot.AcceptsCode = false
		op.cancel()
	}
	s.mu.Unlock()
	select {
	case <-op.done:
		return s.Login(ctx, agentID, id)
	case <-ctx.Done():
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_CANCEL_PENDING", "Login is still stopping. Retry before opening another login.")
	}
}

// Close stops background logins when the daemon shuts down.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	ops := make([]*loginOperation, 0, len(s.operations))
	for _, op := range s.operations {
		if op.snapshot.Status == "running" {
			op.snapshot.Status = "cancelled"
			op.snapshot.LoginURL, op.snapshot.DeviceCode = "", ""
			op.snapshot.AcceptsCode = false
		}
		op.cancel()
		ops = append(ops, op)
	}
	s.mu.Unlock()
	for _, op := range ops {
		<-op.done
	}
}

var (
	loginURLPattern   = regexp.MustCompile(`https://[^\s\x1b<>"']+`)
	loginANSIPattern  = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`)
	deviceCodePattern = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,5}\b`)
)

// loginOutput consumes a bounded rolling window, including prompts split over
// writes. Raw CLI output is never returned, logged, or persisted.
type loginOutput struct {
	service   *Service
	operation *loginOperation
	tail      string
}

func (w *loginOutput) Write(p []byte) (int, error) {
	w.service.mu.Lock()
	defer w.service.mu.Unlock()
	if w.operation.snapshot.Status != "running" {
		return len(p), nil
	}
	if w.operation.codeSubmitted {
		return len(p), nil
	}
	w.tail += string(p)
	if len(w.tail) > 16*1024 {
		w.tail = w.tail[len(w.tail)-16*1024:]
	}
	// Never publish a link whose query string might still be arriving.
	end := strings.LastIndexAny(w.tail, "\r\n")
	if end < 0 {
		return len(p), nil
	}
	output := loginANSIPattern.ReplaceAllString(w.tail[:end+1], "")
	for _, indices := range loginURLPattern.FindAllStringIndex(output, -1) {
		// A write may stop in the middle of the URL. Wait for a delimiter.
		if indices[1] == len(output) {
			continue
		}
		match := output[indices[0]:indices[1]]
		if safeLoginURL(w.operation.snapshot.AgentID, match) {
			if w.operation.snapshot.AgentID == "claude-code" {
				parsed, _ := url.Parse(match)
				// Only publish the manual-code fallback. A localhost callback
				// belongs to the CLI's host and cannot be reopened remotely.
				if parsed.Query().Get("code") != "true" {
					continue
				}
				w.operation.snapshot.AcceptsCode = true
			}
			w.operation.snapshot.LoginURL = match
		}
	}
	if w.operation.snapshot.Method == "device_code" {
		if code := deviceCodePattern.FindString(output); code != "" {
			w.operation.snapshot.DeviceCode = code
		}
	}
	return len(p), nil
}

func safeLoginURL(agentID, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	if agentID == "codex" {
		return u.Hostname() == "auth.openai.com" && (u.Path == "/oauth/authorize" || u.Path == "/codex/device")
	}
	if agentID != "claude-code" {
		return false
	}
	if u.Hostname() == "claude.com" {
		return u.Path == "/cai/oauth/authorize"
	}
	return (u.Hostname() == "claude.ai" || u.Hostname() == "console.anthropic.com" || u.Hostname() == "platform.claude.com") && u.Path == "/oauth/authorize"
}
