package systeminstall

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestNodeShimTargetsPackage(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "bin", "codex.js"), []byte("cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(root, "codex.cmd")
	bad := filepath.Join(root, "claude.cmd")
	if err := os.WriteFile(good, []byte(`@"%~dp0\node_modules\@openai\codex\bin\codex.js" %*`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`@"%~dp0\node_modules\@anthropic-ai\claude-code\cli.js" %*`), 0o644); err != nil {
		t.Fatal(err)
	}
	if owned, err := nodeShimTargetsPackage(good, packageRoot); err != nil || !owned {
		t.Fatalf("good shim owned=%t err=%v", owned, err)
	}
	if owned, err := nodeShimTargetsPackage(bad, packageRoot); err != nil || owned {
		t.Fatalf("wrong shim owned=%t err=%v", owned, err)
	}
}

// npm's cmd-shim chains the launch after endLocal/goto/title on one line.
func TestNodeShimTargetsPackageReadsNPMCmdShim(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "bin", "codex.js"), []byte("cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "codex.cmd")
	body := "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n\r\n" +
		"IF EXIST \"%dp0%\\node.exe\" (\r\n  SET \"_prog=%dp0%\\node.exe\"\r\n) ELSE (\r\n  SET \"_prog=node\"\r\n  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n)\r\n\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@openai\\codex\\bin\\codex.js\" %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if owned, err := nodeShimTargetsPackage(shim, packageRoot); err != nil || !owned {
		t.Fatalf("npm cmd shim owned=%t err=%v", owned, err)
	}
	if got, want := nodeShimTarget(shim), filepath.Join(packageRoot, "bin", "codex.js"); got != want {
		t.Fatalf("nodeShimTarget = %q, want %q", got, want)
	}
}

func TestManagerOwnsBinaryAcceptsWindowsNPMShimForExpectedPackageOnly(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "bin", "codex.js"), []byte("cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "codex.cmd")
	if err := os.WriteFile(shim, []byte(`@"%~dp0\node_modules\@openai\codex\bin\codex.js" %*`), 0o644); err != nil {
		t.Fatal(err)
	}
	owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if !reflect.DeepEqual(argv, []string{"npm", "root", "-g"}) {
			t.Fatalf("argv = %v", argv)
		}
		_, err := io.WriteString(stdout, filepath.Join(root, "node_modules")+"\n")
		return err
	}))
	if owned, err := owner(context.Background(), shim, "npm", "@openai/codex", false); err != nil || !owned {
		t.Fatalf("expected package owned=%t err=%v", owned, err)
	}
	if owned, err := owner(context.Background(), shim, "npm", "@anthropic-ai/claude-code", false); err != nil || owned {
		t.Fatalf("wrong package owned=%t err=%v", owned, err)
	}
}

