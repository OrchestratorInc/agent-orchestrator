package systeminstall

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var agentDocumentationURLs = map[Target]string{
	TargetMemcode:     "https://github.com/memcode-ai/memcode/releases",
	TargetClaudeCode:  "https://code.claude.com/docs/en/installation",
	TargetCodex:       "https://github.com/openai/codex",
	TargetCursor:      "https://docs.cursor.com/en/cli/installation",
	TargetOpencode:    "https://github.com/anomalyco/opencode",
	TargetOpencodeV2:  "https://opencode.ai/v2/docs",
	TargetAider:       "https://aider.chat/docs/install.html",
	TargetCopilot:     "https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli",
	TargetGrok:        "https://docs.x.ai/build/overview",
	TargetKimi:        "https://moonshotai.github.io/kimi-code/en/",
	TargetPi:          "https://github.com/earendil-works/pi",
	TargetAmp:         "https://ampcode.com/manual",
	TargetAuggie:      "https://docs.augmentcode.com/cli/overview",
	TargetDroid:       "https://docs.factory.ai/droid-cli/cli-reference",
	TargetCrush:       "https://github.com/charmbracelet/crush",
	TargetCline:       "https://github.com/cline/cline",
	TargetGoose:       "https://goose-docs.ai/docs/getting-started/installation/",
	TargetQwen:        "https://qwenlm.github.io/qwen-code-docs/en/users/quickstart/",
	TargetGemini:      "https://geminicli.com/docs/get-started/installation/",
	TargetContinue:    "https://docs.continue.dev/cli/quickstart",
	TargetDevin:       "https://docs.devin.ai/get-started/devin-intro",
	TargetKiro:        "https://kiro.dev/docs/getting-started/installation/",
	TargetKilocode:    "https://kilo.ai/docs/code-with-ai/platforms/cli",
	TargetVibe:        "https://github.com/mistralai/mistral-vibe",
	TargetMuse:        "https://ai.meta.com/llama/",
	TargetAgy:         "https://github.com/google-antigravity/antigravity-cli",
	TargetAutohand:    "https://docs.autohand.ai/working-with-autohand-code/cli",
	TargetKimchi:      "https://docs.kimchi.dev/docs/coding-getting-started",
	TargetPrimeAgent:  "https://github.com/PrimeIntellect-ai/prime-agent/blob/main/packages/coding-agent/docs/quickstart.md",
	TargetOMP:         "https://github.com/can1357/oh-my-pi",
	TargetCodewhale:   "https://github.com/Hmbown/Codewhale",
	TargetFX:          "https://fx.sh/docs",
	TargetUnreal:      "https://github.com/unreallabsai/unreal-agent",
	TargetMiMoCode:    "https://github.com/XiaomiMiMo/MiMo-Code",
	TargetDeepSeek:    "https://github.com/deepseek-ai/deepseek-harness",
	TargetOpenHands:   "https://docs.openhands.dev/openhands/usage/cli/installation",
	TargetCommandCode: "https://commandcode.ai/docs/quickstart",
}

