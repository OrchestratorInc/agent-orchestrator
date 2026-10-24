package binaryutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeDiscoveryLauncher(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoveryExecutableLimitsTotalFiles(t *testing.T) {
	for _, files := range []int{discoveryLauncherFileLimit, discoveryLauncherFileLimit + 1} {
		t.Run(fmt.Sprint(files), func(t *testing.T) {
			root := t.TempDir()
			var body strings.Builder
			for i := 1; i < files; i++ {
				name := fmt.Sprintf("bridge-%d.cmd", i)
				writeDiscoveryLauncher(t, root, name, "@echo off\n")
				fmt.Fprintf(&body, "@call \"%%~dp0%s\" %%*\n", name)
			}
			launcher := writeDiscoveryLauncher(t, root, "grok.cmd", body.String())
			_, err := VerifyExecutable(context.Background(), "grok", launcher)
			if files <= discoveryLauncherFileLimit {
				if err != nil {
					t.Fatalf("launcher within file budget rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "total file inspection limit") {
				t.Fatalf("verification = %v, want total file limit rejection", err)
			}
		})
	}
}

func TestDiscoveryExecutableReusesSharedTargets(t *testing.T) {
	root := t.TempDir()
	// Repeated quoted commands and branches must reuse completed results rather
	// than reread each file.
	shared := strings.Repeat("\"%~dp0grok.exe\" %*\n", discoveryLauncherFileLimit+1)
	writeDiscoveryLauncher(t, root, "grok.exe", "MZ fixture")
	writeDiscoveryLauncher(t, root, "left.cmd", shared)
	writeDiscoveryLauncher(t, root, "right.cmd", shared)
	launcher := writeDiscoveryLauncher(t, root, "grok.cmd", "@call \"%~dp0left.cmd\" %*\n@call \"%~dp0right.cmd\" %*\n")
	if _, err := VerifyExecutable(context.Background(), "grok", launcher); err != nil {
		t.Fatalf("repeated shared targets rejected: %v", err)
	}
}

func TestDiscoveryExecutableAllowsNativeManagerSymlinks(t *testing.T) {
	for _, tc := range []struct{ name, target, body string }{
		{"mise", ".local/bin/mise", "\x7fELF\x00fixture"},
		{"volta", ".volta/bin/volta-shim", "\xcf\xfa\xed\xfe\x00fixture"},
		{"renamed native", "releases/release-9f31b7", "MZ fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := writeDiscoveryLauncher(t, root, tc.target, tc.body)
			name := "grok"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			link := filepath.Join(root, name)
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				t.Fatal(err)
			}
			verified, err := VerifyExecutable(context.Background(), "grok", link)
			if err != nil || verified != link {
				t.Fatalf("native manager verification = %q, %v, want launcher %q", verified, err, link)
			}
			if _, err := VerifyExecutable(context.Background(), "grok", target); err == nil {
				t.Fatal("unidentified native target accepted without an agent entry point")
			}
			inspection := &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}
			if ok, err := inspectDiscoveryExecutable(context.Background(), "grok", link, inspection); err != nil || !ok {
				t.Fatalf("agent alias rejected: %v", err)
			}
			if ok, err := inspectDiscoveryExecutable(context.Background(), "grok", target, inspection); err != nil || ok {
				t.Fatalf("cached agent alias identity leaked to unidentified target: %t, %v", ok, err)
			}
		})
	}
}

func linkDiscoveryLauncher(t *testing.T, target, link string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("extensionless npm bins and POSIX shims are not Windows launchers")
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestDiscoveryExecutableAllowsNodeShebangPackageBins(t *testing.T) {
	// Shaped like the published @kilocode/cli, @mimo-ai/cli and @factory/cli
	// bins: extensionless node scripts whose `//` comments are not commands.
	body := "#!/usr/bin/env node\n\nconst childProcess = require(\"child_process\")\n//\n// kilocode_change start\n  configureTreeSitterResources(target) // kilocode_change\n"
	for _, tc := range []struct{ agent, pkg, bin string }{
		{"kilocode", "@kilocode/cli", "kilo"},
		{"mimo-code", "@mimo-ai/cli", "mimo"},
		{"droid", "@factory/cli", "droid"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			root := t.TempDir()
			target := writeDiscoveryLauncher(t, root, filepath.Join("lib/node_modules", tc.pkg, "bin", tc.bin), body)
			link := linkDiscoveryLauncher(t, target, filepath.Join(root, tc.bin))
			if verified, err := VerifyExecutable(context.Background(), tc.agent, link); err != nil || verified != link {
				t.Fatalf("node package bin verification = %q, %v, want launcher %q", verified, err, link)
			}
		})
	}
}