func TestManagerOwnsBinaryRecognizesSupportedManagerPackages(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		pkg      string
		path     func(string) string
		wantArgv []string
		output   func(string, string) string
	}{
		{
			name: "bun", method: "bun", pkg: "@oh-my-pi/pi-coding-agent",
			path: func(root string) string {
				return filepath.Join(root, ".bun", "install", "global", "node_modules", "@oh-my-pi", "pi-coding-agent", "bin", "omp")
			},
			wantArgv: []string{"bun", "pm", "bin", "-g"},
			output:   func(root, _ string) string { return filepath.Join(root, ".bun", "bin") + "\n" },
		},
		{
			name: "uv", method: "uv", pkg: "mistral-vibe",
			path: func(root string) string {
				return filepath.Join(root, ".local", "share", "uv", "tools", "mistral-vibe", "bin", "vibe")
			},
			wantArgv: []string{"uv", "tool", "list", "--show-paths", "--color", "never"},
			output: func(_ string, binary string) string {
				return "mistral-vibe v1.2.3\n- vibe (" + binary + ")\n"
			},
		},
		{
			name: "pipx", method: "pipx", pkg: "mistral-vibe",
			path: func(root string) string {
				return filepath.Join(root, ".local", "pipx", "venvs", "mistral-vibe", "bin", "vibe")
			},
			wantArgv: []string{"pipx", "list", "--json"},
			output: func(_, binary string) string {
				pathJSON, _ := json.Marshal(binary)
				return `{"venvs":{"mistral-vibe":{"metadata":{"main_package":{"package":"mistral-vibe","app_paths":[{"__type__":"Path","__Path__":` + string(pathJSON) + `}],"package_version":"1.2.3"}}}}}`
			},
		},
		{
			name: "winget", method: "winget", pkg: "GitHub.Copilot",
			path: func(root string) string {
				return filepath.Join(root, "AppData", "Local", "Microsoft", "WinGet", "Packages", "GitHub.Copilot", "copilot.exe")
			},
			wantArgv: []string{"winget", "list", "--id", "GitHub.Copilot", "--exact", "--source", "winget", "--accept-source-agreements", "--disable-interactivity"},
			output:   func(_, _ string) string { return "GitHub Copilot  GitHub.Copilot  1.2.3  winget\n" },
		},
		{
			name: "pnpm", method: "pnpm", pkg: "@openai/codex",
			path: func(root string) string {
				return filepath.Join(root, "pnpm", "global", "5", "node_modules", "@openai", "codex", "bin", "codex")
			},
			wantArgv: []string{"pnpm", "root", "-g"},
			output:   func(root, _ string) string { return filepath.Join(root, "pnpm", "global", "5", "node_modules") + "\n" },
		},
		{
			name: "yarn", method: "yarn", pkg: "@openai/codex",
			path: func(root string) string {
				return filepath.Join(root, ".config", "yarn", "global", "node_modules", "@openai", "codex", "bin", "codex")
			},
			wantArgv: []string{"yarn", "global", "dir"},
			output:   func(root, _ string) string { return filepath.Join(root, ".config", "yarn", "global") + "\n" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			binary := tt.path(root)
			if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, stderr io.Writer) error {
				if !reflect.DeepEqual(argv, tt.wantArgv) {
					t.Fatalf("argv = %v, want %v", argv, tt.wantArgv)
				}
				// Managers print warnings to stderr; they must not corrupt parsed output.
				if _, err := io.WriteString(stderr, "warning: something unrelated\n"); err != nil {
					return err
				}
				_, err := io.WriteString(stdout, tt.output(root, binary))
				return err
			}))
			owned, err := owner(context.Background(), binary, tt.method, tt.pkg, false)
			if err != nil || !owned {
				t.Fatalf("owned=%t err=%v", owned, err)
			}
		})
	}
}

func TestManagerOwnsBinaryRejectsUnresolvedVersionManagerShim(t *testing.T) {
	called := false
	owner := managerOwnsBinary(commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	}))
	owned, err := owner(context.Background(), "/Users/me/.asdf/shims/codex", "npm", "@openai/codex", false)
	if err != nil || owned {
		t.Fatalf("owned=%t err=%v", owned, err)
	}
	if called {
		t.Fatal("manager command ran for an unresolved version-manager shim")
	}
}

func TestAdvisoryPackageSourcesAddsDetectedNodeManagerWithoutMakingItAnOperation(t *testing.T) {
	plans := []Plan{
		{Method: "npm", Package: "@openai/codex", PackagePrefix: "/npm-prefix"},
		{Method: "homebrew", Package: "codex", PackageCask: true},
		{Method: "winget", Package: "OpenAI.Codex"},
	}

	pnpm := advisoryPackageSources(plans, "npm", layoutPNPM)
	if len(pnpm) != 3 || pnpm[0].Method != "pnpm" || pnpm[0].Package != "@openai/codex" || pnpm[0].PackagePrefix != "/npm-prefix" {
		t.Fatalf("pnpm sources = %+v", pnpm)
	}
	yarn := advisoryPackageSources(plans, "", layoutYarn)
	if len(yarn) != 3 || yarn[0].Method != "yarn" || yarn[0].Package != "@openai/codex" {
		t.Fatalf("yarn sources = %+v", yarn)
	}
	brew := advisoryPackageSources(plans, "homebrew", "")
	if len(brew) != 3 || brew[0].Method != "homebrew" {
		t.Fatalf("preferred sources = %+v", brew)
	}
	if sources := advisoryPackageSources(plans, "", layoutVersionManager); len(sources) != 0 {
		t.Fatalf("version-manager sources = %+v, want none", sources)
	}
}

