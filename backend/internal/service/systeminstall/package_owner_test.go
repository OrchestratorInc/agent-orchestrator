package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeFileAt(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeriveOwnerFromPathReadsEachManagersLayout(t *testing.T) {
	root := t.TempDir()
	shimPrefix := filepath.Join(root, "AppData", "Roaming", "npm")
	writeFileAt(t, filepath.Join(shimPrefix, "node_modules", "@google", "gemini-cli", "dist", "index.js"), "")
	shim := writeFileAt(t, filepath.Join(shimPrefix, "gemini.cmd"), "@ECHO off\r\n\"%_prog%\"  \"%dp0%\\node_modules\\@google\\gemini-cli\\dist\\index.js\" %*\r\n")
	for _, tt := range []struct {
		name string
		path string
		want derivedOwner
		ok   bool
	}{
		{"homebrew formula", writeFileAt(t, filepath.Join(root, "homebrew", "Cellar", "block-goose-cli", "1.48.0", "bin", "goose"), ""), derivedOwner{method: "homebrew", pkg: "block-goose-cli"}, true},
		{"homebrew cask", writeFileAt(t, filepath.Join(root, "homebrew", "Caskroom", "devin-cli", "3000.6.11", "bin", "devin"), ""), derivedOwner{method: "homebrew", pkg: "devin-cli", cask: true}, true},
		{"scoped npm package", writeFileAt(t, filepath.Join(root, "node", "lib", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js"), ""), derivedOwner{method: "npm", pkg: "@earendil-works/pi-coding-agent"}, true},
		{"npm package", writeFileAt(t, filepath.Join(root, "node", "lib", "node_modules", "prime-agent", "dist", "cli.js"), ""), derivedOwner{method: "npm", pkg: "prime-agent"}, true},
		{"Windows npm shim", shim, derivedOwner{method: "npm", pkg: "@google/gemini-cli"}, true},
		{"uv tool", writeFileAt(t, filepath.Join(root, ".local", "share", "uv", "tools", "aider-chat", "bin", "aider"), ""), derivedOwner{method: "uv", pkg: "aider-chat"}, true},
		{"pipx venv", writeFileAt(t, filepath.Join(root, ".local", "pipx", "venvs", "mistral-vibe", "bin", "vibe"), ""), derivedOwner{method: "pipx", pkg: "mistral-vibe"}, true},
		{"pnpm keeps node_modules too", writeFileAt(t, filepath.Join(root, "pnpm", "global", "5", "node_modules", "@openai", "codex", "bin", "codex.js"), ""), derivedOwner{}, false},
		{"version manager shim", writeFileAt(t, filepath.Join(root, ".asdf", "shims", "codex"), "#!/bin/sh\n"), derivedOwner{}, false},
		{"vendor directory", writeFileAt(t, filepath.Join(root, ".grok", "downloads", "grok-1.0.13"), ""), derivedOwner{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := deriveOwnerFromPath(tt.path)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("owner = %+v, %t; want %+v, %t", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestUpdateAdvisoryMaintainsConfirmedOwnerMissingFromHarnessMethods(t *testing.T) {
	binary := writeFileAt(t, filepath.Join(t.TempDir(), "homebrew", "Cellar", "block-goose-cli", "1.48.0", "bin", "goose"), "")
	s := newTestService("darwin", "brew", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: binary, Output: "1.48.0"}, nil
	})
	s.ownsInstallation = func(_ context.Context, _ string, method, pkg string, cask bool) (bool, error) {
		return method == "homebrew" && pkg == "block-goose-cli" && !cask, nil
	}
	s.managedVersion = fixedManagedVersion("1.53.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) {
		t.Fatal("a package-owned binary was compared with the vendor channel")
		return "", nil
	}
	advisory, err := s.UpdateAdvisory(context.Background(), TargetGoose)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != "homebrew" || advisory.MaintenanceMethod != "homebrew" {
		t.Fatalf("advisory = %+v", advisory)
	}
	plans, err := s.AgentPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.AgentID != string(TargetGoose) {
			continue
		}
		for _, method := range plan.Methods {
			if method.ID == "homebrew" {
				if !method.UpdateAvailable || method.UpdateCommand != "brew upgrade block-goose-cli" || !method.UninstallAvailable || method.UninstallCommand != "brew uninstall block-goose-cli" {
					t.Fatalf("Goose homebrew method = %+v", method)
				}
				return
			}
		}
		t.Fatalf("Goose methods %+v do not list the confirmed Homebrew owner", plan.Methods)
	}
	t.Fatal("Goose plan missing")
}

func TestUpdateAdvisoryIgnoresDerivedOwnerTheManagerDoesNotConfirm(t *testing.T) {
	binary := writeFileAt(t, filepath.Join(t.TempDir(), "homebrew", "Cellar", "block-goose-cli", "1.48.0", "bin", "goose"), "")
	s := newTestService("darwin", "brew", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: binary, Output: "1.48.0"}, nil
	})
	s.ownsInstallation = func(context.Context, string, string, string, bool) (bool, error) { return false, nil }
	s.managedVersion = fixedManagedVersion("1.53.0", nil)
	s.officialVersion = func(context.Context, Target) (string, error) { return "1.53.0", nil }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetGoose)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.MaintenanceMethod != "" || advisory.Source == "homebrew" {
		t.Fatalf("unconfirmed path claim became a maintenance method: %+v", advisory)
	}
	if _, ok := s.derivedOwnerFor(TargetGoose); ok {
		t.Fatal("unconfirmed owner was kept")
	}
}

func TestNPMInstallerHarnessUpdatesThroughVendorCommand(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), ".nvm", "versions", "node", "v24.18.1")
	entry := writeNPMPackage(t, filepath.Join(prefix, "lib", "node_modules"), "prime-agent")
	bin := filepath.Join(prefix, "bin", "prime-agent")
	linkBinary(t, bin, entry)
	s := newTestService("darwin", "sh", "npm")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: bin, Output: "0.9.1"}, nil
	})
	s.ownsInstallation = func(_ context.Context, path, method, pkg string, cask bool) (bool, error) {
		return managerOwnsBinary(commandRunnerFunc(func(context.Context, []string, io.Writer, io.Writer) error { return errors.New("no npm") }))(context.Background(), path, method, pkg, cask)
	}
	s.managedVersion = func(context.Context, Plan, updateVersion) (managedVersionResult, error) {
		t.Fatal("Prime Agent's tarball install was looked up in the npm registry")
		return managedVersionResult{}, nil
	}
	s.officialVersion = func(context.Context, Target) (string, error) { return "0.9.8", nil }
	advisory, err := s.UpdateAdvisory(context.Background(), TargetPrimeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if advisory.Status != UpdateStatusBehindLatest || advisory.Source != officialReleaseSource || advisory.MaintenanceMethod != "official-installer" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if !s.methodOwnsBinary(context.Background(), Plan{Target: TargetPrimeAgent, Method: "official-installer"}, bin) {
		t.Fatal("Prime Agent's installer was refused ownership of its own npm layout")
	}
	if s.methodOwnsBinary(context.Background(), Plan{Target: TargetGrok, Method: "official-installer"}, bin) {
		t.Fatal("another harness's installer claimed an npm layout")
	}
}