func (s requestPlanner) agentMethodPlans(target Target, operation AgentOperation) []Plan {
	var plans []Plan
	switch target {
	case TargetClaudeCode:
		official := s.officialByOS(target, "https://claude.ai/install.sh", "bash", "https://claude.ai/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrewCask(target, "claude-code"), s.planNPM(target, "@anthropic-ai/claude-code"), official}
		} else {
			plans = []Plan{s.planNPM(target, "@anthropic-ai/claude-code"), official}
		}
	case TargetCodex:
		official := s.officialByOS(target, "https://chatgpt.com/codex/install.sh", "sh", "https://chatgpt.com/codex/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrewCask(target, "codex"), s.planNPM(target, "@openai/codex"), official}
		} else {
			plans = []Plan{s.planNPM(target, "@openai/codex"), official}
		}
	case TargetOpencode:
		switch s.goos {
		case "windows":
			plans = []Plan{s.planWinget(target, "SST.opencode")}
		case "darwin":
			plans = []Plan{s.planBrew(target, "anomalyco/tap/opencode"), s.planNPM(target, "opencode-ai@latest"), s.planShellInstaller(target, "https://opencode.ai/install", "bash")}
		case "linux":
			plans = []Plan{s.planNPM(target, "opencode-ai@latest"), s.planShellInstaller(target, "https://opencode.ai/install", "bash")}
		default:
			plans = []Plan{s.planNPM(target, "opencode-ai@latest")}
		}
	case TargetOpencodeV2:
		switch s.goos {
		case "darwin":
			plans = []Plan{s.planBrew(target, "anomalyco/tap/opencode-v2"), s.planNPM(target, "@opencode/cli"), s.planShellInstaller(target, "https://opencode.ai/v2/install", "bash")}
		case "linux":
			plans = []Plan{s.planNPM(target, "@opencode/cli"), s.planShellInstaller(target, "https://opencode.ai/v2/install", "bash")}
		default:
			plans = []Plan{s.planNPM(target, "@opencode/cli")}
		}
	case TargetCopilot:
		switch s.goos {
		case "windows":
			plans = []Plan{s.planWinget(target, "GitHub.Copilot"), s.planNPM(target, "@github/copilot")}
		case "darwin":
			plans = []Plan{s.planBrewCask(target, "copilot-cli"), s.planNPM(target, "@github/copilot")}
		default:
			plans = []Plan{s.planNPM(target, "@github/copilot")}
		}
	case TargetCursor:
		plans = []Plan{s.officialByOS(target, "https://cursor.com/install", "bash", "https://cursor.com/install?win32=true", agentDocumentationURLs[target])}
	// Aider's installer runs `uv tool install aider-chat`, so uv owns the binary
	// it leaves behind; listing uv lets updates go through it.
	case TargetAider:
		plans = []Plan{s.officialByOS(target, "https://aider.chat/install.sh", "sh", "https://aider.chat/install.ps1", agentDocumentationURLs[target]), s.planUV(target, "aider-chat")}
	case TargetGrok:
		plans = []Plan{s.officialByOS(target, "https://x.ai/cli/install.sh", "bash", "https://x.ai/cli/install.ps1", agentDocumentationURLs[target])}
	case TargetKimi:
		official := s.officialByOS(target, "https://code.kimi.com/kimi-code/install.sh", "bash", "https://code.kimi.com/kimi-code/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			// Homebrew core ships Kimi as the kimi-code formula; listing it lets AO
			// recognise and update brew-installed copies.
			plans = []Plan{official, s.planBrew(target, "kimi-code")}
		} else {
			plans = []Plan{official}
		}
	case TargetPi:
		plans = []Plan{s.planNPM(target, "@earendil-works/pi-coding-agent")}
		if s.goos == "darwin" || s.goos == "linux" {
			plans = append(plans, s.planPiOfficialInstaller())
		}
	case TargetAmp:
		official := s.officialByOS(target, "https://ampcode.com/install.sh", "bash", "https://ampcode.com/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrew(target, "ampcode/tap/ampcode"), s.planNPM(target, "@ampcode/cli"), official}
		} else {
			plans = []Plan{s.planNPM(target, "@ampcode/cli"), official}
		}
	case TargetDroid:
		official := s.officialByOS(target, "https://app.factory.ai/cli", "sh", "https://app.factory.ai/cli/windows", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrewCask(target, "droid"), s.planNPM(target, "droid"), official}
		} else {
			plans = []Plan{s.planNPM(target, "droid"), official}
		}
	case TargetAuggie:
		plans = []Plan{s.planNPM(target, "@augmentcode/auggie")}
	case TargetCrush:
		if s.goos == "darwin" {
			plans = []Plan{s.planBrew(target, "charmbracelet/tap/crush"), s.planNPM(target, "@charmland/crush")}
		} else {
			plans = []Plan{s.planNPM(target, "@charmland/crush")}
		}
	case TargetCline:
		plans = []Plan{s.planNPM(target, "cline@latest")}
	case TargetGoose:
		switch s.goos {
		case "windows":
			plan := s.officialByOS(
				target,
				"https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh", "bash",
				"https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1",
				agentDocumentationURLs[target],
			)
			if plan.Script != nil {
				plan.Script.Env = []string{"CONFIGURE=false"}
			}
			plans = []Plan{plan}
		case "darwin", "linux":
			plans = []Plan{s.planShellInstaller(target, "https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh", "bash")}
		default:
			plans = []Plan{manualPlan(target, "Goose publishes this installer for macOS and Linux only.", agentDocumentationURLs[target])}
		}
	case TargetGemini:
		plans = []Plan{s.planNPM(target, "@google/gemini-cli@latest")}
	case TargetQwen:
		official := s.officialByOS(target, "https://qwen-code-assets.oss-cn-hangzhou.aliyuncs.com/installation/install-qwen-standalone.sh", "bash", "https://qwen-code-assets.oss-cn-hangzhou.aliyuncs.com/installation/install-qwen-standalone.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrew(target, "qwen-code"), s.planNPM(target, "@qwen-code/qwen-code@latest"), official}
		} else {
			plans = []Plan{s.planNPM(target, "@qwen-code/qwen-code@latest"), official}
		}
	case TargetContinue:
		plans = []Plan{s.planNPM(target, "@continuedev/cli")}
	case TargetDevin:
		switch s.goos {
		case "windows":
			plans = []Plan{manualPlan(target, "Devin for Terminal documents installation through WSL on Windows.", agentDocumentationURLs[target])}
		case "darwin", "linux":
			plans = []Plan{s.planShellInstaller(target, "https://cli.devin.ai/install.sh", "bash")}
		default:
			plans = []Plan{manualPlan(target, "Devin for Terminal publishes this installer for macOS and Linux only.", agentDocumentationURLs[target])}
		}
	case TargetAutohand:
		official := s.officialByOS(target, "https://autohand.ai/install.sh", "sh", "https://autohand.ai/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrew(target, "autohandai/code/autohand-code"), s.planNPM(target, "autohand-cli"), official}
		} else {
			plans = []Plan{s.planNPM(target, "autohand-cli"), official}
		}
	case TargetKimchi:
		official := s.officialByOS(target,
			"https://github.com/getkimchi/kimchi/releases/latest/download/install.sh", "sh",
			"https://github.com/getkimchi/kimchi/releases/latest/download/install.ps1",
			agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrew(target, "getkimchi/tap/kimchi"), official}
		} else {
			plans = []Plan{official}
		}
	case TargetVibe:
		plans = []Plan{s.planUV(target, "mistral-vibe"), s.planPipx(target, "mistral-vibe")}
	case TargetKiro:
		plans = []Plan{s.planKiroInstall()}
	case TargetKilocode:
		plans = []Plan{s.planNPM(target, "@kilocode/cli")}
	case TargetMuse:
		switch s.goos {
		case "windows":
			plans = []Plan{manualPlan(target, "Muse Code does not currently publish a native Windows installer.", agentDocumentationURLs[target])}
		case "darwin", "linux":
			plans = []Plan{s.planShellInstaller(target, "https://dev.meta.ai/install.sh", "bash")}
		default:
			plans = []Plan{manualPlan(target, "Muse Code publishes this installer for macOS and Linux only.", agentDocumentationURLs[target])}
		}
	case TargetAgy:
		plans = []Plan{s.officialByOS(target, "https://antigravity.google/cli/install.sh", "bash", "https://antigravity.google/cli/install.ps1", agentDocumentationURLs[target])}
	case TargetPrimeAgent:
		switch s.goos {
		case "windows":
			plans = []Plan{manualPlan(target, "Prime Agent currently documents macOS and Linux; use WSL on Windows.", agentDocumentationURLs[target])}
		case "darwin", "linux":
			plans = []Plan{s.planShellInstaller(target, "https://app.primeintellect.ai/prime-agent/install.sh", "sh")}
		default:
			plans = []Plan{manualPlan(target, "Prime Agent publishes this installer for macOS and Linux only.", agentDocumentationURLs[target])}
		}
	case TargetFX:
		switch s.goos {
		case "darwin", "linux":
			plan := s.planShellInstaller(target, "https://fx.sh/setup.sh", "bash")
			plan.ExpectedDestination = "~/.local/bin/fx"
			plans = []Plan{plan}
		case "windows":
			plans = []Plan{manualPlan(target, "fx publishes macOS and Linux installers; use WSL on Windows.", agentDocumentationURLs[target])}
		default:
			plans = []Plan{manualPlan(target, "fx publishes this installer for macOS and Linux only.", agentDocumentationURLs[target])}
		}
	case TargetOMP:
		official := s.officialByOS(target, "https://omp.sh/install", "sh", "https://omp.sh/install.ps1", agentDocumentationURLs[target])
		if s.goos == "darwin" {
			plans = []Plan{s.planBrew(target, "can1357/tap/omp"), s.planBun(target), official}
		} else {
			plans = []Plan{s.planBun(target), official}
		}
	case TargetCodewhale:
		plans = []Plan{manualPlan(
			target,
			"AO does not automatically install Codewhale. Follow the upstream installation instructions, then refresh harness status.",
			agentDocumentationURLs[target],
		)}
	case TargetUnreal:
		plans = []Plan{{
			Target: target, Unsupported: true, Method: "manual",
			Reason: "Unreal Agent is built into AO; update AO to update the harness.",
		}}
	case TargetMiMoCode:
		plans = []Plan{s.planNPM(target, "@mimo-ai/cli")}
	case TargetDeepSeek:
		// DeepSeek Harness ships as one Node CLI that boots every profile
		// (headless, ACP, web) from the same install, so npm is the only method.
		plans = []Plan{s.planNPM(target, "@deepseek-ai/dsh")}
	case TargetOpenHands:
		// The package pins Requires-Python ==3.12.*, which uv resolves (and
		// downloads if needed) on its own; pipx would need a 3.12 interpreter
		// already on PATH, so it is not offered.
		plans = []Plan{s.planUV(target, "openhands")}
		if s.goos == "darwin" || s.goos == "linux" {
			plans = append(plans, s.planShellInstaller(target, "https://install.openhands.dev/install.sh", "sh"))
		}
	case TargetMemcode:
		if s.goos == "darwin" || s.goos == "linux" {
			plans = []Plan{s.planShellInstaller(target, "https://memcode.ai/install.sh", "sh")}
		} else {
			plans = []Plan{{Target: target, Method: "manual", Unsupported: true, Reason: "Install the official native memcode release binary", DocsURL: agentDocumentationURLs[target]}}
		}
	case TargetCommandCode:
		plans = []Plan{s.planNPM(target, "command-code")}
	default:
		plans = []Plan{{Target: target, Unsupported: true, Method: "manual", Reason: "unknown install target"}}
	}
	if owner, ok := s.derivedOwnerFor(target); ok {
		if derived, ok := s.derivedOwnerPlan(target, owner); ok {
			plans = withDerivedOwner(plans, derived)
		}
	}
	for index := range plans {
		plans[index].DocsURL = agentDocumentationURLs[target]
		if target != TargetOpencodeV2 || plans[index].Method != "npm" {
			plans[index].Notice = installNotice(target)
		}
		plans[index] = s.planForOperation(plans[index], operation)
	}
	return plans
}