func TestManagerOwnsBinaryRejectsWrongExactPackage(t *testing.T) {
	owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		if argv[0] != "winget" {
			t.Fatalf("argv = %v", argv)
		}
		_, err := io.WriteString(stdout, "Other Tool  Vendor.Other  1.2.3  winget\n")
		return err
	}))
	owned, err := owner(context.Background(), "/Users/me/AppData/Local/Microsoft/WinGet/Packages/GitHub.Copilot/copilot.exe", "winget", "GitHub.Copilot", false)
	if err != nil || owned {
		t.Fatalf("owned=%t err=%v", owned, err)
	}
}

// writeNPMPackage lays out <prefix>/<modules>/<pkg> with a manifest and an
// entry point, and returns the entry point.
func writeNPMPackage(t *testing.T, modulesRoot, pkg string) string {
	t.Helper()
	root := filepath.Join(modulesRoot, filepath.FromSlash(pkg))
	entry := filepath.Join(root, "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"`+pkg+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return entry
}

func linkBinary(t *testing.T, link, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
}

func TestNPMPrefixOwningBinaryFindsPrefixesOutsideDaemonNPM(t *testing.T) {
	const pkg = "@earendil-works/pi-coding-agent"
	t.Run("nvm Node installation uses its own Node and npm", func(t *testing.T) {
		prefix := filepath.Join(t.TempDir(), ".nvm", "versions", "node", "v24.18.1")
		entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), pkg)
		bin := filepath.Join(prefix, "bin", "pi")
		linkBinary(t, bin, entry)
		node, cli := filepath.Join(prefix, "bin", "node"), filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")
		for _, file := range []string{node, cli} {
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		got, ok := npmPrefixOwningBinary(bin, pkg)
		if resolved, _ := filepath.EvalSymlinks(prefix); !ok || got != resolved {
			t.Fatalf("prefix = %q, %t; want %q", got, ok, resolved)
		}
		plan := targetInstalledCopy(runtime.GOOS, Plan{Method: "npm", Package: pkg, PackagePrefix: "/daemon/npm", Command: []string{"npm", "install", "-g", "--prefix", "/daemon/npm", pkg + "@latest"}}, TargetPi, bin)
		resolvedNode, _ := filepath.EvalSymlinks(node)
		resolvedCLI, _ := filepath.EvalSymlinks(cli)
		if want := []string{resolvedNode, resolvedCLI, "install", "-g", "--prefix", got, pkg + "@latest"}; !slices.Equal(plan.Command, want) || plan.PackagePrefix != got {
			t.Fatalf("command = %v prefix=%q, want %v", plan.Command, plan.PackagePrefix, want)
		}
	})
	t.Run("installer prefix without Node uses npm from PATH", func(t *testing.T) {
		prefix := filepath.Join(t.TempDir(), ".local")
		entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), pkg)
		bin := filepath.Join(prefix, "bin", "pi")
		linkBinary(t, bin, entry)
		got, ok := npmPrefixOwningBinary(bin, pkg)
		if !ok {
			t.Fatal("~/.local npm prefix was not recognised")
		}
		plan := targetInstalledCopy(runtime.GOOS, Plan{Method: "npm", Package: pkg, Command: []string{"npm", "install", "-g", "--prefix", "/daemon/npm", pkg + "@latest"}}, TargetPi, bin)
		if want := []string{"npm", "install", "-g", "--prefix", got, pkg + "@latest"}; !slices.Equal(plan.Command, want) {
			t.Fatalf("command = %v, want %v", plan.Command, want)
		}
	})
	t.Run("Windows npm cmd shim in the prefix", func(t *testing.T) {
		prefix := filepath.Join(t.TempDir(), "AppData", "Roaming", "npm")
		writeNPMPackage(t, filepath.Join(prefix, "node_modules"), pkg)
		shim := filepath.Join(prefix, "pi.cmd")
		body := "@ECHO off\r\nSET dp0=%~dp0\r\n\"%_prog%\"  \"%dp0%\\node_modules\\@earendil-works\\pi-coding-agent\\dist\\cli.js\" %*\r\n"
		if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		if got, ok := npmPrefixOwningBinary(shim, pkg); !ok || got != prefix {
			t.Fatalf("prefix = %q, %t; want %q", got, ok, prefix)
		}
	})
	t.Run("a different package is never claimed", func(t *testing.T) {
		prefix := filepath.Join(t.TempDir(), ".local")
		entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), "@earendil-works/pi-coding-agent")
		if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(entry)), "package.json"), []byte(`{"name":"someone-else"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(prefix, "bin", "pi")
		linkBinary(t, bin, entry)
		if got, ok := npmPrefixOwningBinary(bin, pkg); ok {
			t.Fatalf("claimed prefix %q for a package with another name", got)
		}
	})
	t.Run("another manager's node_modules is never claimed", func(t *testing.T) {
		for name, modulesRoot := range map[string]string{
			"bun":  filepath.Join(".bun", "install", "global", "node_modules"),
			"pnpm": filepath.Join("Library", "pnpm", "global", "5", "node_modules"),
			"yarn": filepath.Join(".config", "yarn", "global", "node_modules"),
		} {
			home := t.TempDir()
			entry := writeNPMPackage(t, filepath.Join(home, modulesRoot), pkg)
			bin := filepath.Join(home, "bin", "pi")
			linkBinary(t, bin, entry)
			if got, ok := npmPrefixOwningBinary(bin, pkg); ok {
				t.Fatalf("%s: npm claimed prefix %q", name, got)
			}
			plan := Plan{Method: "npm", Package: pkg, Command: []string{"npm", "install", "-g", "--prefix", "/daemon/npm", pkg + "@latest"}}
			if got := targetInstalledCopy(runtime.GOOS, plan, TargetPi, bin); !slices.Equal(got.Command, plan.Command) {
				t.Fatalf("%s: update retargeted to %v", name, got.Command)
			}
		}
	})
}

