package binaryutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func TestDiscoveryExecutablePackageBinLayouts(t *testing.T) {
	for _, tc := range []struct{ pkg, bin, agent, interpreter string }{
		{"@kilocode/cli", "bin/kilo", "kilocode", "node"},
		{"@mimo-ai/cli", "bin/mimo", "mimo-code", "node"},
		{"@factory/cli", "bin/droid", "droid", "node"},
		{"@oh-my-pi/pi-coding-agent", "dist/cli.js", "omp", "bun"},
		{"@mariozechner/pi-coding-agent", "dist/cli.js", "pi", "node"},
	} {
		for _, install := range []string{"npm/lib", "bun/install/global"} {
			t.Run(tc.pkg+"/"+install, func(t *testing.T) {
				root := t.TempDir()
				entry := writeDiscoveryLauncher(t, root, install+"/node_modules/"+tc.pkg+"/"+tc.bin,
					"#!/usr/bin/env "+tc.interpreter+"\n// Native launcher: this is JavaScript, not a shell redirect.\n// autohand is only comment text\n")
				inspection := &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}
				if ok, err := inspectDiscoveryExecutable(t.Context(), tc.agent, entry, inspection); err != nil || !ok {
					t.Fatalf("package entry = %t, %v", ok, err)
				}
				if tc.agent == "omp" {
					if _, err := inspectDiscoveryExecutable(t.Context(), "pi", entry, &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}); err == nil {
						t.Fatal("OMP package accepted as Pi")
					}
				}
				if runtime.GOOS != "windows" {
					alias := filepath.Join(root, strings.ReplaceAll(tc.agent, "mimo-code", "mimo"))
					if err := os.Symlink(entry, alias); err != nil {
						t.Fatal(err)
					}
					if got, err := VerifyExecutable(t.Context(), tc.agent, alias); err != nil || got != alias {
						t.Fatalf("package bin symlink = %q, %v", got, err)
					}
				}
			})
		}
	}
}

func TestDiscoveryExecutablePackageCannotHideForeignEntry(t *testing.T) {
	entry := writeDiscoveryLauncher(t, t.TempDir(), "node_modules/@factory/cli/bin/autohand", "#!/usr/bin/env node\n")
	if _, err := inspectDiscoveryExecutable(t.Context(), "droid", entry, &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}); err == nil || !strings.Contains(err.Error(), "autohand") {
		t.Fatalf("foreign entry inside owned package = %v", err)
	}
}

