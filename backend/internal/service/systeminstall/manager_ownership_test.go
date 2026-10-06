package systeminstall

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNodeShimTargetsPackage(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
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

func TestManagerOwnsBinaryAcceptsWindowsNPMShimForExpectedPackageOnly(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@openai", "codex")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
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
			output: func(_, _ string) string {
				return `{"venvs":{"mistral-vibe":{"metadata":{"main_package":{"package":"mistral-vibe","package_version":"1.2.3"}}}}}`
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
			owner := managerOwnsBinary(commandRunnerFunc(func(_ context.Context, argv []string, stdout, _ io.Writer) error {
				if !reflect.DeepEqual(argv, tt.wantArgv) {
					t.Fatalf("argv = %v, want %v", argv, tt.wantArgv)
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