func TestDiscoveryExecutableIdentifiesScopedPackage(t *testing.T) {
	root := t.TempDir()
	// OMP publishes as @oh-my-pi/pi-coding-agent; its bare package segment
	// must not identify it as Pi.
	target := writeDiscoveryLauncher(t, root, "install/global/node_modules/@oh-my-pi/pi-coding-agent/dist/cli.js", "#!/usr/bin/env bun\n// application code\n")
	shim := writeDiscoveryLauncher(t, root, "omp.cmd", "@\"%~dp0\\node.exe\" \"%~dp0\\install\\global\\node_modules\\@oh-my-pi\\pi-coding-agent\\dist\\cli.js\" %*\n")
	if verified, err := VerifyExecutable(context.Background(), "omp", shim); err != nil || verified != shim {
		t.Fatalf("omp npm shim verification = %q, %v, want launcher %q", verified, err, shim)
	}
	if _, err := VerifyExecutable(context.Background(), "pi", shim); err == nil || !strings.Contains(err.Error(), "omp instead of pi") {
		t.Fatalf("pi verification of omp shim = %v, want omp identified", err)
	}
	link := linkDiscoveryLauncher(t, target, filepath.Join(root, "omp"))
	if verified, err := VerifyExecutable(context.Background(), "omp", link); err != nil || verified != link {
		t.Fatalf("omp package symlink verification = %q, %v, want launcher %q", verified, err, link)
	}
}

func TestDiscoveryExecutableAllowsVersionManagerShims(t *testing.T) {
	// Rendered from asdf 0.16+, pyenv and nodenv shim templates. The manager's
	// own dispatcher resolves the named program and is not inspected.
	pyenv := "#!/usr/bin/env bash\nset -e\n[ -n \"$PYENV_DEBUG\" ] && set -x\n\nprogram=\"${0##*/}\"\n\nexport PYENV_ROOT=\"/home/user/.pyenv\"\nSHIM_PATH=${0%/*}\nif [[ $SHIM_PATH != \"/home/user/.pyenv/shims\" ]]; then\n  export _PYENV_SHIM_PATH=\"$SHIM_PATH\"\nfi\nexec \"/opt/homebrew/opt/pyenv/libexec/pyenv\" exec \"$program\" \"$@\"\n"
	nodenv := "#!/usr/bin/env bash\nset -e\n[ -n \"$NODENV_DEBUG\" ] && set -x\n\nprogram=\"${0##*/}\"\nif [ \"$program\" = \"node\" ]; then\n  for arg; do\n    case \"$arg\" in\n    -e* | -- ) break ;;\n    */* )\n      if [ -f \"$arg\" ]; then\n        export NODENV_DIR=\"${arg%/*}\"\n        break\n      fi\n      ;;\n    esac\n  done\nfi\n\nexport NODENV_ROOT=\"/home/user/.nodenv\"\nexec \"/usr/local/opt/nodenv/libexec/nodenv\" exec \"$program\" \"$@\"\n"
	for _, tc := range []struct{ name, agent, bin, body string }{
		{"asdf", "opencode", "opencode", "#!/usr/bin/env bash\n# asdf-plugin: nodejs 22.11.0\nexec asdf exec \"opencode\" \"$@\" # asdf_allow: ' asdf '\n"},
		{"asdf claude", "claude-code", "claude", "#!/usr/bin/env bash\nexec asdf exec \"claude\" \"$@\"\n"},
		{"mise file shim", "codex", "codex", "#!/bin/sh\nexec mise x node@22 -- codex \"$@\"\n"},
		{"pyenv", "aider", "aider", pyenv},
		{"nodenv", "codex", "codex", nodenv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.bin
			if runtime.GOOS == "windows" {
				name += ".cmd"
			}
			shim := writeDiscoveryLauncher(t, t.TempDir(), name, tc.body)
			if verified, err := VerifyExecutable(context.Background(), tc.agent, shim); err != nil || verified != shim {
				t.Fatalf("version-manager shim verification = %q, %v, want launcher %q", verified, err, shim)
			}
		})
	}
	shim := writeDiscoveryLauncher(t, t.TempDir(), "grok.cmd", "#!/usr/bin/env bash\nexec asdf exec \"autohand\" \"$@\"\n")
	if _, err := VerifyExecutable(context.Background(), "grok", shim); err == nil || !strings.Contains(err.Error(), "autohand instead of grok") {
		t.Fatalf("foreign version-manager program verification = %v, want Autohand rejected", err)
	}
}
