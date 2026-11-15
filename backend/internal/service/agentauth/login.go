package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

const loginLifetime = 15 * time.Minute

const claudeSetupTokenMethod = "setup_token"

var claudeSetupTokenPattern = regexp.MustCompile(`^sk-ant-oat[0-9A-Za-z_-]{10,}$`)

type LoginMethod struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

func loginMethods(agentID string) []LoginMethod {
	if _, ok := catalogProviders(agentID); ok {
		methods := []LoginMethod{{catalogKeyMethod, "Provider API key", true}}
		for _, method := range nativeLoginMethods(agentID) {
			if !method.Secret {
				methods = append(methods, method)
			}
		}
		return methods
	}
	methods := nativeLoginMethods(agentID)
	return append(methods, providerEnvironment[agentID]...)
}

func nativeLoginMethods(agentID string) []LoginMethod {
	switch agentID {
	case "claude-code":
		return []LoginMethod{{"browser", "Claude subscription", false}, {"console", "Anthropic Console", false}, {"sso", "Organization SSO", false}, {claudeSetupTokenMethod, "Setup token", true}}
	case "codex":
		return []LoginMethod{{"browser", "ChatGPT in browser", false}, {"device_code", "Device code", false}, {"api_key", "OpenAI API key", true}, {"access_token", "Access token", true}}
	case "cline":
		return []LoginMethod{{"browser", "Cline account in browser", false}, {"native", "Other native sign-in", false}}
	case "kiro":
		return []LoginMethod{{"browser", "Kiro account in browser", false}}
	case "cursor":
		return []LoginMethod{{"browser", "Cursor account in browser", false}}
	case "continue":
		return []LoginMethod{{"browser", "Continue account in browser", false}}
	case "grok":
		return []LoginMethod{{"browser", "Grok account in browser", false}}
	case "command-code":
		return []LoginMethod{{"browser", "Command Code account in browser", false}}
	case "opencode", "opencode-v2":
		return []LoginMethod{{"anthropic_key", "Anthropic API key", true}, {"openai_key", "OpenAI API key", true}, {"openrouter_key", "OpenRouter API key", true}, {"native", "Other native sign-in", false}}
	case "codewhale":
		return []LoginMethod{{"codewhale_deepseek_key", "DeepSeek API key", true}, {"codewhale_openai_key", "OpenAI API key", true}, {"codewhale_openrouter_key", "OpenRouter API key", true}}
	case "aider":
		return []LoginMethod{{"aider_openai_key", "OpenAI API key", true}, {"aider_anthropic_key", "Anthropic API key", true}, {"aider_openrouter_key", "OpenRouter API key", true}, {"aider_deepseek_key", "DeepSeek API key", true}, {"aider_gemini_key", "Gemini API key", true}}
	case "gemini":
		return []LoginMethod{{"gemini_api_key", "Google AI Studio API key", true}, {"native", "Other native sign-in", false}}
	case "deepseek-harness":
		return []LoginMethod{{"deepseek_api_key", "DeepSeek API key", true}, {"native", "Other native setup", false}}
	case "copilot":
		return []LoginMethod{{"browser", "GitHub account in browser", false}, {"device_code", "Device code", false}, {"copilot_token", "GitHub token", true}, {"native", "Other native sign-in", false}}
	case "kimi":
		return []LoginMethod{{"device_code_global", "Kimi account (kimi.ai)", false}, {"device_code_cn", "Kimi account (kimi.com)", false}, {"native", "Other native sign-in", false}}
	default:
		if plan, ok := planByAgentID[agentID]; ok && len(plan.command) > 0 && plan.LaunchMode == LaunchTerminal && (plan.terminalInput == "" || plan.initialInput != "") {
			return []LoginMethod{{"native", "Native sign-in", false}}
		}
		return nil
	}
}