func (s *Service) resolveAgentMethod(target Target, method string) (Plan, error) {
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{}, err
	}
	return planner.resolveAgentMethod(target, method, AgentOperationInstall)
}

func (s requestPlanner) resolveAgentMethod(target Target, method string, operation AgentOperation) (Plan, error) {
	for _, plan := range s.agentMethodPlans(target, operation) {
		if plan.Method != method {
			continue
		}
		if plan.Unsupported {
			return Plan{}, fmt.Errorf("%w: install method %q is not available: %s", ErrInstallMethod, method, plan.Reason)
		}
		return plan, nil
	}
	return Plan{}, fmt.Errorf("%w: unknown install method %q for %s", ErrInstallMethod, method, target)
}

func (s requestPlanner) planPiOfficialInstaller() Plan {
	plan := s.planShellInstaller(TargetPi, "https://pi.dev/install.sh", "sh")
	if plan.Unsupported {
		return plan
	}
	if s.capabilities == nil {
		plan.Unsupported = true
		plan.Reason = "Node.js 22.19+ and npm could not be inspected; Pi's installer cannot repair them without a terminal."
		return plan
	}
	nodeVersion, nodeOK := parseToolVersion(s.capabilities.NPM.NodeVersion)
	if !nodeOK || !versionAtLeast(nodeVersion, minimumNodeVersionForTarget(TargetPi)) {
		plan.Unsupported = true
		plan.Reason = "Node.js 22.19+ is required before Pi's installer can run headlessly."
		return plan
	}
	if _, npmOK := parseToolVersion(s.capabilities.NPM.NPMVersion); !npmOK {
		plan.Unsupported = true
		plan.Reason = "npm is required before Pi's installer can run headlessly."
	}
	return plan
}

