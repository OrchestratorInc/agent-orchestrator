package systeminstall

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
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
	args func(pin string) []string
}

// managedInstallerPath is the PATH an official installer runs with: system
// tools only. Several installers look for an existing copy on PATH (to skip
// an install they think is current, or to migrate or replace it), which must
// never be the user's own copy or AO's current release.
const managedInstallerPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// officialInstallerEnv is the environment every recipe shares: a private HOME
// inside the release, so any profile or config an installer writes lands
// there instead of in the user's files, and a PATH of system tools.
func officialInstallerEnv(dir string, extra ...string) []string {
	return append([]string{"HOME=" + filepath.Join(dir, ".install-home"), "PATH=" + managedInstallerPath}, extra...)
}

func withV(pin string) string {
	if pin == "" || strings.HasPrefix(pin, "v") {
		return pin
	}
	return "v" + pin
}

func noArgs(string) []string { return nil }

var officialManagedRecipes = map[Target]officialManagedRecipe{
	// opencode.ai/install installs to $HOME/.opencode/bin, takes VERSION, and
	// skips the install when the opencode on PATH already reports it.
	TargetOpencode: {
		binRel: filepath.Join(".install-home", ".opencode", "bin", "opencode"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "VERSION="+strings.TrimPrefix(pin, "v"))
		},
		args: func(string) []string { return []string{"--no-modify-path"} },
	},
	// Kimi installs to $KIMI_INSTALL_DIR/bin and migrates other kimi
	// executables it finds on PATH.
	TargetKimi: {
		binRel: filepath.Join("bin", "kimi"),
		env: func(dir, pin string) []string {
			return officialInstallerEnv(dir, "KIMI_INSTALL_DIR="+dir, "KIMI_VERSION="+strings.TrimPrefix(pin, "v"), "KIMI_NO_MODIFY_PATH=1")
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
		args: func(pin string) []string {
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
		args: func(pin string) []string {
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
}

// officialManagedRecipeFor returns the recipe that installs plan's official
// installer as a managed release, on the platforms it was read for.
func (s *Service) officialManagedRecipeFor(plan Plan) (officialManagedRecipe, bool) {
	if plan.Method != "official-installer" || plan.Script == nil || (s.goos != "darwin" && s.goos != "linux") {
		return officialManagedRecipe{}, false
	}
	recipe, ok := officialManagedRecipes[plan.Target]
	return recipe, ok
}

// runOfficialManagedInstall runs the vendor's installer script for one
// release into dir.
func (s *Service) runOfficialManagedInstall(ctx context.Context, plan Plan, recipe officialManagedRecipe, pin, dir string, env []string, out io.Writer) error {
	command := *plan.Script
	command.Env = append(append(append([]string(nil), env...), command.Env...), recipe.env(dir, pin)...)
	command.Args = recipe.args(pin)
	_, _ = fmt.Fprintf(out, "Installing %s with its official installer into %s\n", plan.Target, dir)
	result, err := s.installScripts.RunInstallScript(ctx, command, out, out)
	if result.SHA256 != "" {
		_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
	}
	return err
}
