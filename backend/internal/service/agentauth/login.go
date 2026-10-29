package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencodev2"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

const loginLifetime = 15 * time.Minute

// LoginMethod describes a reviewed native login method, never a command.
type LoginMethod struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

func loginMethods(agentID string) []LoginMethod {
	// A harness with a provider catalog on this device offers every provider
	// in it through one picker instead of a fixed list of key methods.
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
		return []LoginMethod{{"browser", "Claude subscription", false}, {"console", "Anthropic Console", false}, {"sso", "Organization SSO", false}}
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
		// GitHub Enterprise and Microsoft Entra stay in Copilot's own picker.
		return []LoginMethod{{"browser", "GitHub account in browser", false}, {"device_code", "Device code", false}, {"copilot_token", "GitHub token", true}, {"native", "Other native sign-in", false}}
	case "kimi":
		return []LoginMethod{{"device_code_global", "Kimi account (kimi.ai)", false}, {"device_code_cn", "Kimi account (kimi.com)", false}, {"native", "Other native sign-in", false}}
	default:
		if plan, ok := planByAgentID[agentID]; ok && len(plan.command) > 0 && plan.LaunchMode == LaunchTerminal {
			return []LoginMethod{{"native", "Native sign-in", false}}
		}
		return nil
	}
}

// LoginSnapshot contains only recognized prompts and process state. A successful
// process must still be checked through the harness's authentication probe.
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
	// nativeDigits holds, for each NativeOptions entry, the digit the CLI
	// printed for it.
	nativeDigits []string
}

// StartLogin runs a fixed native login or saves a reviewed provider key.
// Native CLI credentials travel through stdin; no credentials enter API
// snapshots, terminal replay, or application logs.
func (s *Service) StartLogin(ctx context.Context, agentID, method, secret string) (LoginSnapshot, error) {
	return s.StartLoginWithModel(ctx, agentID, method, secret, "")
}

// StartLoginWithModel supplies the model identifier required by Qwen,
// Continue, and OpenHands provider-key setup. Other methods reject it.
func (s *Service) StartLoginWithModel(ctx context.Context, agentID, method, secret, model string) (LoginSnapshot, error) {
	return s.StartLoginWithOptions(ctx, agentID, method, secret, LoginOptions{Model: model})
}

// LoginOptions carries the optional parts of a provider-key login.
type LoginOptions struct {
	// Model is required by Qwen, Continue, and OpenHands provider-key setup.
	Model string
	// Device also publishes the key to the OS user environment, for programs
	// started outside AO. AO-launched sessions receive it either way.
	Device bool
	// Provider is the catalog provider a provider_key login configures.
	Provider string
}