func (s requestPlanner) planKiroInstall() Plan {
	if s.goos == "darwin" {
		return manualPlan(
			TargetKiro,
			"Kiro's official macOS installer writes an app bundle to /Applications and must be run interactively. Follow Kiro's installation guide, then refresh harness status.",
			agentDocumentationURLs[TargetKiro],
		)
	}
	return s.officialByOS(TargetKiro,
		"https://cli.kiro.dev/install", "bash",
		"https://cli.kiro.dev/install.ps1", agentDocumentationURLs[TargetKiro])
}

func (s requestPlanner) planForOperation(plan Plan, operation AgentOperation) Plan {
	if operation == AgentOperationUpdate && plan.Method == "npm" && (plan.daemonRuntimeRejected || plan.daemonPrefixRejected) {
		// The installed copy may live under another prefix (nvm, fnm, ~/.local)
		// with its own Node. The update validates that installation once the
		// owner is known, and applies this reason again if it uses the daemon's.
		plan.deferredReason = plan.Reason
		plan.Unsupported, plan.Reason = false, ""
	}
	if operation == AgentOperationInstall || plan.Unsupported {
		return plan
	}
	installScript := plan.Script
	plan.Script = nil
	if operation == AgentOperationReinstall {
		if record, ok := s.readManagedRecord(plan.Target); ok && record.Method == plan.Method && s.managedEligible(plan) {
			// Sessions run AO's managed copy, so a reinstall repairs that one:
			// the same release, installed fresh beside it.
			plan.managed = true
			plan.managedPin = record.Version
			argv, _ := managedInstallCommand(plan.Method, record.Package, record.Version, filepath.Join(s.managedTargetDir(plan.Target), "versions", "<next>"))
			plan.Command, plan.Prepare = argv, nil
			return plan
		}
		switch plan.Method {
		case "homebrew":
		case "npm":
			plan.Command = append(plan.Command, "--force")
		case "winget":
			plan.Command = append(plan.Command, "--force")
		case "uv":
			plan.Command = []string{"uv", "tool", "install", plan.Package, "--force", "--reinstall"}
		case "pipx":
			plan.Command = []string{"pipx", "install", "--force", plan.Package}
		case "bun":
			plan.Command = append(plan.Command, "--force")
		case "official-installer":
			plan.Unsupported = true
			plan.Command = nil
			plan.Script = nil
			plan.Reason = "This vendor installer does not provide a verified headless reinstall operation."
		default:
			plan.Unsupported = true
			plan.Reason = "This installation method does not provide an explicit reinstall operation."
		}
		return plan
	}

	if operation == AgentOperationUpdate {
		switch plan.Method {
		case "homebrew":
			// The runner sets HOMEBREW_NO_AUTO_UPDATE, and the advisory reads the
			// live Homebrew API, so refresh local metadata before upgrading.
			plan.Prepare = [][]string{{"brew", "update"}}
			plan.Command = []string{"brew", "upgrade"}
			if plan.PackageCask {
				plan.Command = append(plan.Command, "--cask")
			}
			plan.Command = append(plan.Command, plan.Package)
		case "npm":
			pkg := packageWithoutLatest(plan.Package)
			plan.Command = []string{"npm", "install", "-g", "--prefix", plan.PackagePrefix, pkg + "@latest"}
		case "winget":
			plan.Command = []string{"winget", "upgrade", "-e", "--id", plan.Package, "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}
		case "uv":
			plan.Command = []string{"uv", "tool", "upgrade", plan.Package}
		case "pipx":
			plan.Command = []string{"pipx", "upgrade", plan.Package}
		case "bun":
			plan.Command = []string{"bun", "install", "-g", packageWithoutLatest(plan.Package) + "@latest"}
		case "official-installer":
			// These self-updaters do not reliably replace the installed binary
			// headlessly: installerUpdateTargets on every platform, and these
			// Windows ones. Re-run the vendor's installer instead; ownership and
			// post-update version checks still guard the operation.
			if installScript != nil && (installerUpdateTargets[plan.Target] || s.goos == "windows" &&
				(plan.Target == TargetAutohand || plan.Target == TargetGrok)) {
				plan.Command = nil
				plan.Script = installScript
				break
			}
			command, ok := vendorUpdateCommands[plan.Target]
			if !ok {
				plan.Unsupported = true
				plan.Reason = "This vendor installer does not expose a supported update command."
			} else {
				plan.Command = slices.Clone(command)
			}
		default:
			plan.Unsupported = true
			plan.Reason = "This installation method does not provide a supported update command."
		}
		if !plan.Unsupported {
			plan.UpdateWhileRunning = updateWhileRunningLevel(s.goos, plan.Target, plan.Method, plan.PackageCask)
		}
		if s.managedEligible(plan) {
			// Installed side by side, so no session's files are touched, on any
			// platform: the update is safe while sessions run.
			plan.managed = true
			plan.UpdateWhileRunning = UpdateWhileRunningSafe
			argv, _ := managedInstallCommand(plan.Method, packageWithoutLatest(plan.Package), "", filepath.Join(s.managedTargetDir(plan.Target), "versions", "<next>"))
			plan.Command, plan.Prepare = argv, nil
		}
		return plan
	}

	return plan
}

// vendorUpdateCommands are the harnesses' own self-update commands, used only
// when the vendor installer, not a package manager, owns the running binary.
// Each updates the stable channel without a terminal: Kimchi's "self" leaves
// its extensions alone and --force skips its confirmation prompt, and
// OpenCode is told the install method AO already proved. Amp's --porcelain
// prints a one-line result instead of interactive progress, and Pi updates
// only itself without trusting project-local files.
var vendorUpdateCommands = map[Target][]string{
	TargetClaudeCode: {"claude", "update"},
	TargetCodex:      {"codex", "update"},
	TargetOpencode:   {"opencode", "upgrade", "--method", "curl"},
	TargetGoose:      {"goose", "update"},
	TargetKimchi:     {"kimchi", "update", "self", "--force"},
	TargetOMP:        {"omp", "update"},
	TargetAutohand:   {"autohand", "update"},
	TargetCursor:     {"cursor-agent", "update"},
	TargetAmp:        {"amp", "update", "--porcelain"},
	TargetGrok:       {"grok", "update"},
	TargetDroid:      {"droid", "update"},
	TargetQwen:       {"qwen", "update"},
	TargetFX:         {"fx", "upgrade"},
	TargetPi:         {"pi", "update", "self", "--no-approve"},
	TargetPrimeAgent: {"prime-agent", "update"},
}

// vendorUpdateEnv turns a vendor's own auto-update switch back on for an
// update the user asked for. `droid update` exits 0 without updating, and
// prints "Auto-update is not available for npm installations", whenever
// FACTORY_DROID_AUTO_UPDATE_ENABLED is false or its build (npm's) turns
// auto-update off, even for the copy at the official installer's location.
var vendorUpdateEnv = map[Target][]string{
	TargetDroid: {"FACTORY_DROID_AUTO_UPDATE_ENABLED=true"},
}

// installerUpdateTargets update by re-running the vendor installer, pinned to
// the approved release and bound to the confirmed install directory. Kimi's
// `kimi upgrade` exits 0 without replacing the binary: it reports "already up
// to date" from its own staged-rollout channel, prints a manual command for
// installs it does not manage, and on native installs only stages the new
// binary for a later launch to swap in.
var installerUpdateTargets = map[Target]bool{
	TargetKimi: true,
}

func packageWithoutLatest(pkg string) string {
	return strings.TrimSuffix(pkg, "@latest")
}

// planAgent preserves the legacy single-plan call sites while selecting from
// the same method registry used by the catalog and execution route.
func (s *Service) planAgent(target Target) Plan {
	planner, err := s.newRequestPlanner(context.Background())
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "manual", Reason: "install capabilities could not be inspected", DocsURL: agentDocumentationURLs[target]}
	}
	plans := planner.agentMethodPlans(target, AgentOperationInstall)
	return plans[recommendedPlanIndex(plans)]
}

