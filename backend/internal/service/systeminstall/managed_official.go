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

// officialManagedRecipe runs a vendor's own installer into one AO release
// directory, pinned to the release the update picked, so the update never
// touches the files a running session uses. Each recipe is read from the
// vendor's script: where it installs, how it takes a version, and what keeps
// it from editing the user's shell files or their own copy.
type officialManagedRecipe struct {
	// binRel is where the installer puts the CLI inside the release directory.
	binRel string
	// env and args configure one install into dir; pin is a bare version
	// ("1.2.3"), or "" for the installer's latest.
	env  func(dir, pin string) []string
	args func(dir, pin string) []string
	// command, when set, replaces the vendor's script: AO downloads the
	// release itself because the script cannot run beside live sessions.
	command func(dir, pin, goos, goarch string) ([]string, error)
}

// managedInstallerPath is the PATH an official installer runs with: system
// tools only. Several installers look for an existing copy on PATH (to skip
// an install they think is current, or to migrate or replace it), which must
// never be the user's own copy or AO's current release.
const managedInstallerPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// officialInstallerEnv is the environment every recipe shares: a private HOME
// inside the release, so any profile or config an installer writes lands
// there instead of in the user's files, a PATH of system tools, and a plain
// sh as SHELL. Installers pick the profile they append PATH to from SHELL;
// fx, for one, writes $HOME/.config/fish/config.fish for fish users without
// creating its folder, and fails under set -e.
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
	// opencode.ai/install installs to $HOME/.opencode/bin, takes VERSION, and
	// skips the install when the opencode on PATH already reports it.
	TargetOpencode: {
		binRel: filepath.Join(".install-home", ".opencode", "bin", "opencode"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: func(string, string) []string { return []string{"--no-modify-path"} },
	},
	// Kimi installs to $KIMI_INSTALL_DIR/bin, migrates other kimi executables
	// it finds on PATH, and writes a region marker to $KIMI_CODE_HOME.
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
	// Memcode takes a release tag, and only uses sudo or a terminal when it
	// has one, which the daemon does not.
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
	// fx takes its release tag as the first argument.
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
	// OMP builds from source with bun unless told to install its binary.
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
	// Qwen's standalone install swaps and deletes its whole library folder
	// in place, so a running session's later imports would load the new
	// release; a release of its own keeps them on the old one.
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
	// Codex keeps each release in its own folder under $CODEX_HOME; a
	// private CODEX_HOME keeps the user's config, auth and their own
	// installs out of it.
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
	// OpenCode 2 installs the same opencode binary (plus an opencode2 shim)
	// to $HOME/.opencode/bin; its launcher stays distinct from version 1's.
	TargetOpencodeV2: {
		binRel: filepath.Join(".install-home", ".opencode", "bin", "opencode"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: func(string, string) []string { return []string{"--no-modify-path"} },
	},
	// Amp installs to $AMP_HOME/bin and always links itself into the HOME's
	// ~/.local/bin, which the private HOME keeps away from the user's.
	TargetAmp: {
		binRel: filepath.Join("bin", "amp"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "AMP_HOME="+dir, "AMP_VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: noArgs,
	},
	// Autohand also installs zit, a computer-control component and a first
	// run, and replaces any agent command it can write on PATH.
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
	// Agy installs its latest release only; the installed version is still
	// checked against the release the update picked.
	TargetAgy: {
		binRel: filepath.Join("bin", "agy"),
		env:    func(dir, _ string) []string { return officialInstallerEnv(dir) },
		args: func(dir, _ string) []string {
			return []string{"--dir", filepath.Join(dir, "bin")}
		},
	},
	// Droid's script kills every running droid before it installs, so AO
	// downloads the release and its checksum from the same place itself.
	TargetDroid: {
		binRel:  filepath.Join("bin", "droid"),
		env:     func(dir, _ string) []string { return officialInstallerEnv(dir) },
		args:    noArgs,
		command: droidDownloadCommand,
	},
}

// officialManagedCask is a Homebrew cask whose upgrade deletes files a
// running session can still load. AO updates it beside running sessions with
// the vendor's installer instead, into a release of its own; the cask itself
// is left as it is.
type officialManagedCask struct {
	cask, url, shell string
}

// codexInstallScriptURL is Codex's official macOS and Linux installer.
const codexInstallScriptURL = "https://chatgpt.com/codex/install.sh"

const ompInstallScriptURL = "https://omp.sh/install"

// officialManagedCasks: Codex's cask also ships codex-resources, which an
// upgrade removes while sessions may still read it.
var officialManagedCasks = map[Target]officialManagedCask{
	TargetCodex: {cask: "codex", url: codexInstallScriptURL, shell: "sh"},
}

// officialManagedCaskFor returns the cask entry when plan upgrades it.
func officialManagedCaskFor(goos string, plan Plan) (officialManagedCask, bool) {
	entry, ok := officialManagedCasks[plan.Target]
	return entry, ok && goos == "darwin" && plan.Method == "homebrew" && plan.PackageCask && plan.Package == entry.cask
}

// officialManagedRecipeFor returns the recipe that installs plan's official
// installer as a managed release, on the platforms it was read for.
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

// officialManagedScript returns the vendor installer that installs plan as a
// release of AO's own: installScript, plan's official installer, or for a
// cask in officialManagedCasks the installer AO runs instead of the cask.
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

// officialManagedUpdate reports whether one of plans, a target's install
// plans, is an official installer AO runs into a release of its own. Such an
// update maintains the copy sessions run wherever the vendor's installer put
// the user's copy, since it never writes there.
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

// runOfficialManagedInstall installs one release into dir: the vendor's
// installer script, or the recipe's own download.
func (s *Service) runOfficialManagedInstall(ctx context.Context, plan Plan, recipe officialManagedRecipe, pin, dir string, env []string, out io.Writer) error {
	// The private HOME must exist: installers such as Codex's and fx's append
	// their PATH line to a profile in it under set -e, and fail without it.
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
	// Only the recipe's environment reaches the installer: a variable the
	// user exported, such as KIMI_CODE_HOME or OPENCODE_INSTALL_DIR, would
	// otherwise send the install into the user's own files.
	command.Isolated = true
	_, _ = fmt.Fprintf(out, "Installing %s with its official installer into %s\n", plan.Target, dir)
	result, err := s.installScripts.RunInstallScript(ctx, command, out, out)
	if result.SHA256 != "" {
		_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
	}
	return err
}

// droidDownloadScript fetches one Droid release and its published checksum,
// verifies it, and moves it into place: what Droid's installer does, without
// killing the droids that are running.
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

// droidDownloadCommand downloads Droid's release from downloads.factory.ai,
// where its installer gets it. Intel Macs and PCs get the baseline build,
// which runs whether or not the CPU has AVX2.
func droidDownloadCommand(dir, pin, goos, goarch string) ([]string, error) {
	version := strings.TrimPrefix(pin, "v")
	if version == "" {
		return nil, errors.New("AO needs the release to install, but no newer Droid release is known")
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
