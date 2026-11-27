package agentauth

import (
	"context"
	"strings"

	agyagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/minimaxcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agentaccount"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// plans is the code-reviewed authentication allowlist in stable Harness
// settings order. Commands must be added here, never supplied by clients.
// qwenAuthInput works with Qwen's default editor and its optional Vim mode.
// In NORMAL, i enters INSERT and Backspace is harmless on the empty prompt; in
// INSERT/default mode, Backspace removes the literal i before /auth is entered.
const qwenAuthInput = "i\x7f/auth\r"

var plans = []Plan{
	plan("mastra-code", ActionLogin, "Log in to Mastra Code", []string{"mastracode", "login"}, "Choose an OAuth account or Add an API key in Mastra's native provider chooser. After adding a key, open Mastra Code in build mode and use /model or /setup in the same AO profile to select a build model.", "https://github.com/mastra-ai/mastra/tree/8a0fd46222d8737e4c369291a3efa59e632aadbd/mastracode"),
	plan("docker-agent", ActionSetup, "Set up Docker Agent", []string{"docker-agent", "setup"}, "Native model setup: connect Anthropic, OpenAI or Google and Docker Agent saves the key in its own env file, which Orchestrator's default configuration uses. To use your own YAML, set AO_DOCKER_AGENT_CONFIG to its absolute path. Authorization is checked by the provider on a live turn.", "https://docs.docker.com/ai/docker-agent/getting-started/quickstart/"),
	loginMenuPlan("claude-code", "claude-login", nil, "Log in to Claude Code", []string{"claude", "auth", "login"}, "Choose Claude subscription, Anthropic Console, SSO, or setup token", "https://code.claude.com/docs/en/installation"),
	loginMenuPlan("codex", "codex-login", []string{"--use-default-credential-store"}, "Log in to Codex", []string{"codex", "login"}, "Choose ChatGPT, device code, API key, or access token", "https://github.com/openai/codex"),
	plan("cursor", ActionLogin, "Log in to Cursor", []string{"cursor-agent", "login"}, "Native browser flow", "https://docs.cursor.com/en/cli/installation"),
	plan("opencode", ActionLogin, "Log in to OpenCode", []string{"opencode", "auth", "login"}, "Native provider chooser", "https://github.com/anomalyco/opencode"),
	plan("opencode-v2", ActionLogin, "Log in to OpenCode 2", []string{"opencode", "auth", "login"}, "Native provider chooser", "https://opencode.ai/v2/docs"),
	plan("mimo-code", ActionLogin, "Log in to MiMo Code", []string{"mimo", "auth", "login"}, "Native provider chooser", "https://mimo.mi.com/docs/en-US/tokenplan/integration/mimo-code"),
	// MiniMax's TUI /login offers both China and Global accounts; the bare
	// `mcode login` subcommand defaults to the China region.
	terminalInputPlan("minimax-code", ActionLogin, "Log in to MiniMax Code", []string{"mcode"}, "/login\r", "Select Open login after MiniMax Code finishes starting", "https://agent.minimax.io/docs/cli/quick-start"),
	documentationPlan("memcode", "Set up memcode", "Configure a native endpoint and provider credentials before launching Orchestrator", "https://github.com/memcode-ai/memcode"),
	documentationPlan("aider", "Set up Aider", "Configure provider credentials using Aider's documented environment or configuration-file options", "https://aider.chat/docs/config/api-keys.html"),
	copilotLoginPlan(),
	plan("grok", ActionLogin, "Log in to Grok", []string{"grok", "login"}, "Native login; device-auth remains available inside the CLI", "https://docs.x.ai/build/overview"),
	kimiLoginPlan(),
	piLoginPlan(),
	tauLoginPlan(),
	plan("amp", ActionLogin, "Log in to Amp", []string{"amp", "login"}, "Native browser flow", "https://ampcode.com/manual"),
	plan("auggie", ActionLogin, "Log in to Auggie", []string{"auggie", "login"}, "Native browser flow", "https://docs.augmentcode.com/cli/overview"),
	terminalInputPlan("droid", ActionLogin, "Log in to Droid", []string{"droid"}, "/login\r", "Select Open login after Droid finishes starting", "https://docs.factory.ai/droid-cli/cli-reference"),
	plan("crush", ActionLogin, "Log in to Crush", []string{"crush", "login"}, "Native Charm Hyper login flow; GitHub Copilot remains available as a platform option", "https://github.com/charmbracelet/crush"),
	plan("cline", ActionLogin, "Log in to Cline", []string{"cline", "auth"}, "Native authentication flow", "https://github.com/cline/cline"),
	plan("goose", ActionSetup, "Set up Goose", []string{"goose", "configure"}, "Native provider configuration; Orchestrator forwards terminal input without persisting or logging the raw input, while Goose controls credential storage", "https://block.github.io/goose/index.html"),
	geminiSetupPlan(),
	terminalInputPlan("qwen", ActionSetup, "Set up Qwen", []string{"qwen"}, qwenAuthInput, "Select Open setup after Qwen finishes starting to configure a model provider", "https://qwenlm.github.io/qwen-code-docs/en/users/configuration/auth/"),
	plan("continue", ActionLogin, "Log in to Continue", []string{"cn", "login"}, "Native browser flow", "https://docs.continue.dev/cli/quickstart"),
	plan("devin", ActionLogin, "Log in to Devin", []string{"devin", "auth", "login"}, "Native browser flow; manual-token flow remains available from the CLI", "https://docs.devin.ai/get-started/devin-intro"),
	plan("kiro", ActionLogin, "Log in to Kiro", []string{"kiro-cli", "login"}, "Native browser flow; device flow remains a CLI option", "https://kiro.dev/docs/getting-started/installation/"),
	plan("kilocode", ActionLogin, "Log in to Kilo Code", []string{"kilo", "auth", "login"}, "Native browser flow", "https://kilo.ai/docs/code-with-ai/platforms/cli"),
	plan("vibe", ActionSetup, "Set up Vibe", []string{"vibe", "--setup"}, "Native provider setup; Orchestrator forwards terminal input without persisting or logging the raw input, while Vibe controls credential storage", "https://github.com/mistralai/mistral-vibe"),
	plan("muse", ActionLogin, "Log in to Muse", []string{"muse", "login"}, "Native login flow", "https://ai.meta.com/llama/"),
	agyLoginPlan(),
	plan("autohand", ActionLogin, "Log in to Autohand", []string{"autohand", "login"}, "Native Autohand account sign-in", "https://docs.autohand.ai/working-with-autohand-code/cli-reference"),
	kimchiLoginPlan(),
	terminalInputPlan("prime-agent", ActionLogin, "Log in to Prime Agent", []string{"prime-agent"}, "/login\r", "Select Open login after Prime Agent finishes starting", "https://github.com/PrimeIntellect-ai/prime-agent/blob/main/packages/coding-agent/docs/quickstart.md"),
	ompLoginPlan(),
	plan("codewhale", ActionSetup, "Set up Codewhale", []string{"codewhale", "auth", "set", "--provider", "deepseek"}, "Save a provider key in Codewhale's native credential store", "https://codewhale.net/en/docs/auth"),
	plan("fx", ActionLogin, "Log in to fx", []string{"fx", "login"}, "Select Vercel, Codex, or Grok in fx's native login flow", "https://fx.sh/docs"),
	documentationPlan("unreal-agent", "View Unreal Agent documentation", "Configure an OpenAI, OpenRouter, Fireworks, Codex, or Ollama provider for Orchestrator's built-in Unreal Agent", "https://github.com/Untrivial-ai/agent-orchestrator/blob/main/docs/harnesses/unreal-agent.md"),
	// DeepSeek Harness has no login subcommand — credentials are records in its
	// own store (~/.dsh/.credentials.yaml) — but the web profile serves the
	// Models page that writes them, which is the route a failed run names
	// ("store DEEPSEEK_API_KEY through the credentials service (the web Models
	// page writes it)"). The profile prints a tokenised URL and opens it, so
	// setup lands on the page that does the work rather than on a docs link.
	plan("deepseek-harness", ActionSetup, "Set up DeepSeek", []string{"dsh", "--profile", "web"}, "Opens DeepSeek's Models page to store an API key and pick a model route; leave it running until the key is saved", "https://github.com/deepseek-ai/deepseek-harness"),
	plan("openhands", ActionSetup, "Set up OpenHands", []string{"openhands"}, "Native first-run LLM settings; Orchestrator forwards terminal input without persisting or logging the raw input, while OpenHands stores settings in ~/.openhands", "https://docs.openhands.dev/openhands/usage/cli/quick-start"),
	plan("command-code", ActionLogin, "Log in to Command Code", []string{"command-code", "login"}, "Native browser flow; an API key can be pasted in the terminal", "https://commandcode.ai/docs/quickstart"),
	plan("hermes", ActionSetup, "Set up Hermes Agent", []string{"hermes", "model"}, "Native provider setup: choose a provider, then paste an API key or finish its OAuth flow", "https://hermes-agent.nousresearch.com/docs"),
	plan("stakpak", ActionLogin, "Log in to Stakpak", []string{"stakpak", "auth", "login"}, "Native provider login: create a Stakpak API key in the browser, or paste an Anthropic, OpenAI or Gemini key", "https://github.com/stakpak/agent#authentication"),
	// Native login covers the built-in OpenAI provider. The plan also lists
	// Open Interpreter in the provider-key catalog, which offers the keys its
	// generated Anthropic, Gemini, OpenRouter and DeepSeek providers read.
	plan("open-interpreter", ActionLogin, "Log in to Open Interpreter", []string{"interpreter", "login"}, "Native sign-in for the built-in OpenAI provider; other providers use a saved API key", "https://www.openinterpreter.com/docs/terminal/install"),
}

func terminalInputPlan(agentID string, action Action, title string, command []string, terminalInput, guidance, docs string) Plan {
	p := plan(agentID, action, title, command, guidance, docs)
	p.terminalInput = terminalInput
	return p
}

// kimchiLoginPlan starts Kimchi's TUI, which opens the authentication picker
// on first launch. If the picker is not already open, /login is sent once the
// TUI is ready.
func kimchiLoginPlan() Plan {
	p := plan("kimchi", ActionLogin, "Log in to Kimchi", []string{"kimchi"}, "Kimchi opens the authentication method picker in the PTY", "https://docs.kimchi.dev/docs/service-keys")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{
		{Text: "Select authentication method:", SkipInput: true},
		{Text: "/ for commands"},
	}
	return p
}

// kimiLoginPlan opens Kimi's TUI and injects /login so the native platform
// picker (Kimi Code browser login and Kimi Platform API keys) is offered;
// the bare `kimi login` subcommand only runs the device-code flow. The input
// is sent automatically once Kimi renders its unauthenticated ready message,
// without relying on a fixed startup delay. initialInput carries no trailing
// Enter because terminal delivery
// (SendMessage) presses it. Kimi's first-run "Trust this folder?" dialog would
// swallow that input in AO's private auth workspace, so the workspace trust
// record is seeded first.
func kimiLoginPlan() Plan {
	p := plan("kimi", ActionLogin, "Log in to Kimi", []string{"kimi"}, "Kimi opens its login picker automatically", "https://moonshotai.github.io/kimi-code/en/")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{{Text: "Run /login or /provider to get started."}}
	p.prepareWorkspace = kimi.EnsureWorkspaceTrusted
	return p
}

// guidedStep is one reviewed move through a native CLI's login screens: once
// readyText is on screen, the step types its command (a fixed slash command,
// then Enter) and presses its keys in order. Keys are limited to the auth
// terminal's reviewed actions ("previous", "next", "select") and commands to
// plain slash commands written in the plan, so no free-form or UI-supplied
// input ever reaches the CLI. An optional step is skipped when the next
// step's screen shows without it. After a restart step the CLI is closed and
// started afresh once a fresh auth check no longer finds the old sign-in.
type guidedStep struct {
	readyText string
	command   string
	keys      []string
	optional  bool
	restart   bool
}

// loginURLRule allows a sign-in link the CLI prints in its hidden terminal to
// be shown in the panel: https on exactly this host, under this path prefix,
// carrying every required query parameter. The required parameters keep a
// link cut short by a missed line wrap out of the panel.
type loginURLRule struct {
	host          string
	pathPrefix    string
	requiredQuery []string
}

// agyLoginPlan drives Agy's own login menu from the background UI. Agy has no
// login subcommand: a signed-out `agy` shows "Select login method" (Google
// OAuth, or a Google Cloud project, which then asks for its Google Cloud
// sign-in method), opens the browser itself and prints the link. The menu is
// arrow-key only, and Agy stays open in its chat view after signing in, so the
// sign-in finishes when Agy's auth probe passes rather than when it exits.
// Only macOS and Windows have a sign-in probe (Agy's Keychain or Credential
// Manager entry); elsewhere Agy keeps the plain native sign-in, which finishes
// when the user leaves Agy.
func agyLoginPlan() Plan {
	p := plan("agy", ActionLogin, "Log in to Agy", []string{"agy"}, "Choose Google account or Google Cloud project", "https://github.com/google-antigravity/antigravity-cli")
	p.guidedMethods = map[string][]guidedStep{
		agyGoogleMethod: {{readyText: "Select login method", keys: []string{"select"}}},
		agyCloudMethod: {
			{readyText: "Select login method", keys: []string{"next", "select"}},
			{readyText: "Continue with Google Cloud", keys: []string{"select"}},
		},
	}
	p.signInKeepsRunning = true
	// Agy prints state last, so a link missing it was cut short.
	p.loginURLRules = []loginURLRule{{host: "accounts.google.com", pathPrefix: "/o/oauth2/", requiredQuery: []string{"client_id", "redirect_uri", "response_type", "state"}}}
	p.guidedPlatforms = []string{"darwin", "windows"}
	// Signed in, Agy skips its login menu: it signs in silently, asks whether
	// to trust the (fresh, empty) working folder, then opens its chat. It has
	// /logout but no /login, and after /logout it flashes its login menu and
	// returns to the chat signed out. So a login over an existing sign-in
	// replaces it: trust the folder, sign out, start a fresh agy (which shows
	// the menu and waits), then walk the menu as usual.
	p.signedInScreens = []string{agyTrustPrompt, agyChatPrompt}
	// Google's callback page shows an authorization code to paste back into
	// Agy, which waits for it under the sign-in link.
	p.codePrompt = "paste the authorization code below"
	p.codeRejected = "Got an error:"
	p.signOutSteps = []guidedStep{
		{readyText: agyTrustPrompt, keys: []string{"select"}, optional: true},
		{readyText: agyChatPrompt, command: "/logout", restart: true},
	}
	// An Antigravity account is an agy folder whose launch environment names
	// it (GEMINI_HOME) and keeps its sign-in in a file there. agy reads the
	// folder from its flag only, and the account's sign-in lands in that file.
	p.accountLogin = func(env map[string]string) ([]string, func(context.Context) bool, bool) {
		home := strings.TrimSpace(env["GEMINI_HOME"])
		if home == "" {
			return nil, nil, false
		}
		signedIn := func(ctx context.Context) bool {
			identity, err := (&agentaccount.Antigravity{}).Identity(ctx, home)
			return err == nil && identity.SignedIn
		}
		return []string{agyagent.GeminiDirFlag(home)}, signedIn, true
	}
	return p
}

const (
	agyTrustPrompt  = "Do you trust the contents of this project?"
	agyChatPrompt   = "? for shortcuts"
	agyGoogleMethod = "browser"
	agyCloudMethod  = "cloud_project"
)

// piLoginPlan opens Pi's TUI and injects /login automatically; Pi has no
// login subcommand. The primary ready marker is the footer's context stat: a
// fresh Pi session always renders "0.0%/<window>" there, with or without a
// selected model and even with quietStartup, which drops the onboarding line
// kept as a second marker. Pi keeps text typed during its brief startup tail
// in the editor, and the runtime's pause before Enter covers that window. If a
// future Pi renders neither marker, /login is still sent when the ready wait
// expires: by then Pi is at its prompt, where /login is always valid.
func piLoginPlan() Plan {
	p := plan("pi", ActionLogin, "Log in to Pi", []string{"pi"}, "Pi opens its login picker automatically", "https://github.com/earendil-works/pi")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{
		{Text: "0.0%/"},
		{Text: "Pi can explain its own features"},
	}
	p.sendInitialInputOnReadyTimeout = true
	return p
}

// tauLoginPlan opens Tau's TUI and injects /login, which opens Tau's native
// login-method picker (subscription OAuth, provider API key, or a custom
// provider); Tau has no login subcommand. The input is sent once the composer
// placeholder renders, the same marker Tau's adapter waits on before typing a
// task, or after the bounded ready wait if a future Tau renames it.
func tauLoginPlan() Plan {
	p := plan("tau", ActionLogin, "Log in to Tau", []string{"tau"}, "Tau opens its login picker automatically", "https://twotimespi.dev/guides/providers-and-models/")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{{Text: "Ask Tau…"}}
	p.sendInitialInputOnReadyTimeout = true
	return p
}

// copilotLoginPlan opens Copilot's TUI and injects /login so its native
// account picker (GitHub.com, GitHub Enterprise Cloud, or Microsoft Entra) is
// offered; the bare `copilot login` subcommand jumps straight into the
// GitHub.com browser flow. The input is sent once the composer footer renders,
// or after the bounded ready wait when slow hooks delay that footer.
// Copilot's "Confirm folder trust" dialog would swallow that input in AO's
// private auth workspace, so the folder is recorded as trusted first.
func copilotLoginPlan() Plan {
	p := plan("copilot", ActionLogin, "Log in to GitHub Copilot", []string{"copilot"}, "Copilot opens its account picker automatically", "https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli")
	p.initialInput = "/login"
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{{Text: "/ commands"}}
	p.sendInitialInputOnReadyTimeout = true
	p.prepareWorkspace = copilot.EnsureWorkspaceTrusted
	return p
}

// geminiSetupPlan waits for Gemini's editor to render, types /auth, then waits
// for the command completion to prove the slash-command registry is loaded
// before pressing Enter. Gemini's --prompt-interactive flag (and submitting
// too early during startup) treats /auth as a model prompt instead.
func geminiSetupPlan() Plan {
	p := plan("gemini", ActionSetup, "Set up Gemini CLI", []string{"gemini"}, "Gemini opens its authentication picker automatically; if /auth remains in the editor, press Enter once command completion appears", "https://geminicli.com/docs/get-started/authentication/")
	p.initialInputReadyStates = []shellterm.InitialInputReadyState{
		{Text: "How would you like to authenticate for this project?", SkipInput: true},
		{
			Text:            "Type your message or @path/to/file",
			RawPrefix:       "/auth",
			SubmitReadyText: "Manage authentication",
		},
	}
	return p
}

// ompLoginPlan uses OMP's native onboarding command, whose first scene is
// provider sign-in even when setup is already complete. OMP paints its prompt
// before opening first-run setup, so prompt markers cannot safely gate /login
// injection. Let OMP own the picker without sending any terminal input.
func ompLoginPlan() Plan {
	return plan("omp", ActionLogin, "Log in to OMP", []string{"omp", "setup"}, "Sign in to a provider in OMP's setup wizard, then finish or skip the remaining setup steps", "https://github.com/can1357/oh-my-pi")
}

func documentationPlan(agentID, title, guidance, docs string) Plan {
	p := plan(agentID, ActionSetup, title, nil, guidance, docs)
	p.LaunchMode = LaunchDocumentation
	return p
}

func loginMenuPlan(agentID, launcher string, launcherArgs []string, title string, command []string, guidance, docs string) Plan {
	p := plan(agentID, ActionLogin, title, command, guidance, docs)
	p.launcher = launcher
	p.launcherArgs = launcherArgs
	return p
}

var planByAgentID = func() map[string]Plan {
	out := make(map[string]Plan, len(plans))
	for _, plan := range plans {
		out[plan.AgentID] = plan
	}
	return out
}()

func plan(agentID string, action Action, title string, command []string, guidance, docs string) Plan {
	logout := logoutCommand(agentID)
	return Plan{
		LogoutCommand:    strings.Join(logout, " "),
		logoutCommand:    logout,
		AgentID:          agentID,
		Action:           action,
		LaunchMode:       LaunchTerminal,
		DisplayCommand:   strings.Join(command, " "),
		Guidance:         guidance,
		DocumentationURL: docs,
		command:          command,
		title:            title,
	}
}

// nativeLogoutCommand completes a logout argv whose account scope lives in the
// harness's own state when the logout runs.
func nativeLogoutCommand(agentID string, command []string) []string {
	if agentID == "minimax-code" {
		return append(append([]string(nil), command...), minimaxcode.LogoutArgs()...)
	}
	return command
}

// Only documented CLI subcommands belong here. An absent entry explicitly
// means unsupported; do not infer logout from login or delete credential files.
func logoutCommand(agentID string) []string {
	switch agentID {
	case "claude-code":
		return []string{"claude", "auth", "logout"}
	case "codex":
		return []string{"codex", "logout"}
	case "cursor":
		return []string{"cursor-agent", "logout"}
	case "opencode", "opencode-v2":
		return []string{"opencode", "auth", "logout"}
	case "amp":
		return []string{"amp", "logout"}
	case "auggie":
		return []string{"auggie", "logout"}
	case "devin":
		return []string{"devin", "auth", "logout"}
	case "minimax-code":
		return []string{"mcode", "logout"}
	case "open-interpreter":
		return []string{"interpreter", "logout"}
	default:
		return nil
	}
}