func (s *Service) officialByOS(target Target, unixURL, unixShell, windowsURL, docsURL string) Plan {
	switch s.goos {
	case "windows":
		return withDocs(s.planPowerShellInstaller(target, windowsURL), docsURL)
	case "darwin", "linux":
		return withDocs(s.planShellInstaller(target, unixURL, unixShell), docsURL)
	default:
		return manualPlan(target, fmt.Sprintf("The official installer does not support %s.", s.goos), docsURL)
	}
}

func (s *Service) planShellInstaller(target Target, url, shell string) Plan {
	resolved, err := s.executables.LookPath(shell)
	if err != nil {
		return Plan{Target: target, Unsupported: true, Method: "official-installer", Reason: fmt.Sprintf("%s was not found on PATH.", shell)}
	}
	return Plan{
		Target: target, Method: "official-installer",
		Script: &ports.InstallScriptCommand{URL: url, Interpreter: []string{resolved}},
	}
}

func (s *Service) planPowerShellInstaller(target Target, url string) Plan {
	// WindowsApps can expose a pwsh.exe execution alias that LookPath finds but
	// the daemon cannot launch. Prefer the Windows PowerShell binary instead.
	for _, shell := range []string{"powershell.exe", "powershell", "pwsh.exe", "pwsh"} {
		resolved, err := s.executables.LookPath(shell)
		if err != nil {
			continue
		}
		return Plan{
			Target: target, Method: "official-installer",
			Script: &ports.InstallScriptCommand{
				URL:         url,
				Interpreter: []string{resolved, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"},
			},
		}
	}
	return Plan{Target: target, Unsupported: true, Method: "official-installer", Reason: "PowerShell was not found on PATH."}
}

func (s *Service) planUV(target Target, pkg string) Plan {
	if _, err := s.executables.LookPath("uv"); err != nil {
		return Plan{Target: target, Unsupported: true, Method: "uv", Reason: "uv was not found on PATH. Install uv, then retry."}
	}
	return Plan{Target: target, Method: "uv", Command: []string{"uv", "tool", "install", pkg}, Package: pkg}
}

func (s *Service) planBun(target Target) Plan {
	const pkg = "@oh-my-pi/pi-coding-agent"
	if _, err := s.executables.LookPath("bun"); err != nil {
		return Plan{Target: target, Unsupported: true, Method: "bun", Reason: "Bun was not found on PATH."}
	}
	return Plan{Target: target, Method: "bun", Command: []string{"bun", "install", "-g", pkg}, Package: pkg}
}

func (s *Service) planPipx(target Target, pkg string) Plan {
	if _, err := s.executables.LookPath("pipx"); err != nil {
		return Plan{Target: target, Unsupported: true, Method: "pipx", Reason: "pipx was not found on PATH. Install pipx, then retry."}
	}
	return Plan{Target: target, Method: "pipx", Command: []string{"pipx", "install", pkg}, Package: pkg}
}

func manualPlan(target Target, reason, docsURL string) Plan {
	return Plan{Target: target, Unsupported: true, Method: "manual", Reason: reason, DocsURL: docsURL}
}

// pinVendorUpdate asks OpenCode and Kimi's installer for the exact release
// the update advisory reported, so the version installed is the one the
// update badge announced.
func pinVendorUpdate(plan Plan, latest string) Plan {
	if plan.Method != "official-installer" || latest == "" {
		return plan
	}
	if plan.Target == TargetKimi && plan.Script != nil {
		plan.Script = withInstallerEnv(plan.Script, "KIMI_VERSION="+latest)
		return plan
	}
	if plan.Target != TargetOpencode || len(plan.Command) < 2 {
		return plan
	}
	plan.Command = append([]string{plan.Command[0], plan.Command[1], latest}, plan.Command[2:]...)
	return plan
}

// withInstallerEnv returns a copy of script whose environment sets entries,
// replacing any earlier value of the same keys. Keys compare case-insensitively
// because Windows environment names do.
func withInstallerEnv(script *ports.InstallScriptCommand, entries ...string) *ports.InstallScriptCommand {
	next := *script
	next.Env = nil
	for _, entry := range script.Env {
		key, _, _ := strings.Cut(entry, "=")
		replaced := slices.ContainsFunc(entries, func(set string) bool {
			setKey, _, _ := strings.Cut(set, "=")
			return strings.EqualFold(key, setKey)
		})
		if !replaced {
			next.Env = append(next.Env, entry)
		}
	}
	next.Env = append(next.Env, entries...)
	return &next
}

// bindKimiInstallerUpdate binds a Kimi installer update to the Kimi Code
// native install sessions run. Only the installer's own default root,
// ~/.kimi-code, is accepted: a kimi in ~/.local, ~/.cargo or /usr/local may
// belong to another tool (the legacy Python kimi-cli, a cargo build), and the
// installer would replace it with the native binary. KIMI_NO_MODIFY_PATH and
// a PATH without <root>/bin keep the installer from editing shell rc files
// and from its legacy-shim migration, which renames or deletes other kimi
// commands on PATH; the update only replaces <root>/bin/kimi.
func bindKimiInstallerUpdate(goos string, plan Plan, binaryPath, home, pathList string) Plan {
	if plan.Target != TargetKimi || plan.Method != "official-installer" || plan.Script == nil {
		return plan
	}
	root, ok := kimiInstallRoot(goos, binaryPath, home)
	if !ok {
		plan.Unsupported = true
		plan.Reason = fmt.Sprintf("AO updates Kimi Code only in its default ~/.kimi-code installation; %q is managed elsewhere, so update it manually.", binaryPath)
		return plan
	}
	env := []string{"KIMI_INSTALL_DIR=" + root, "KIMI_NO_MODIFY_PATH=1"}
	if pathList != "" {
		env = append(env, "PATH="+pathWithoutDir(goos, pathList, root+kimiPathSeparator(goos)+"bin"))
	}
	plan.Script = withInstallerEnv(plan.Script, env...)
	plan.ExpectedDestination = binaryPath
	return plan
}

// kimiInstallRoot is the KIMI_INSTALL_DIR whose bin/kimi is binaryPath, when
// that is Kimi Code's default root under home. Windows paths compare
// case-insensitively with either separator; other platforms compare exactly.
func kimiInstallRoot(goos, binaryPath, home string) (string, bool) {
	if home == "" {
		return "", false
	}
	if goos != "windows" {
		root, found := strings.CutSuffix(binaryPath, "/bin/kimi")
		return root, found && path.Clean(root) == path.Clean(home+"/.kimi-code")
	}
	slashed := strings.ToLower(strings.ReplaceAll(binaryPath, `\`, "/"))
	if !strings.HasSuffix(slashed, "/bin/kimi.exe") {
		return "", false
	}
	root := binaryPath[:len(binaryPath)-len("/bin/kimi.exe")]
	want := strings.ToLower(strings.ReplaceAll(home, `\`, "/")) + "/.kimi-code"
	return root, path.Clean(slashed[:len(root)]) == path.Clean(want)
}

func kimiPathSeparator(goos string) string {
	if goos == "windows" {
		return `\`
	}
	return "/"
}

// pathWithoutDir removes dir from a PATH list, comparing as the platform does.
func pathWithoutDir(goos, pathList, dir string) string {
	separator, clean := ":", path.Clean
	if goos == "windows" {
		separator = ";"
		clean = func(entry string) string {
			return path.Clean(strings.ToLower(strings.ReplaceAll(entry, `\`, "/")))
		}
	}
	var kept []string
	for _, entry := range strings.Split(pathList, separator) {
		if entry == "" || clean(entry) != clean(dir) {
			kept = append(kept, entry)
		}
	}
	return strings.Join(kept, separator)
}

// targetInstalledCopy points an update at the copy sessions run. A
// vendor self-update runs from that binary rather than whichever copy PATH
// finds first, and npm acts on the global prefix that holds the package with
// that prefix's own Node, which need not be the daemon's npm.
func targetInstalledCopy(goos string, plan Plan, target Target, binaryPath string) Plan {
	if goos == "windows" && target == TargetGrok && plan.Method == "official-installer" && plan.Script != nil {
		// PowerShell's installer defaults to the user's profile. Bind its
		// destination even when the confirmed copy lives on another drive/share.
		slashed := strings.ReplaceAll(binaryPath, `\`, "/")
		driveAbsolute := len(slashed) >= 3 && ((slashed[0] >= 'A' && slashed[0] <= 'Z') || (slashed[0] >= 'a' && slashed[0] <= 'z')) && slashed[1:3] == ":/"
		uncAbsolute := strings.HasPrefix(slashed, "//") && len(strings.Split(strings.TrimPrefix(slashed, "//"), "/")) >= 3
		if !driveAbsolute && !uncAbsolute {
			plan.Unsupported = true
			plan.Reason = "AO could not preserve the confirmed Grok installation directory; update it manually."
			return plan
		}
		end := strings.LastIndex(slashed, "/")
		if driveAbsolute && end == 2 {
			end++ // Keep the separator in a drive root such as D:\.
		}
		plan.Script = withInstallerEnv(plan.Script, "GROK_BIN_DIR="+binaryPath[:end])
		plan.ExpectedDestination = binaryPath
		return plan
	}
	if len(plan.Command) == 0 {
		return plan
	}
	switch plan.Method {
	case "official-installer":
		if command, ok := vendorUpdateCommands[target]; ok && plan.Command[0] == command[0] {
			plan.Command = append([]string{binaryPath}, plan.Command[1:]...)
		}
	case "npm":
		prefix, ok := npmPrefixOwningBinary(binaryPath, packageWithoutLatest(plan.Package))
		if !ok || plan.Command[0] != "npm" {
			return plan
		}
		args := slices.Clone(plan.Command[1:])
		for index := 0; index+1 < len(args); index++ {
			if args[index] == "--prefix" {
				args[index+1] = prefix
			}
		}
		plan.PackagePrefix = prefix
		plan.ownerPrefixFound = true
		plan.Command = append(npmCommandForPrefix(goos, prefix), args...)
	}
	return plan
}

// validateNPMUpdateTarget checks the installation an npm update acts on.
// planNPM validated the daemon's Node/npm and global prefix; an update that
// targets another prefix, or that deferred a daemon rejection, is validated
// against the owning prefix and the runtime that will run the update: that
// prefix's own Node and npm when it has them, else the daemon's.
func (s *Service) validateNPMUpdateTarget(ctx context.Context, plan Plan, plannedPrefix string) Plan {
	deferred, runtimeRejected, prefixRejected, ownerFound := plan.deferredReason, plan.daemonRuntimeRejected, plan.daemonPrefixRejected, plan.ownerPrefixFound
	plan.deferredReason, plan.daemonRuntimeRejected, plan.daemonPrefixRejected, plan.ownerPrefixFound = "", false, false, false
	if plan.Method != "npm" || plan.Unsupported {
		return plan
	}
	reject := func(reason string) Plan {
		plan.Unsupported = true
		plan.Reason = reason
		return plan
	}
	if !samePath(plan.PackagePrefix, plannedPrefix) || prefixRejected {
		if !ownerFound && deferred != "" {
			return reject(deferred)
		}
		writable := false
		if s.pathWritable != nil && plan.PackagePrefix != "" {
			writable, _ = s.pathWritable.PathWritable(ctx, plan.PackagePrefix)
		}
		if !writable {
			return reject(fmt.Sprintf("The npm prefix %s that holds the %s sessions run is not writable by the current user. Update it manually; AO will not use sudo.", plan.PackagePrefix, plan.Target))
		}
	}
	if len(plan.Command) > 1 && plan.Command[0] != "npm" {
		// npmCommandForPrefix chose this prefix's own Node and npm.
		minimum := minimumNodeVersionForTarget(plan.Target)
		node, nodeOK := s.toolVersion(ctx, plan.Command[0], "--version")
		_, npmOK := s.toolVersion(ctx, plan.Command[0], plan.Command[1], "--version")
		if !nodeOK || !npmOK {
			return reject(fmt.Sprintf("AO could not validate the Node.js and npm in %s that update %s.", plan.PackagePrefix, plan.Target))
		}
		if !versionAtLeast(node, minimum) {
			return reject(fmt.Sprintf("Node.js %s+ is required to update %s with the Node in %s; found %s.", formatToolVersion(minimum), plan.Target, plan.PackagePrefix, formatToolVersion(node)))
		}
	} else if runtimeRejected {
		// npm from PATH runs the update on the daemon's own Node.
		return reject(deferred)
	}
	return plan
}

// toolVersion runs a version probe and parses its first line.
func (s *Service) toolVersion(ctx context.Context, argv ...string) ([3]int, bool) {
	output, err := runProbe(ctx, s.commands, localProbeTimeout, false, argv)
	if err != nil {
		return [3]int{}, false
	}
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return parseToolVersion(strings.TrimSpace(line))
}