type LoginSnapshot struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agentId"`
	Method         string    `json:"method"`
	Status         string    `json:"status" enum:"running,succeeded,failed,cancelled,timed_out"`
	LoginURL       string    `json:"loginUrl,omitempty"`
	DeviceCode     string    `json:"deviceCode,omitempty"`
	AcceptsCode    bool      `json:"acceptsCode,omitempty"`
	NativePrompt   string    `json:"nativePrompt,omitempty" enum:"waiting,menu,terminal_required"`
	SelectedOption string    `json:"selectedOption,omitempty"`
	NativeOptions  []string  `json:"nativeOptions,omitempty"`
	WithdrawFailed bool      `json:"withdrawFailed,omitempty"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type loginOperation struct {
	snapshot      LoginSnapshot
	cancel        context.CancelFunc
	done          chan struct{}
	terminalID    string
	cleanupFailed bool
	stdin         io.WriteCloser
	codeSubmitted bool
	nativeDigits  []string
}

func (s *Service) StartLogin(ctx context.Context, agentID, method, secret string) (LoginSnapshot, error) {
	return s.StartLoginWithModel(ctx, agentID, method, secret, "")
}

func (s *Service) StartLoginWithModel(ctx context.Context, agentID, method, secret, model string) (LoginSnapshot, error) {
	return s.StartLoginWithOptions(ctx, agentID, method, secret, LoginOptions{Model: model})
}

type LoginOptions struct {
	Model    string
	Device   *bool
	Provider string
}

func (o LoginOptions) sharesDevice() bool { return o.Device != nil && *o.Device }

func (s *Service) StartLoginWithOptions(ctx context.Context, agentID, method, secret string, opts LoginOptions) (LoginSnapshot, error) {
	model := opts.Model
	if (method == catalogKeyMethod) != (opts.Provider != "") {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_PROVIDER_INVALID", "Choose a provider for a provider key, and only for a provider key.", nil)
	}
	if method == catalogKeyMethod && (model != "" || opts.sharesDevice()) {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_PROVIDER_INVALID", "A provider key saved in the harness's own store takes no model or device setting.", nil)
	}
	if _, ok := environmentMethod(agentID, method); !ok && opts.sharesDevice() {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_DEVICE_INVALID", "Only provider keys can be added to the device environment.", nil)
	}
	if name, ok := environmentMethod(agentID, method); ok && strings.TrimSpace(secret) == "" {
		if keys, err := providerkeys.Load(s.dataDir); err == nil {
			secret = keys[name]
		}
	}
	needsModel := (agentID == "qwen" || agentID == "openhands" || agentID == "continue" && method != "env_CONTINUE_API_KEY") && strings.HasPrefix(method, "env_")
	if model != "" && !needsModel {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_MODEL_INVALID", "This login method does not accept a model.", nil)
	}
	if method == "native" {
		if secret != "" {
			return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Native sign-in does not accept a credential when starting.", nil)
		}
		return s.startNativeLogin(ctx, agentID)
	}
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
	setupToken := agentID == "claude-code" && method == claudeSetupTokenMethod
	if setupToken && !claudeSetupTokenPattern.MatchString(secret) {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "A setup token starts with sk-ant-oat and contains no spaces.", nil)
	}
	if setupToken && strings.TrimSpace(s.dataDir) == "" {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Orchestrator cannot save a setup token without a data directory.")
	}
	release, err := s.reserveLogin(ctx, agentID)
	if err != nil {
		return LoginSnapshot{}, err
	}
	defer release()
	withdrawFailed := false
	if isKey, err := s.saveLoginKey(ctx, plan, method, secret, model, opts, &withdrawFailed); isKey {
		if err != nil {
			return LoginSnapshot{}, err
		}
		if !strings.HasPrefix(method, "env_") && (agentID != "gemini" || method != "gemini_api_key") {
			if _, err := s.releaseSubscriptionKey(ctx, agentID); err != nil {
				return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The key was saved, but AO could not stop passing this harness its earlier API key. Try again.")
			}
		}
		return s.recordCompletedLogin(agentID, method, withdrawFailed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Authentication service is stopping.")
	}
	s.pruneOperationsLocked(agentID)
	expires := time.Now().Add(s.loginTimeout)
	if setupToken {
		return s.saveClaudeSetupToken(secret, expires)
	}
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
	cmd.Env = providerkeys.EnvList(append(os.Environ(), "NO_COLOR=1", "TERM=dumb"), providerkeys.WithheldForSignIn(agentID))
	if agentID == "opencode-v2" {
		dataHome, err := opencodev2.DataHome(ctx)
		if err != nil {
			cancel()
			return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_DATA_HOME_UNAVAILABLE", "OpenCode 2's credential home is unavailable.")
		}
		cmd.Env = append(cmd.Env, "XDG_DATA_HOME="+dataHome)
	}
	if needsSecret {
		if agentID == "opencode-v2" {
			credential, _ := json.Marshal([]struct {
				ID            string `json:"id"`
				IntegrationID string `json:"integrationID"`
				Label         string `json:"label"`
				Active        bool   `json:"active"`
				Value         struct {
					Type string `json:"type"`
					Key  string `json:"key"`
				} `json:"value"`
			}{{ID: uuid.NewString(), IntegrationID: strings.TrimSuffix(method, "_key"), Label: "AO", Active: true, Value: struct {
				Type string `json:"type"`
				Key  string `json:"key"`
			}{Type: "key", Key: secret}}})
			cmd.Stdin = strings.NewReader(string(credential) + "\n")
		} else {
			cmd.Stdin = strings.NewReader(secret + "\n")
		}
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
	afterLogin := context.WithoutCancel(ctx)
	go func() {
		err := cmd.Wait()
		if op.stdin != nil {
			_ = op.stdin.Close()
		}
		var releaseErr error
		if err == nil && loginCtx.Err() == nil {
			releaseCtx, releaseCancel := context.WithTimeout(afterLogin, 5*time.Second)
			_, releaseErr = s.releaseSubscriptionKey(releaseCtx, agentID)
			releaseCancel()
		}
		s.mu.Lock()
		if op.snapshot.Status == "running" {
			switch {
			case loginCtx.Err() != nil:
				op.snapshot.Status = "timed_out"
			case err != nil:
				op.snapshot.Status = "failed"
			case releaseErr != nil:
				op.snapshot.Status = "failed"
			default:
				op.snapshot.Status = "succeeded"
				if agentID == "claude-code" && s.dataDir != "" {
					_ = claudecode.RemoveLocalOAuthToken(s.dataDir)
				}
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

func (s *Service) saveClaudeSetupToken(token string, expires time.Time) (LoginSnapshot, error) {
	ctx := context.Background()
	released, err := s.releaseSubscriptionKey(ctx, "claude-code")
	if err != nil {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_SAVE_FAILED", "Claude Code's saved Anthropic API key could not be switched off, so the setup token was not saved.")
	}
	if err := claudecode.SaveLocalOAuthToken(s.dataDir, []byte(token)); err != nil {
		if restoreErr := s.restoreSubscriptionKey(ctx, "claude-code", released); restoreErr != nil {
			return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_SAVE_FAILED", "The setup token could not be saved, and Claude Code's saved Anthropic API key could not be switched back on. Save the key again from this panel.")
		}
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_SAVE_FAILED", "The setup token could not be saved.")
	}
	done := make(chan struct{})
	close(done)
	op := &loginOperation{snapshot: LoginSnapshot{ID: uuid.NewString(), AgentID: "claude-code", Method: claudeSetupTokenMethod, Status: "succeeded", ExpiresAt: expires}, cancel: func() {}, done: done}
	s.operations[op.snapshot.ID] = op
	return op.snapshot, nil
}

type guidedAuthTerminal interface {
	TerminalOpener
	ObserveAuthTerminal(context.Context, string) (shellterm.AuthTerminalObservation, error)
	SendAuthTerminalKey(context.Context, string, string) error
	CloseShellTerminal(context.Context, string) error
}

func (s *Service) startNativeLogin(ctx context.Context, agentID string) (LoginSnapshot, error) {
	plan, ok := planByAgentID[agentID]
	if !ok || len(plan.command) == 0 || plan.LaunchMode != LaunchTerminal {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This harness has no native login command.", nil)
	}
	terminal, ok := s.terminals.(guidedAuthTerminal)
	if !ok {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_TERMINAL_UNAVAILABLE", "Guided native sign-in is unavailable.")
	}
	release, err := s.reserveLogin(ctx, agentID)
	if err != nil {
		return LoginSnapshot{}, err
	}
	defer release()
	plan.hiddenTerminal = true
	result, err := s.startReserved(ctx, plan)
	if err != nil {
		return LoginSnapshot{}, err
	}
	s.mu.Lock()
	for id, op := range s.operations {
		if op.snapshot.AgentID == agentID {
			select {
			case <-op.done:
			default:
				s.mu.Unlock()
				cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
				_ = terminal.CloseShellTerminal(cleanupCtx, result.Terminal.HandleID)
				cancelCleanup()
				return LoginSnapshot{}, apierr.Conflict("AGENT_AUTH_IN_PROGRESS", "A login is already in progress for this harness.", nil)
			}
			delete(s.operations, id)
		}
	}
	expires := time.Now().Add(s.loginTimeout)
	loginCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), expires)
	op := &loginOperation{
		snapshot: LoginSnapshot{ID: uuid.NewString(), AgentID: agentID, Method: "native", Status: "running", NativePrompt: "waiting", ExpiresAt: expires},
		cancel:   cancel, done: make(chan struct{}), terminalID: result.Terminal.HandleID,
	}
	s.operations[op.snapshot.ID] = op
	s.mu.Unlock()
	// #nosec G118 -- The bounded native login must continue after its HTTP request returns.
	go s.watchNativeLogin(loginCtx, terminal, op)
	return op.snapshot, nil
}

func (s *Service) watchNativeLogin(ctx context.Context, terminal guidedAuthTerminal, op *loginOperation) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	defer close(op.done)
	defer op.cancel()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			if op.snapshot.Status == "running" {
				op.snapshot.Status = "timed_out"
			}
			op.snapshot.NativePrompt, op.snapshot.SelectedOption, op.snapshot.NativeOptions = "", "", nil
			s.mu.Unlock()
		case <-ticker.C:
			observation, err := terminal.ObserveAuthTerminal(ctx, op.terminalID)
			if err != nil {
				continue
			}
			if observation.Alive {
				kind, selected, options, digits := nativePrompt(observation.Output)
				s.mu.Lock()
				if op.snapshot.Status == "running" {
					op.snapshot.NativePrompt, op.snapshot.SelectedOption, op.snapshot.NativeOptions = kind, selected, options
					op.nativeDigits = digits
				}
				s.mu.Unlock()
				continue
			}
			probeCtx, probeCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			released, releaseErr := s.releaseSubscriptionKey(probeCtx, op.snapshot.AgentID)
			state := domain.AgentAuthenticationUnknown
			if releaseErr == nil {
				state = s.nativeSignInState(probeCtx, op.snapshot.AgentID)
				if !nativeSignInFound(state) {
					restoreCtx, restoreCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					_ = s.restoreSubscriptionKey(restoreCtx, op.snapshot.AgentID, released)
					restoreCancel()
				}
			}
			probeCancel()
			s.mu.Lock()
			if op.snapshot.Status == "running" {
				op.snapshot.Status = "failed"
				if releaseErr == nil && nativeSignInFound(state) {
					op.snapshot.Status = "succeeded"
				}
			}
			op.snapshot.NativePrompt, op.snapshot.SelectedOption, op.snapshot.NativeOptions = "", "", nil
			s.mu.Unlock()
		}
		for attempt := 0; attempt < 3; attempt++ {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := terminal.CloseShellTerminal(cleanupCtx, op.terminalID)
			cancel()
			if err == nil {
				return
			}
			var apiError *apierr.Error
			if errors.As(err, &apiError) && apiError.Kind == apierr.KindNotFound {
				return
			}
			if attempt < 2 {
				time.Sleep(250 * time.Millisecond)
			}
		}
		s.mu.Lock()
		op.cleanupFailed = true
		op.snapshot.Status = "failed"
		s.mu.Unlock()
		return
	}
}

func nativeSignInFound(state domain.AgentAuthenticationState) bool {
	return state == domain.AgentAuthenticationAuthorized || state == domain.AgentAuthenticationConfigured
}

type signInProber interface {
	InvalidateAgentAuthentication(agentID string)
	EnsureAgentReadiness(ctx context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error)
}

func (s *Service) nativeSignInState(ctx context.Context, agentID string) domain.AgentAuthenticationState {
	prober, ok := s.agents.(signInProber)
	if !ok {
		return domain.AgentAuthenticationUnknown
	}
	prober.InvalidateAgentAuthentication(agentID)
	snapshot, err := prober.EnsureAgentReadiness(ctx, agentID, domain.AgentReadinessPurposeLaunch)
	if err != nil {
		return domain.AgentAuthenticationUnknown
	}
	return snapshot.Authentication.State
}

var (
	nativeSelectedLine     = regexp.MustCompile(`(?m)^\s*[>❯●◉‣▸►]\s*([^\r\n]+?)\s*$`)
	nativeNumberedLine     = regexp.MustCompile(`(?m)^\s*(?:[>❯●◉‣▸►]\s*)?([1-9])[.)]\s+([^\r\n]+?)\s*$`)
	nativeInteractiveWords = regexp.MustCompile(`(?i)\b(select|choose|enter|password|api key|token|credential)s?\b`)
	nativeMenuGlyphs       = regexp.MustCompile(`^[\s>❯›»?:·•●○◉*-]*$`)
	nativeMenuPrompt       = regexp.MustCompile(`^.{0,60}[:?>❯›]\s*[1-9]?$`)
	nativeMenuHint         = regexp.MustCompile(`(?i)(arrow|↑|↓|enter|esc|navigate|confirm|cancel|number)`)
	nativeSecretWords      = regexp.MustCompile(`(?i)(api key|token|password|secret|credential|paste|\bcode\b)`)
)

const nativeScreenLines = 40

var nativeOptions = map[string]string{
	"anthropic": "Anthropic", "anthropic console": "Anthropic Console", "openai": "OpenAI", "openrouter": "OpenRouter", "google": "Google", "google cloud": "Google Cloud", "gemini": "Gemini", "github": "GitHub", "github.com": "GitHub.com", "github enterprise": "GitHub Enterprise", "github copilot": "GitHub Copilot", "copilot": "Copilot", "microsoft entra": "Microsoft Entra", "chatgpt": "ChatGPT", "claude": "Claude", "kiro": "Kiro", "aws builder id": "AWS Builder ID", "api key": "API key", "oauth": "OAuth", "browser": "Browser", "device code": "Device code", "qwen": "Qwen", "deepseek": "DeepSeek", "kimi": "Kimi", "mistral": "Mistral", "grok": "Grok", "xai": "xAI", "azure": "Azure", "vertex ai": "Vertex AI", "vercel": "Vercel", "ollama": "Ollama",
}

func nativePrompt(output string) (kind, selected string, options, digits []string) {
	plain := terminalui.PlainTerminalText(output)
	if len(plain) > 16*1024 {
		plain = plain[len(plain)-16*1024:]
	}
	var lines []string
	for _, line := range strings.Split(plain, "\n") {
		line = strings.TrimRight(strings.TrimLeft(line, " \t|│┃║"), " \t\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	if options, digits, entries := currentNumberedMenu(lines); entries > 1 {
		if len(options) == entries {
			return "menu", "", options, digits
		}
		return "terminal_required", "", nil, nil
	}
	screen := lines[max(0, len(lines)-nativeScreenLines):]
	for i := len(screen) - 1; i >= 0; i-- {
		match := nativeSelectedLine.FindStringSubmatch(screen[i])
		if match == nil || nativeNumberedLine.MatchString(screen[i]) {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(match[1]))
		if strings.Trim(label, "0123456789") == "" {
			continue
		}
		if canonical, ok := nativeOptions[label]; ok {
			return "menu", canonical, nil, nil
		}
		return "terminal_required", "", nil, nil
	}
	if nativeInteractiveWords.MatchString(strings.Join(lastTextLines(lines, 2), "\n")) {
		return "terminal_required", "", nil, nil
	}
	return "waiting", "", nil, nil
}

func lastTextLines(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.IndexFunc(lines[i], unicode.IsLetter) >= 0 {
			out = append(out, lines[i])
		}
	}
	return out
}

func currentNumberedMenu(lines []string) (options, digits []string, entries int) {
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if nativeNumberedLine.MatchString(lines[i]) {
			last = i
			break
		}
	}
	if last < 0 {
		return nil, nil, 0
	}
	for i, line := range lines[last+1:] {
		if !nativeMenuTail(line, last+1+i == len(lines)-1) {
			return nil, nil, 0
		}
	}
	first := 0
	for i := last; i >= 0; i-- {
		if match := nativeNumberedLine.FindStringSubmatch(lines[i]); len(match) > 1 && match[1] == "1" {
			first = i
			break
		}
	}
	seen := map[string]bool{}
	for _, line := range lines[first : last+1] {
		match := nativeNumberedLine.FindStringSubmatch(line)
		if len(match) < 3 || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		entries++
		if canonical, ok := nativeOptions[strings.ToLower(match[2])]; ok {
			options = append(options, canonical)
			digits = append(digits, match[1])
		}
	}
	return options, digits, entries
}

func nativeMenuTail(line string, final bool) bool {
	text := strings.TrimSpace(line)
	switch {
	case nativeSecretWords.MatchString(text):
		return false
	case nativeMenuGlyphs.MatchString(text):
		return true
	case final && nativeMenuPrompt.MatchString(text):
		return true
	}
	return len(text) <= 80 && !strings.ContainsAny(text, "0123456789") && nativeMenuHint.MatchString(text)
}

func (s *Service) ActOnLogin(ctx context.Context, agentID, id, action, option string) (LoginSnapshot, error) {
	s.mu.Lock()
	op := s.operations[id]
	if op == nil || op.snapshot.AgentID != agentID {
		s.mu.Unlock()
		return LoginSnapshot{}, apierr.NotFound("AGENT_AUTH_OPERATION_NOT_FOUND", "Login operation not found.")
	}
	if op.snapshot.Method != "native" || op.snapshot.Status != "running" || op.snapshot.NativePrompt != "menu" {
		s.mu.Unlock()
		return LoginSnapshot{}, apierr.Conflict("AGENT_AUTH_ACTION_UNAVAILABLE", "There is no recognized native menu to control.", nil)
	}
	key := action
	if len(op.snapshot.NativeOptions) > 0 {
		switch {
		case action == "select":
		case len(action) == 1 && action >= "1" && action <= "9" && int(action[0]-'0') <= len(op.nativeDigits):
			if option != "" && op.snapshot.NativeOptions[action[0]-'1'] != option {
				s.mu.Unlock()
				return LoginSnapshot{}, apierr.Conflict("AGENT_AUTH_MENU_CHANGED", "The sign-in menu changed. Choose again.", nil)
			}
			key = op.nativeDigits[action[0]-'1']
		default:
			s.mu.Unlock()
			return LoginSnapshot{}, apierr.Conflict("AGENT_AUTH_ACTION_UNAVAILABLE", "Choose one of the listed options.", nil)
		}
	} else if action != "previous" && action != "next" && action != "select" {
		s.mu.Unlock()
		return LoginSnapshot{}, apierr.Conflict("AGENT_AUTH_ACTION_UNAVAILABLE", "Choose a supported login menu action.", nil)
	}
	terminalID := op.terminalID
	s.mu.Unlock()
	terminal, ok := s.terminals.(guidedAuthTerminal)
	if !ok {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_TERMINAL_UNAVAILABLE", "Guided native sign-in is unavailable.")
	}
	if err := terminal.SendAuthTerminalKey(ctx, terminalID, key); err != nil {
		return LoginSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.snapshot.Status == "running" {
		op.snapshot.NativePrompt, op.snapshot.SelectedOption, op.snapshot.NativeOptions = "waiting", "", nil
		op.nativeDigits = nil
	}
	return op.snapshot, nil
}

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
	if method == catalogKeyMethod {
		if _, ok := providerCatalogs[agentID]; ok {
			return nil, true, nil
		}
	}
	if _, ok := environmentMethod(agentID, method); ok {
		return nil, true, nil
	}
	if agentID == "claude-code" {
		switch method {
		case "browser":
			return []string{"auth", "login", "--claudeai"}, false, nil
		case "console":
			return []string{"auth", "login", "--console"}, false, nil
		case "sso":
			return []string{"auth", "login", "--sso"}, false, nil
		case claudeSetupTokenMethod:
			return nil, true, nil
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
	if agentID == "cline" && method == "browser" {
		return []string{"auth", "cline"}, false, nil
	}
	if method == "browser" {
		switch agentID {
		case "kiro", "cursor", "continue", "grok", "command-code":
			return []string{"login"}, false, nil
		}
	}
	if agentID == "opencode" || agentID == "opencode-v2" {
		switch method {
		case "anthropic_key", "openai_key", "openrouter_key":
			if agentID == "opencode-v2" {
				return []string{"auth", "import", "--standalone"}, true, nil
			}
			return nil, true, nil
		}
	}
	if agentID == "codewhale" {
		switch method {
		case "codewhale_deepseek_key", "codewhale_openai_key", "codewhale_openrouter_key":
			provider := strings.TrimSuffix(strings.TrimPrefix(method, "codewhale_"), "_key")
			return []string{"auth", "set", "--provider", provider, "--api-key-stdin"}, true, nil
		}
	}
	if agentID == "aider" {
		switch method {
		case "aider_openai_key", "aider_anthropic_key", "aider_openrouter_key", "aider_deepseek_key", "aider_gemini_key":
			return nil, true, nil
		}
	}
	if agentID == "gemini" && method == "gemini_api_key" {
		return nil, true, nil
	}
	if agentID == "deepseek-harness" && method == "deepseek_api_key" {
		return nil, true, nil
	}
	if agentID == "copilot" {
		switch method {
		case "browser":
			return []string{"login", "--web-flow"}, false, nil
		case "device_code":
			return []string{"login", "--device-code"}, false, nil
		case "copilot_token":
			return []string{"login", "--with-token"}, true, nil
		}
	}
	if agentID == "kimi" {
		switch method {
		case "device_code_global":
			return []string{"login", "--region", "global"}, false, nil
		case "device_code_cn":
			return []string{"login", "--region", "mainland-cn"}, false, nil
		}
	}
	return nil, false, apierr.Invalid("AGENT_AUTH_METHOD_UNSUPPORTED", "This harness does not support that UI login method. Use terminal login instead.", nil)
}

func directKeyWriter(agentID string) (func(context.Context, string, string) error, bool) {
	switch agentID {
	case "opencode":
		return saveOpenCodeAPIKey, true
	case "aider":
		return saveAiderAPIKey, true
	case "gemini":
		return saveGeminiAPIKey, true
	case "deepseek-harness":
		return saveDeepSeekHarnessKey, true
	}
	return nil, false
}

func (s *Service) saveLoginKey(ctx context.Context, plan Plan, method, secret, model string, opts LoginOptions, withdrawFailed *bool) (isKey bool, err error) {
	store := func(name, value string, share *bool, harness string) error {
		failed, storeErr := s.storeProviderKeyReporting(ctx, name, value, share, harness)
		*withdrawFailed = *withdrawFailed || failed
		return storeErr
	}
	agentID := plan.AgentID
	if method == catalogKeyMethod {
		return true, s.saveCatalogKey(ctx, plan, opts.Provider, secret)
	}
	if writer, ok := directKeyWriter(agentID); ok && !strings.HasPrefix(method, "env_") {
		if err := writer(ctx, method, secret); err != nil {
			return true, err
		}
		if agentID == "gemini" && strings.TrimSpace(s.dataDir) != "" {
			return true, store("GEMINI_API_KEY", strings.TrimSpace(secret), nil, agentID)
		}
		return true, nil
	}
	if name, ok := environmentMethod(agentID, method); ok {
		if agentID == "openhands" {
			model = strings.TrimSpace(model)
			if !providerModelID.MatchString(model) {
				return true, apierr.Invalid("AGENT_AUTH_MODEL_INVALID", "Enter a LiteLLM model ID for OpenHands.", nil)
			}
			stored, err := providerkeys.Load(s.dataDir)
			if err != nil {
				return true, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
			}
			previous, hadPrevious := stored["LLM_MODEL"]
			if err := providerkeys.Save(ctx, s.dataDir, "LLM_MODEL", model); err != nil {
				return true, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The OpenHands model could not be saved.")
			}
			if err := providerkeys.ExportForProbes("LLM_MODEL", model); err != nil {
				undoOpenHandsModel(ctx, s.dataDir, previous, hadPrevious)
				return true, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The OpenHands model could not be activated.")
			}
			if err := store(name, secret, opts.Device, agentID); err != nil {
				undoOpenHandsModel(ctx, s.dataDir, previous, hadPrevious)
				return true, err
			}
			return true, nil
		}
		if agentID == "qwen" {
			undo, err := saveQwenProviderUndoable(ctx, name, model)
			if err != nil {
				return true, err
			}
			if err := store(name, secret, opts.Device, agentID); err != nil {
				undo()
				return true, err
			}
			return true, nil
		}
		if agentID == "continue" && name != "CONTINUE_API_KEY" {
			undo, err := saveContinueProviderUndoable(ctx, name, model, secret)
			if err != nil {
				return true, err
			}
			if err := store(name, secret, opts.Device, agentID); err != nil {
				undo()
				return true, err
			}
			return true, nil
		}
		return true, store(name, secret, opts.Device, agentID)
	}
	return false, nil
}

func undoOpenHandsModel(ctx context.Context, dataDir, previous string, had bool) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if had {
		_ = providerkeys.Save(cleanupCtx, dataDir, "LLM_MODEL", previous)
		_ = providerkeys.ExportForProbes("LLM_MODEL", previous)
		return
	}
	_ = providerkeys.Remove(cleanupCtx, dataDir, "LLM_MODEL")
	_ = providerkeys.RestoreInherited("LLM_MODEL")
}

func (s *Service) recordCompletedLogin(agentID, method string, withdrawFailed bool) (LoginSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Authentication service is stopping.")
	}
	s.pruneOperationsLocked(agentID)
	complete := &loginOperation{snapshot: LoginSnapshot{ID: uuid.NewString(), AgentID: agentID, Method: method, Status: "succeeded", WithdrawFailed: withdrawFailed, ExpiresAt: time.Now()}, done: make(chan struct{})}
	close(complete.done)
	s.operations[complete.snapshot.ID] = complete
	return complete.snapshot, nil
}

func (s *Service) RecordSignIn(ctx context.Context, agentID string) (string, error) {
	if _, ok := planByAgentID[agentID]; !ok {
		return "", apierr.Invalid("AGENT_AUTH_TARGET_UNKNOWN", fmt.Sprintf("unknown agent authentication target %q", agentID), nil)
	}
	released, err := s.releaseSubscriptionKey(ctx, agentID)
	if err != nil {
		return "", apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO could not stop passing this harness its earlier API key.")
	}
	if agentID == "claude-code" && s.dataDir != "" {
		if err := claudecode.RemoveLocalOAuthToken(s.dataDir); err != nil {
			return "", apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO could not remove the earlier Claude Code setup token.")
		}
	}
	return released, nil
}

func (s *Service) pruneOperationsLocked(agentID string) {
	for id, op := range s.operations {
		if op.snapshot.AgentID == agentID {
			delete(s.operations, id)
		}
	}
}

func (s *Service) Login(_ context.Context, agentID, id string) (LoginSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op := s.operations[id]
	if op == nil || op.snapshot.AgentID != agentID {
		return LoginSnapshot{}, apierr.NotFound("AGENT_AUTH_OPERATION_NOT_FOUND", "Login operation not found.")
	}
	return op.snapshot, nil
}

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
		op.snapshot.NativePrompt, op.snapshot.SelectedOption, op.snapshot.NativeOptions = "", "", nil
		op.snapshot.AcceptsCode = false
		if op.cancel != nil {
			op.cancel()
		}
	}
	s.mu.Unlock()
	select {
	case <-op.done:
		if op.cleanupFailed {
			return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_CANCEL_PENDING", "The native login terminal could not be stopped. Restart AO before retrying.")
		}
		return s.Login(ctx, agentID, id)
	case <-ctx.Done():
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_CANCEL_PENDING", "Login is still stopping. Retry before opening another login.")
	}
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	ops := make([]*loginOperation, 0, len(s.operations))
	for _, op := range s.operations {
		if op.snapshot.Status == "running" {
			op.snapshot.Status = "cancelled"
			op.snapshot.LoginURL, op.snapshot.DeviceCode = "", ""
			op.snapshot.NativePrompt, op.snapshot.SelectedOption, op.snapshot.NativeOptions = "", "", nil
			op.snapshot.AcceptsCode = false
		}
		if op.cancel != nil {
			op.cancel()
		}
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
	end := strings.LastIndexAny(w.tail, "\r\n")
	if end < 0 {
		return len(p), nil
	}
	output := loginANSIPattern.ReplaceAllString(w.tail[:end+1], "")
	for _, indices := range loginURLPattern.FindAllStringIndex(output, -1) {
		if indices[1] == len(output) {
			continue
		}
		match := output[indices[0]:indices[1]]
		if safeLoginURL(w.operation.snapshot.AgentID, match) {
			if w.operation.snapshot.AgentID == "claude-code" {
				parsed, _ := url.Parse(match)
				if parsed.Query().Get("code") != "true" {
					continue
				}
				w.operation.snapshot.AcceptsCode = true
			}
			w.operation.snapshot.LoginURL = match
		}
	}
	if strings.HasPrefix(w.operation.snapshot.Method, "device_code") {
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
	if agentID == "cline" {
		return u.Hostname() == "app.cline.bot"
	}
	if agentID == "copilot" {
		return u.Hostname() == "github.com" && (u.Path == "/login/device" || u.Path == "/login/oauth/authorize")
	}
	if agentID == "kimi" {
		switch u.Hostname() {
		case "auth.kimi.ai", "auth.kimi.com", "www.kimi.ai", "www.kimi.com", "kimi.ai", "kimi.com":
			return true
		}
		return false
	}
	if agentID != "claude-code" {
		return false
	}
	if u.Hostname() == "claude.com" {
		return u.Path == "/cai/oauth/authorize"
	}
	return (u.Hostname() == "claude.ai" || u.Hostname() == "console.anthropic.com" || u.Hostname() == "platform.claude.com") && u.Path == "/oauth/authorize"
}