func TestManagerOwnsBinaryAcceptsNPMPrefixOtherThanDaemons(t *testing.T) {
	const pkg = "@earendil-works/pi-coding-agent"
	prefix := filepath.Join(t.TempDir(), ".nvm", "versions", "node", "v24.18.1")
	entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), pkg)
	bin := filepath.Join(prefix, "bin", "pi")
	linkBinary(t, bin, entry)
	owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
		// The daemon's npm belongs to a different Node.
		_, err := io.WriteString(stdout, filepath.Join(t.TempDir(), "other-node", "lib", "node_modules")+"\n")
		return err
	}))
	if owned, err := owner(context.Background(), bin, "npm", pkg, false); err != nil || !owned {
		t.Fatalf("owned=%t err=%v", owned, err)
	}
	if owned, _ := owner(context.Background(), bin, "npm", "@openai/codex", false); owned {
		t.Fatal("npm prefix fallback claimed another package")
	}
}

func TestVendorUpdateRunsFromTheBinarySessionsRun(t *testing.T) {
	plan := targetInstalledCopy("linux", Plan{Method: "official-installer", Command: []string{"grok", "update"}}, TargetGrok, "/home/u/.grok/downloads/grok-1.0.13")
	if want := []string{"/home/u/.grok/downloads/grok-1.0.13", "update"}; !slices.Equal(plan.Command, want) {
		t.Fatalf("command = %v, want %v", plan.Command, want)
	}
}