// StartLoginWithOptions is StartLogin with a model and device setting for
// provider-key methods. An empty secret reuses the key AO already saved.
func (s *Service) StartLoginWithOptions(ctx context.Context, agentID, method, secret string, opts LoginOptions) (LoginSnapshot, error) {
	model := opts.Model
	if (method == catalogKeyMethod) != (opts.Provider != "") {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_PROVIDER_INVALID", "Choose a provider for a provider key, and only for a provider key.", nil)
	}
	if method == catalogKeyMethod && (model != "" || opts.Device) {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_PROVIDER_INVALID", "A provider key saved in the harness's own store takes no model or device setting.", nil)
	}
	if _, ok := environmentMethod(agentID, method); !ok && opts.Device {
		return LoginSnapshot{}, apierr.Invalid("AGENT_AUTH_DEVICE_INVALID", "Only provider keys can be added to the device environment.", nil)
	}
	if name, ok := environmentMethod(agentID, method); ok && strings.TrimSpace(secret) == "" {
		// Reuse the saved key, for example to choose another model.
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
	release, err := s.reserveLogin(ctx, agentID)
	if err != nil {
		return LoginSnapshot{}, err
	}
	defer release()
	// Key methods write their store under this harness's reservation only.
	// s.mu is taken just to record the result, so a slow store (`omp auth
	// import`, a systemd user bus) never stalls other harnesses' logins.
	if isKey, err := s.saveLoginKey(ctx, plan, method, secret, model, opts); isKey {
		if err != nil {
			return LoginSnapshot{}, err
		}
		return s.recordCompletedLogin(agentID, method)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Authentication service is stopping.")
	}
	s.pruneOperationsLocked(agentID)
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

// guidedAuthTerminal is daemon-internal: the reviewed plan creates its PTY,
// and the UI can send only fixed menu navigation actions.
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
	// The native login outlives the HTTP request but keeps its trace values.
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

// watchNativeLogin reduces private PTY frames to reviewed states. It retains
// ownership until CloseShellTerminal confirms that the child is destroyed.
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
			} // A probe error is not proof of exit.
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
			s.mu.Lock()
			if op.snapshot.Status == "running" {
				op.snapshot.Status = "succeeded"
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

var (
	nativeSelectedLine = regexp.MustCompile(`(?m)^\s*[>❯●◉‣▸►]\s*([^\r\n]+?)\s*$`)
	// A cursor glyph may lead the highlighted entry of a numbered picker.
	nativeNumberedLine     = regexp.MustCompile(`(?m)^\s*(?:[>❯●◉‣▸►]\s*)?([1-9])[.)]\s+([^\r\n]+?)\s*$`)
	nativeInteractiveWords = regexp.MustCompile(`(?i)(select|choose|enter|password|api key|token|credential)`)
)

// Only canonical menu names can cross the credential boundary. Native CLIs
// with other prompts retain an explicit terminal fallback.
var nativeOptions = map[string]string{
	"anthropic": "Anthropic", "anthropic console": "Anthropic Console", "openai": "OpenAI", "openrouter": "OpenRouter", "google": "Google", "google cloud": "Google Cloud", "gemini": "Gemini", "github": "GitHub", "github.com": "GitHub.com", "github enterprise": "GitHub Enterprise", "github copilot": "GitHub Copilot", "copilot": "Copilot", "microsoft entra": "Microsoft Entra", "chatgpt": "ChatGPT", "claude": "Claude", "kiro": "Kiro", "aws builder id": "AWS Builder ID", "api key": "API key", "oauth": "OAuth", "browser": "Browser", "device code": "Device code", "qwen": "Qwen", "deepseek": "DeepSeek", "kimi": "Kimi", "mistral": "Mistral", "grok": "Grok", "xai": "xAI", "azure": "Azure", "vertex ai": "Vertex AI", "vercel": "Vercel", "ollama": "Ollama",
}

// nativePrompt reduces PTY frames to reviewed UI states. An arrow-key picker
// reports its highlighted option; a numbered picker reports the recognized
// choices so the UI can offer them as buttons. Anything unrecognized keeps
// the explicit terminal fallback.
func nativePrompt(output string) (kind, selected string, options, digits []string) {
	plain := terminalui.PlainTerminalText(output)
	if len(plain) > 16*1024 {
		plain = plain[len(plain)-16*1024:]
	}
	numbered := nativeNumberedLine.FindAllStringSubmatch(plain, -1)
	// Scrollback can hold earlier menus: read only the latest, which starts at
	// the last "1." entry, and keep the digit the CLI printed for each choice.
	for i := len(numbered) - 1; i >= 0; i-- {
		if numbered[i][1] == "1" {
			numbered = numbered[i:]
			break
		}
	}
	seen := map[string]bool{}
	entries := 0
	for _, match := range numbered {
		if seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		entries++
		if canonical, ok := nativeOptions[strings.ToLower(match[2])]; ok {
			options = append(options, canonical)
			digits = append(digits, match[1])
		}
	}
	// A numbered picker with at least two entries is drivable from the UI only
	// when every choice is a reviewed label; otherwise hand it to the terminal.
	if entries > 1 {
		if len(options) == entries {
			return "menu", "", options, digits
		}
		return "terminal_required", "", nil, nil
	}
	if matches := nativeSelectedLine.FindAllStringSubmatch(plain, -1); len(matches) > 0 {
		label := strings.ToLower(strings.TrimSpace(matches[len(matches)-1][1]))
		if canonical, ok := nativeOptions[label]; ok {
			return "menu", canonical, nil, nil
		}
		return "terminal_required", "", nil, nil
	}
	if nativeInteractiveWords.MatchString(plain) {
		return "terminal_required", "", nil, nil
	}
	return "waiting", "", nil, nil
}

// ActOnLogin sends a reviewed menu choice to a running native login. For a
// numbered picker a digit names the Nth listed option, sent as the digit the
// CLI printed for it; Select sends Enter. The PTY write runs outside s.mu.
func (s *Service) ActOnLogin(ctx context.Context, agentID, id, action string) (LoginSnapshot, error) {
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
	// Numbered pickers accept their listed options and Enter; arrow-key
	// pickers accept only navigation. No free-form bytes ever reach the PTY.
	key := action
	if len(op.snapshot.NativeOptions) > 0 {
		switch {
		case action == "select":
		case len(action) == 1 && action >= "1" && action <= "9" && int(action[0]-'0') <= len(op.nativeDigits):
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
			// Copilot reads the token from stdin into its own credential store.
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

// directKeyWriter returns the reviewed credential-store writer for harnesses
// whose documented key stores AO can write directly, without running the CLI.
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

// saveLoginKey saves the credential of a key method in its store and reports
// isKey=false for a method that runs a native CLI login instead.
func (s *Service) saveLoginKey(ctx context.Context, plan Plan, method, secret, model string, opts LoginOptions) (isKey bool, err error) {
	agentID := plan.AgentID
	if method == catalogKeyMethod {
		return true, s.saveCatalogKey(ctx, plan, opts.Provider, secret)
	}
	if writer, ok := directKeyWriter(agentID); ok && !strings.HasPrefix(method, "env_") {
		return true, writer(ctx, method, secret)
	}
	if name, ok := environmentMethod(agentID, method); ok {
		if agentID == "openhands" {
			model = strings.TrimSpace(model)
			if !providerModelID.MatchString(model) {
				return true, apierr.Invalid("AGENT_AUTH_MODEL_INVALID", "Enter a LiteLLM model ID for OpenHands.", nil)
			}
			if err := providerkeys.Save(ctx, s.dataDir, "LLM_MODEL", model); err != nil {
				return true, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The OpenHands model could not be saved.")
			}
			if err := os.Setenv("LLM_MODEL", model); err != nil {
				return true, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The OpenHands model could not be activated.")
			}
		}
		if agentID == "qwen" {
			if err := saveQwenProvider(ctx, name, model); err != nil {
				return true, err
			}
		} else if agentID == "continue" && name != "CONTINUE_API_KEY" {
			if err := saveContinueProvider(ctx, name, model, secret); err != nil {
				return true, err
			}
		}
		return true, s.storeProviderKey(ctx, name, secret, opts.Device, agentID)
	}
	return false, nil
}

// recordCompletedLogin registers a finished key save as the harness's latest
// operation, so the panel can poll it like any other login.
func (s *Service) recordCompletedLogin(agentID, method string) (LoginSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return LoginSnapshot{}, apierr.Unavailable("AGENT_AUTH_UNAVAILABLE", "Authentication service is stopping.")
	}
	s.pruneOperationsLocked(agentID)
	complete := &loginOperation{snapshot: LoginSnapshot{ID: uuid.NewString(), AgentID: agentID, Method: method, Status: "succeeded", ExpiresAt: time.Now()}, done: make(chan struct{})}
	close(complete.done)
	s.operations[complete.snapshot.ID] = complete
	return complete.snapshot, nil
}

// pruneOperationsLocked keeps only the latest operation per harness. A stale
// tab cannot cancel its replacement, and repeated logins cannot grow the
// registry unbounded. The caller holds s.mu.
func (s *Service) pruneOperationsLocked(agentID string) {
	for id, op := range s.operations {
		if op.snapshot.AgentID == agentID {
			delete(s.operations, id)
		}
	}
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

// Close stops background logins when the daemon shuts down.
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
		// Kimi Code's device flow: auth.kimi.ai (global) or auth.kimi.com.
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
