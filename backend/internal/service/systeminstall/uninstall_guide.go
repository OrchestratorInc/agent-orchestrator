package systeminstall

// UninstallGuide tells a user how to remove a vendor install that AO does not
// remove itself. AO only shows it; it never runs these commands or paths.
// Unix paths use ~ and Windows paths %USERPROFILE%/%LOCALAPPDATA% for the
// user's directories.
type UninstallGuide struct {
	DocsURL string `json:"docsUrl,omitempty" description:"Vendor page with uninstall instructions."`
	// Command is the vendor's own uninstall step, when it has one.
	Command string `json:"command,omitempty" description:"Vendor uninstall command for this install."`
	// ProgramPaths remove the program itself.
	ProgramPaths []string `json:"programPaths" description:"Program files and directories to remove."`
	// UserDataPaths hold settings, sign-in and history; removing them is optional.
	UserDataPaths []string `json:"userDataPaths,omitempty" description:"Optional settings, sign-in and history paths."`
	// EditsShellProfile reports that the installer added a PATH line to the
	// user's shell profile, which removing the program leaves behind.
	EditsShellProfile bool `json:"editsShellProfile" description:"Whether the installer added a PATH line to the shell profile."`
	// Documented is true when the vendor documents these steps, false when
	// they come from its install script.
	Documented bool `json:"documented" description:"Whether the vendor documents these steps."`
}

type uninstallGuidePlatform struct {
	command  string
	program  []string
	userData []string
}

type uninstallGuideSpec struct {
	docsURL       string
	unix, windows uninstallGuidePlatform
	// darwin overrides unix on macOS when the installers differ.
	darwin            *uninstallGuidePlatform
	editsShellProfile bool
	documented        bool
}