func TestDiscoveryExecutableManagerShimLayouts(t *testing.T) {
	for _, manager := range []string{"asdf", "pyenv", "nodenv"} {
		for _, agent := range []string{"claude-code", "codex", "aider"} {
			t.Run(manager+"/"+agent, func(t *testing.T) {
				root := t.TempDir()
				binary := agent
				if agent == "claude-code" {
					binary = "claude"
				}
				managerPath := filepath.Join(root, "libexec", manager)
				var body string
				if manager == "asdf" {
					// asdf >= 0.16 dispatches to its native runtime on PATH.
					name := manager
					if runtime.GOOS == "windows" {
						name += ".exe"
					}
					managerPath = writeDiscoveryLauncher(t, root, "libexec/"+name, "MZ native runtime fixture")
					t.Setenv("PATH", filepath.Dir(managerPath))
					body = "#!/usr/bin/env bash\n# asdf-plugin: nodejs 22.0.0\nexec asdf exec \"" + binary + "\" \"$@\"\n"
				} else {
					data, err := os.ReadFile(filepath.Join("testdata", "managers", manager))
					if err != nil {
						t.Fatal(err)
					}
					writeDiscoveryLauncher(t, root, "libexec/"+manager, string(data))
					data, err = os.ReadFile(filepath.Join("testdata", "managers", manager+"-shim"))
					if err != nil {
						t.Fatal(err)
					}
					body = strings.ReplaceAll(string(data), "@ROOT@", filepath.ToSlash(root))
				}
				name := binary
				if runtime.GOOS == "windows" {
					name += ".cmd" // Inspect the POSIX content without requiring Windows symlink privileges.
				}
				shim := writeDiscoveryLauncher(t, root, "shims/"+name, body)
				if got, err := VerifyExecutable(t.Context(), agent, shim); err != nil || got != shim {
					t.Fatalf("standard %s shim = %q, %v", manager, got, err)
				}
				for _, replacement := range []string{
					strings.ReplaceAll(body, `exec "$program" "$@"`, `exec "autohand" "$@"`),
					strings.ReplaceAll(body, `exec "`+binary+`" "$@"`, `exec "autohand" "$@"`),
					"#!/usr/bin/env bash\nexec autohand \"$@\"\n",
					strings.ReplaceAll(body, "exec ", "autohand\nexec "),
				} {
					if replacement == body {
						continue
					}
					writeDiscoveryLauncher(t, root, "shims/"+name, replacement)
					if _, err := VerifyExecutable(t.Context(), agent, shim); err == nil {
						t.Fatal("modified manager shim was accepted")
					}
				}
				writeDiscoveryLauncher(t, root, "shims/"+name, body)
				// A recognizable script dispatcher cannot hide a direct overwrite.
				if manager != "asdf" {
					f, err := os.OpenFile(managerPath, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.WriteString("\nexec autohand \"$@\"\n")
					_ = f.Close()
					if err != nil {
						t.Fatal(err)
					}
					if _, err := VerifyExecutable(t.Context(), agent, shim); err == nil || !strings.Contains(err.Error(), "autohand") {
						t.Fatalf("overwritten manager = %v", err)
					}
				}
			})
		}
	}
}

// Native CI uses the pinned managers themselves to generate and execute shims.
// The managed command is a harmless version fixture; no login is attempted.
func TestDiscoveryExecutableNativeManagers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("upstream POSIX version managers do not run natively on Windows")
	}
	managerRoot := os.Getenv("AO_TEST_MANAGERS_ROOT")
	if managerRoot == "" {
		t.Skip("set AO_TEST_MANAGERS_ROOT to the pinned native manager installs")
	}
	for _, manager := range []string{"asdf", "pyenv", "nodenv"} {
		t.Run(manager, func(t *testing.T) {
			repo := filepath.Join(managerRoot, manager)
			dataRoot := repo
			version := "22.0.0"
			agents := []string{"claude-code", "codex"}
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", filepath.Join(repo, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			install := "versions"
			if manager == "asdf" {
				dataRoot = t.TempDir()
				install = filepath.Join("installs", "nodejs")
				t.Setenv("ASDF_DATA_DIR", dataRoot)
				t.Setenv("ASDF_NODEJS_VERSION", version)
				if err := os.MkdirAll(filepath.Join(dataRoot, "plugins", "nodejs"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if manager == "pyenv" {
					version, agents = "3.12.0", []string{"aider"}
				}
				t.Setenv(strings.ToUpper(manager)+"_ROOT", dataRoot)
				t.Setenv(strings.ToUpper(manager)+"_VERSION", version)
			}
			for _, agent := range agents {
				bin := agent
				if agent == "claude-code" {
					bin = "claude"
				}
				writeDiscoveryLauncher(t, dataRoot, filepath.Join(install, version, "bin", bin),
					"#!/bin/sh\nprintf 'fixture-"+agent+"\\n'\n")
			}
			args := []string{"rehash"}
			if manager == "asdf" {
				args = []string{"reshim", "nodejs", version}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			managerBin := filepath.Join(repo, "bin", manager)
			if out, err := aoprocess.CommandContext(ctx, managerBin, args...).CombinedOutput(); err != nil {
				t.Fatalf("generate real %s shims: %v: %s", manager, err, out)
			}
			for _, agent := range agents {
				bin := agent
				if agent == "claude-code" {
					bin = "claude"
				}
				shim := filepath.Join(dataRoot, "shims", bin)
				selected, err := VerifyExecutable(ctx, agent, shim)
				if err != nil || selected != shim {
					t.Fatalf("real %s shim: %q, %v", manager, selected, err)
				}
				out, err := aoprocess.CommandContext(ctx, selected, "--version").CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) != "fixture-"+agent {
					t.Fatalf("execute real %s shim: %v: %s", manager, err, out)
				}
				if err := os.WriteFile(shim, []byte("#!/usr/bin/env bash\nexec autohand \"$@\"\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				if _, err := VerifyExecutable(ctx, agent, shim); err == nil || !strings.Contains(err.Error(), "autohand") {
					t.Fatalf("overwritten real %s shim: %v", manager, err)
				}
			}
		})
	}
}

// CI installs pinned real packages in an isolated prefix on every native OS.
// Normal unit tests remain offline when these integration roots are unset.
func TestDiscoveryExecutableRealPackageLayouts(t *testing.T) {
	prefix := os.Getenv("AO_TEST_NPM_PREFIX")
	if prefix == "" {
		t.Skip("set AO_TEST_NPM_PREFIX to the isolated real-package prefix")
	}
	packages := []struct{ agent, bin, pkg, entry string }{
		{"kilocode", "kilocode", "@kilocode/cli", "bin/kilo"},
		{"mimo-code", "mimo", "@mimo-ai/cli", "bin/mimo"},
		{"droid", "droid", "@factory/cli", "bin/droid"},
		{"omp", "omp", "@oh-my-pi/pi-coding-agent", "dist/cli.js"},
	}
	for _, install := range []string{"npm", "bun"} {
		for _, pkg := range packages {
			if install == "bun" && pkg.agent != "omp" {
				continue
			}
			t.Run(install+"/"+pkg.agent, func(t *testing.T) {
				packageRoot, binRoot := filepath.Join(prefix, "node_modules"), prefix
				if runtime.GOOS != "windows" {
					packageRoot, binRoot = filepath.Join(prefix, "lib", "node_modules"), filepath.Join(prefix, "bin")
				}
				bin := pkg.bin
				if install == "bun" {
					bunRoot := os.Getenv("AO_TEST_BUN_ROOT")
					if bunRoot == "" {
						t.Fatal("AO_TEST_BUN_ROOT is required for the real bun layout")
					}
					packageRoot, binRoot = filepath.Join(bunRoot, "install", "global", "node_modules"), filepath.Join(bunRoot, "bin")
					if runtime.GOOS == "windows" {
						bin += ".exe"
					}
				} else if runtime.GOOS == "windows" {
					bin += ".cmd"
				}
				entry := filepath.Join(packageRoot, filepath.FromSlash(pkg.pkg), filepath.FromSlash(pkg.entry))
				if ok, err := inspectDiscoveryExecutable(t.Context(), pkg.agent, entry, &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}); err != nil || !ok {
					t.Fatalf("real canonical package entry = %t, %v", ok, err)
				}
				selected, err := VerifyExecutable(t.Context(), pkg.agent, filepath.Join(binRoot, bin))
				if err != nil {
					t.Fatal(err)
				}
				profile := t.TempDir()
				for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
					t.Setenv(key, profile)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				cmd := aoprocess.CommandContext(ctx, selected, "--version")
				cmd.Dir = profile
				output, err := cmd.CombinedOutput()
				if err != nil || strings.TrimSpace(string(output)) == "" {
					t.Fatalf("real version command = %v, output=%s", err, output)
				}
				t.Logf("%s version: %s", pkg.agent, strings.TrimSpace(string(output)))
			})
		}
	}
}