func TestNPMUpdateRequiresWritableOwningPrefix(t *testing.T) {
	s := newTestService("darwin", "npm")
	var checked []string
	writable := map[string]bool{"/daemon/npm": true, "/Users/u/.nvm/versions/node/v22.0.0": true}
	s.pathWritable = pathWritableProbeFunc(func(_ context.Context, path string) (bool, error) {
		checked = append(checked, path)
		return writable[path], nil
	})
	plan := Plan{Target: TargetCodex, Method: "npm", PackagePrefix: "/daemon/npm"}
	if got := s.validateNPMUpdateTarget(context.Background(), plan, "/daemon/npm"); got.Unsupported || len(checked) != 0 {
		t.Fatalf("daemon prefix re-checked: plan=%+v checked=%v", got, checked)
	}
	plan.PackagePrefix = "/Users/u/.nvm/versions/node/v22.0.0"
	if got := s.validateNPMUpdateTarget(context.Background(), plan, "/daemon/npm"); got.Unsupported {
		t.Fatalf("writable owning prefix rejected: %+v", got)
	}
	plan.PackagePrefix = "/usr/local"
	got := s.validateNPMUpdateTarget(context.Background(), plan, "/daemon/npm")
	if !got.Unsupported || !strings.Contains(got.Reason, "/usr/local") {
		t.Fatalf("unwritable owning prefix = %+v, want unsupported naming /usr/local", got)
	}
}

// The daemon's npm prefix being unwritable says nothing about the prefix that
// owns the installed binary; an update validates that one instead.
func TestNPMUpdateValidatesOwningPrefixNotDaemonPrefix(t *testing.T) {
	const pkg = "@openai/codex"
	setup := func(t *testing.T, ownerWritable bool, nodeVersion string) (*Service, *[][]string) {
		prefix := filepath.Join(t.TempDir(), ".nvm", "versions", "node", "v22.19.0")
		entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), pkg)
		bin := filepath.Join(prefix, "bin", "codex")
		linkBinary(t, bin, entry)
		for _, file := range []string{filepath.Join(prefix, "bin", "node"), filepath.Join(prefix, "lib", "node_modules", "npm", "bin", "npm-cli.js")} {
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		resolvedPrefix, _ := filepath.EvalSymlinks(prefix)
		s := newTestService("darwin", "npm")
		s.installCapabilities = installCapabilitiesStub{prefix: "/usr/local", writable: false}
		s.pathWritable = pathWritableProbeFunc(func(_ context.Context, path string) (bool, error) {
			return path == resolvedPrefix && ownerWritable, nil
		})
		s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
			return VerifyResult{ResolvedPath: bin, Output: "codex-cli 1.2.0"}, nil
		})
		s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return true, nil }
		ran := &[][]string{}
		s.commands = commandRunnerFunc(func(_ context.Context, argv []string, stdout, stderr io.Writer) error {
			*ran = append(*ran, argv)
			if argv[len(argv)-1] == "--version" {
				// npm prints config warnings before the version; they are not the version.
				if _, err := io.WriteString(stderr, "npm warn config deprecated-key is deprecated\n"); err != nil {
					return err
				}
			}
			if len(argv) == 2 && argv[1] == "--version" {
				_, err := io.WriteString(stdout, nodeVersion+"\n")
				return err
			}
			if len(argv) == 3 && argv[2] == "--version" {
				_, err := io.WriteString(stdout, "10.0.0\n")
				return err
			}
			return nil
		})
		return s, ran
	}

	s, ran := setup(t, true, "v22.19.0")
	job, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, "")
	if err != nil {
		t.Fatalf("update rejected for the daemon's prefix: %v", err)
	}
	if job.Status == StatusUnsupported {
		t.Fatalf("job = %+v", job)
	}
	s.workers.Wait()
	if len(*ran) < 2 || !slices.Contains((*ran)[len(*ran)-1], "install") || !slices.Contains((*ran)[len(*ran)-1], "--prefix") {
		t.Fatalf("ran %v, want an install into the owning prefix", *ran)
	}

	for name, tc := range map[string]struct {
		writable bool
		node     string
		reason   string
	}{
		"owning prefix unwritable": {false, "v22.19.0", "not writable"},
		"owning Node too old":      {true, "v14.0.0", "Node.js"},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := setup(t, tc.writable, tc.node)
			job, err := s.StartAgentOperation(context.Background(), TargetCodex, "npm", AgentOperationUpdate, "")
			if err != nil {
				t.Fatal(err)
			}
			if job.Status != StatusUnsupported || !strings.Contains(job.Error, tc.reason) {
				t.Fatalf("job = %+v, want unsupported mentioning %q", job, tc.reason)
			}
		})
	}
}
