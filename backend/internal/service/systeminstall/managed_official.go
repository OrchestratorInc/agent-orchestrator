package systeminstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type officialManagedRecipe struct {
	binRel  string
	env     func(dir, pin string) []string
	args    func(dir, pin string) []string
	command func(dir, pin, goos, goarch string) ([]string, error)
}

const managedInstallerPath = "/usr/bin:/bin:/usr/sbin:/sbin"

func officialInstallerEnv(dir string, extra ...string) []string {
	return append([]string{"HOME=" + filepath.Join(dir, ".install-home"), "PATH=" + managedInstallerPath, "SHELL=/bin/sh"}, extra...)
}

func withV(pin string) string {
	if pin == "" || strings.HasPrefix(pin, "v") {
		return pin
	}
	return "v" + pin
}

func noArgs(string, string) []string { return nil }

var officialManagedRecipes = map[Target]officialManagedRecipe{
	TargetOpencode: {
		binRel: filepath.Join(".install-home", ".opencode", "bin", "opencode"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: func(string, string) []string { return []string{"--no-modify-path"} },
	},
	TargetKimi: {
		binRel: filepath.Join("bin", "kimi"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "KIMI_INSTALL_DIR="+dir, "KIMI_CODE_HOME="+filepath.Join(dir, ".install-home", ".kimi-code"),
				"KIMI_VERSION="+strings.TrimPrefix(pin, "v"), "KIMI_NO_MODIFY_PATH=1")
		},
		args: noArgs,
	},
	TargetGoose: {
		binRel: filepath.Join("bin", "goose"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "GOOSE_BIN_DIR="+filepath.Join(dir, "bin"), "GOOSE_VERSION="+withV(pin), "CONFIGURE=false")
		},
		args: noArgs,
	},
	TargetMemcode: {
		binRel: filepath.Join("bin", "memcode"),
		env: func(dir, pin string) []string {
			version := withV(pin)
			if version == "" {
				version = "latest"
			}
			return officialInstallerEnv(dir, "MEMCODE_INSTALL_DIR="+filepath.Join(dir, "bin"), "MEMCODE_VERSION="+version)
		},
		args: noArgs,
	},
	TargetFX: {
		binRel: filepath.Join("bin", "fx"),
		env: func(dir, _ string) []string {
			return officialInstallerEnv(dir, "FX_INSTALL_DIR="+filepath.Join(dir, "bin"))
		},
		args: func(_, pin string) []string {
			if pin == "" {
				return nil
			}
			return []string{withV(pin)}
		},
	},
	TargetOMP: {
		binRel: filepath.Join("bin", "omp"),
		env: func(dir, _ string) []string {
			return officialInstallerEnv(dir, "PI_INSTALL_DIR="+filepath.Join(dir, "bin"))
		},
		args: func(_, pin string) []string {
			if pin == "" {
				return []string{"--binary"}
			}
			return []string{"--binary", "--ref", withV(pin)}
		},
	},
	TargetQwen: {
		binRel: filepath.Join("bin", "qwen"),
		env: func(dir, pin string) []string {
			version := strings.TrimPrefix(pin, "v")
			if version == "" {
				version = "latest"
			}
			return officialInstallerEnv(dir,
				"QWEN_INSTALL_LIB_DIR="+filepath.Join(dir, "lib"),
				"QWEN_INSTALL_BIN_DIR="+filepath.Join(dir, "bin"),
				"QWEN_INSTALL_VERSION="+version,
				"QWEN_INSTALL_METHOD=standalone",
				"QWEN_NO_MODIFY_PATH=1")
		},
		args: noArgs,
	},
	TargetCodex: {
		binRel: filepath.Join("bin", "codex"),
		env: func(dir, pin string) []string {
			release := strings.TrimPrefix(pin, "v")
			if release == "" {
				release = "latest"
			}
			return officialInstallerEnv(dir,
				"CODEX_HOME="+filepath.Join(dir, "codex-home"),
				"CODEX_INSTALL_DIR="+filepath.Join(dir, "bin"),
				"CODEX_RELEASE="+release,
				"CODEX_NON_INTERACTIVE=1")
		},
		args: noArgs,
	},
	TargetOpencodeV2: {
		binRel: filepath.Join(".install-home", ".opencode", "bin", "opencode"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: func(string, string) []string { return []string{"--no-modify-path"} },
	},
	TargetAmp: {
		binRel: filepath.Join("bin", "amp"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "AMP_HOME="+dir, "AMP_VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: noArgs,
	},
	TargetAutohand: {
		binRel: filepath.Join("bin", "autohand"),
		env: func(dir, pin string) []string {
			version := strings.TrimPrefix(pin, "v")
			if version == "" {
				version = "latest"
			}
			return officialInstallerEnv(dir,
				"AUTOHAND_INSTALL_DIR="+filepath.Join(dir, "bin"),
				"AUTOHAND_VERSION="+version,
				"AUTOHAND_SKIP_ZIT_INSTALL=1",
				"AUTOHAND_SKIP_COMPUTER_CONTROL_INSTALL=1",
				"AUTOHAND_INSTALL_FIRST_RUN=no",
				"AUTOHAND_INSTALL_LOCAL_AI=0")
		},
		args: noArgs,
	},
	TargetAgy: {
		binRel: filepath.Join("bin", "agy"),
		env:    func(dir, _ string) []string { return officialInstallerEnv(dir) },
		args: func(dir, _ string) []string {
			return []string{"--dir", filepath.Join(dir, "bin")}
		},
	},
	TargetDroid: {
		binRel:  filepath.Join("bin", "droid"),
		env:     func(dir, _ string) []string { return officialInstallerEnv(dir) },
		args:    noArgs,
		command: droidDownloadCommand,
	},
}

type officialManagedCask struct {
	cask, url, shell string
}

const codexInstallScriptURL = "https://chatgpt.com/codex/install.sh"

const ompInstallScriptURL = "https://omp.sh/install"

var officialManagedCasks = map[Target]officialManagedCask{
	TargetCodex: {cask: "codex", url: codexInstallScriptURL, shell: "sh"},
}

func officialManagedCaskFor(goos string, plan Plan) (officialManagedCask, bool) {
	entry, ok := officialManagedCasks[plan.Target]
	return entry, ok && goos == "darwin" && plan.Method == "homebrew" && plan.PackageCask && plan.Package == entry.cask
}

func (s *Service) officialManagedRecipeFor(plan Plan) (officialManagedRecipe, bool) {
	_, cask := officialManagedCaskFor(s.goos, plan)
	if plan.Method != "official-installer" && !cask || plan.Script == nil || (s.goos != "darwin" && s.goos != "linux") {
		return officialManagedRecipe{}, false
	}
	recipe, ok := officialManagedRecipes[plan.Target]
	if !ok || recipe.command == nil && s.installScripts == nil || recipe.command != nil && s.installCommands == nil {
		return officialManagedRecipe{}, false
	}
	return recipe, true
}

func (s requestPlanner) officialManagedScript(plan Plan, installScript *ports.InstallScriptCommand) (*ports.InstallScriptCommand, bool) {
	if s.managedRoot == "" {
		return nil, false
	}
	script := installScript
	if entry, ok := officialManagedCaskFor(s.goos, plan); ok {
		script = s.planShellInstaller(plan.Target, entry.url, entry.shell).Script
	}
	if script == nil {
		return nil, false
	}
	plan.Script = script
	if _, ok := s.officialManagedRecipeFor(plan); !ok {
		return nil, false
	}
	return script, true
}

func (s *Service) officialManagedUpdate(plans []Plan) bool {
	if s.managedRoot == "" {
		return false
	}
	for _, plan := range plans {
		if plan.Method != "official-installer" || plan.Unsupported {
			continue
		}
		if _, ok := s.officialManagedRecipeFor(plan); ok {
			return true
		}
	}
	return false
}

func (s *Service) runOfficialManagedInstall(ctx context.Context, plan Plan, recipe officialManagedRecipe, pin, dir string, env []string, out io.Writer) error {
	if err := os.MkdirAll(filepath.Join(dir, ".install-home"), 0o700); err != nil {
		return fmt.Errorf("create installer home: %w", err)
	}
	recipeEnv := recipe.env(dir, pin)
	if recipe.command != nil {
		argv, err := recipe.command(dir, pin, s.goos, runtime.GOARCH)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Downloading %s %s into %s\n", plan.Target, pin, dir)
		return s.installCommands.RunInstall(ctx, ports.InstallCommand{
			Argv: argv, Env: append(append([]string(nil), env...), recipeEnv...), Dir: dir, Isolated: true,
		}, out, out)
	}
	command := *plan.Script
	command.Env = append(append(append([]string(nil), env...), command.Env...), recipeEnv...)
	command.Args = recipe.args(dir, pin)
	command.Isolated = true
	_, _ = fmt.Fprintf(out, "Installing %s with its official installer into %s\n", plan.Target, dir)
	result, err := s.installScripts.RunInstallScript(ctx, command, out, out)
	if result.SHA256 != "" {
		_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
	}
	return err
}

const droidDownloadScript = `set -eu
url="$1"; sum_url="$2"; dest="$3"
mkdir -p "$(dirname "$dest")"
tmp="$dest.download"
curl -fsSL -o "$tmp" "$url"
want="$(curl -fsSL "$sum_url" | awk '{print $1}')"
if command -v shasum >/dev/null 2>&1; then
  got="$(shasum -a 256 "$tmp" | awk '{print $1}')"
else
  got="$(sha256sum "$tmp" | awk '{print $1}')"
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  rm -f "$tmp"
  echo "checksum mismatch for $url" >&2
  exit 1
fi
chmod 755 "$tmp"
mv "$tmp" "$dest"
`

func droidDownloadCommand(dir, pin, goos, goarch string) ([]string, error) {
	version := strings.TrimPrefix(pin, "v")
	if version == "" {
		return nil, errors.New("no newer Droid release is known, so Orchestrator has no release to install")
	}
	var arch string
	switch goarch {
	case "arm64":
		arch = "arm64"
	case "amd64":
		arch = "x64-baseline"
	default:
		return nil, fmt.Errorf("no Droid build is published for %s", goarch)
	}
	base := fmt.Sprintf("https://downloads.factory.ai/factory-cli/releases/%s/%s/%s/droid", version, goos, arch)
	return []string{"/bin/sh", "-c", droidDownloadScript, "droid-install", base, base + ".sha256", filepath.Join(dir, "bin", "droid")}, nil
}