// uninstallGuides lists, per harness, what its vendor installer writes, with
// default install locations. Entries hold only paths confirmed in the vendor's
// documentation or install script (read 2026-10-08). Paths other tools also
// create, such as ~/.local/bin/agent (Cursor, Grok, Autohand), and config
// directories shared with other products, such as ~/.cursor and ~/.gemini,
// are deliberately left out.
var uninstallGuides = map[Target]uninstallGuideSpec{
	TargetClaudeCode: {
		docsURL: "https://code.claude.com/docs/en/installation#uninstall-claude-code",
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/claude", "~/.local/share/claude"},
			userData: []string{"~/.claude", "~/.claude.json"},
		},
		windows: uninstallGuidePlatform{
			program:  []string{`%USERPROFILE%\.local\bin\claude.exe`, `%USERPROFILE%\.local\share\claude`},
			userData: []string{`%USERPROFILE%\.claude`, `%USERPROFILE%\.claude.json`},
		},
		documented: true,
	},
	TargetCodex: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/codex", "~/.local/bin/codex-code-mode-host", "~/.codex/packages/standalone"},
			userData: []string{"~/.codex"},
		},
		windows: uninstallGuidePlatform{
			program:  []string{`%LOCALAPPDATA%\Programs\OpenAI\Codex`, `%USERPROFILE%\.codex\packages\standalone`},
			userData: []string{`%USERPROFILE%\.codex`},
		},
		editsShellProfile: true,
	},
	// OpenCode 2's installer writes opencode into the same ~/.opencode/bin as
	// OpenCode 1, plus an opencode2 shim; it names no settings directories.
	TargetOpencodeV2: {
		unix:              uninstallGuidePlatform{program: []string{"~/.opencode/bin/opencode", "~/.opencode/bin/opencode2"}},
		windows:           uninstallGuidePlatform{program: []string{`%USERPROFILE%\.opencode\bin\opencode.exe`, `%USERPROFILE%\.opencode\bin\opencode2.cmd`}},
		editsShellProfile: true,
	},
	TargetAmp: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.amp/bin", "~/.amp/amp-install-version.txt", "~/.amp/signing-key.pub", "~/.local/bin/amp"},
			userData: []string{"~/.config/amp"},
		},
		windows: uninstallGuidePlatform{
			program:  []string{`%USERPROFILE%\.amp\bin`},
			userData: []string{`%USERPROFILE%\.config\amp`},
		},
		editsShellProfile: true,
	},
	TargetAutohand: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/autohand", "~/.local/bin/autohand-code", "~/.local/bin/ah"},
			userData: []string{"~/.autohand"},
		},
		windows: uninstallGuidePlatform{program: []string{`%LOCALAPPDATA%\autohand`}},
	},
	TargetCursor: {
		unix:    uninstallGuidePlatform{program: []string{"~/.local/share/cursor-agent", "~/.local/bin/cursor-agent"}},
		windows: uninstallGuidePlatform{program: []string{`%LOCALAPPDATA%\cursor-agent`}},
	},
	TargetDroid: {
		unix: uninstallGuidePlatform{program: []string{"~/.local/bin/droid"}, userData: []string{"~/.factory"}},
		windows: uninstallGuidePlatform{
			program:  []string{`%USERPROFILE%\bin\droid.exe`},
			userData: []string{`%USERPROFILE%\.factory`},
		},
	},
	TargetFX: {
		unix:              uninstallGuidePlatform{program: []string{"~/.local/bin/fx"}, userData: []string{"~/.fx"}},
		editsShellProfile: true,
	},
	TargetGrok: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.grok/downloads", "~/.grok/bin", "~/.grok/completions", "~/.local/bin/grok"},
			userData: []string{"~/.grok"},
		},
		windows: uninstallGuidePlatform{
			program:  []string{`%USERPROFILE%\.grok\bin`, `%USERPROFILE%\.grok\downloads`, `%LOCALAPPDATA%\grok\git`},
			userData: []string{`%USERPROFILE%\.grok`},
		},
		editsShellProfile: true,
	},
	TargetPrimeAgent: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/share/prime-agent", "~/.local/bin/prime-agent"},
			userData: []string{"~/.prime/agent"},
		},
		windows: uninstallGuidePlatform{
			program: []string{`%USERPROFILE%\.local\share\prime-agent`, `%USERPROFILE%\.local\bin\prime-agent.cmd`, `%USERPROFILE%\.local\bin\prime-agent`},
		},
		editsShellProfile: true,
	},
	TargetAgy: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/agy", "~/.cache/antigravity/staging"},
			userData: []string{"~/.gemini/antigravity-cli"},
		},
		windows:           uninstallGuidePlatform{program: []string{`%LOCALAPPDATA%\agy`, `%LOCALAPPDATA%\antigravity\staging`}},
		editsShellProfile: true,
	},
	TargetMuse: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/muse", "~/.local/bin/muse-bin-*", "~/.local/bin/.muse-*", "~/.config/fish/conf.d/muse.fish"},
			userData: []string{"~/.config/muse"},
		},
		windows:           uninstallGuidePlatform{program: []string{`%LOCALAPPDATA%\Programs\muse`}},
		editsShellProfile: true,
	},
	TargetKimi: {
		docsURL:           "https://www.kimi.com/code/docs/en/kimi-code-cli/guides/getting-started",
		unix:              uninstallGuidePlatform{program: []string{"~/.kimi-code/bin"}, userData: []string{"~/.kimi-code"}},
		windows:           uninstallGuidePlatform{program: []string{`%USERPROFILE%\.kimi-code\bin`}, userData: []string{`%USERPROFILE%\.kimi-code`}},
		editsShellProfile: true,
		documented:        true,
	},
	TargetKiro: {
		docsURL: "https://kiro.dev/docs/cli/installation/",
		unix: uninstallGuidePlatform{
			command:  "kiro-cli uninstall",
			program:  []string{"~/.local/bin/kiro-cli", "~/.local/bin/kiro-cli-chat"},
			userData: []string{"~/.kiro"},
		},
		darwin: &uninstallGuidePlatform{
			command:  "kiro-cli uninstall",
			program:  []string{"/Applications/Kiro CLI.app"},
			userData: []string{"~/.kiro"},
		},
		windows:    uninstallGuidePlatform{command: "kiro-cli uninstall", userData: []string{`%USERPROFILE%\.kiro`}},
		documented: true,
	},
	TargetGoose: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/goose"},
			userData: []string{"~/.config/goose", "~/.local/share/goose", "~/.local/state/goose"},
		},
		windows: uninstallGuidePlatform{
			program:  []string{`%USERPROFILE%\goose`},
			userData: []string{`%APPDATA%\Block\goose`},
		},
		editsShellProfile: true,
	},
	TargetQwen: {
		docsURL: "https://github.com/QwenLM/qwen-code/blob/main/scripts/installation/INSTALLATION_GUIDE.md",
		unix: uninstallGuidePlatform{
			command:  "curl -fsSL https://qwen-code-assets.oss-cn-hangzhou.aliyuncs.com/installation/uninstall-qwen-standalone.sh | bash",
			program:  []string{"~/.local/lib/qwen-code", "~/.local/bin/qwen"},
			userData: []string{"~/.qwen"},
		},
		windows: uninstallGuidePlatform{
			command:  `powershell -ExecutionPolicy Bypass -c "irm https://qwen-code-assets.oss-cn-hangzhou.aliyuncs.com/installation/uninstall-qwen-standalone.ps1 | iex"`,
			program:  []string{`%LOCALAPPDATA%\qwen-code`},
			userData: []string{`%USERPROFILE%\.qwen`},
		},
		documented: true,
	},
	TargetKimchi: {
		unix: uninstallGuidePlatform{
			program:  []string{"~/.local/bin/kimchi", "~/.local/share/kimchi"},
			userData: []string{"~/.config/kimchi"},
		},
		windows:           uninstallGuidePlatform{program: []string{`%LOCALAPPDATA%\Kimchi`}},
		editsShellProfile: true,
	},
	TargetOMP: {
		unix:    uninstallGuidePlatform{program: []string{"~/.local/bin/omp"}, userData: []string{"~/.omp/agent"}},
		windows: uninstallGuidePlatform{program: []string{`%LOCALAPPDATA%\omp`}, userData: []string{`%USERPROFILE%\.omp\agent`}},
	},
	TargetPi: {
		docsURL: "https://pi.dev/docs/latest/quickstart",
		// The installer's menu offers "Uninstall Pi"; it needs a terminal.
		unix: uninstallGuidePlatform{
			command:  "curl -fsSL https://pi.dev/install.sh | sh",
			program:  []string{"~/.pi/agent/install", "~/.pi/agent/bin/pi"},
			userData: []string{"~/.pi/agent"},
		},
		windows: uninstallGuidePlatform{
			command:  "irm https://pi.dev/install.ps1 | iex",
			userData: []string{`%USERPROFILE%\.pi\agent`},
		},
		documented: true,
	},
}

func uninstallGuideFor(target Target, goos string) *UninstallGuide {
	spec, ok := uninstallGuides[target]
	if !ok {
		return nil
	}
	platform := spec.unix
	switch {
	case goos == "windows":
		platform = spec.windows
	case goos == "darwin" && spec.darwin != nil:
		platform = *spec.darwin
	}
	if platform.command == "" && len(platform.program) == 0 {
		return nil
	}
	return &UninstallGuide{
		DocsURL: spec.docsURL, Command: platform.command,
		ProgramPaths: platform.program, UserDataPaths: platform.userData,
		EditsShellProfile: spec.editsShellProfile, Documented: spec.documented,
	}
}
