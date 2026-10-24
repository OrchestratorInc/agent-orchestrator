package modelcatalog

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

func TestDiscoveryExecutableRejectsForeignAgent(t *testing.T) {
	for _, tc := range []struct {
		name, agent, filename, body string
	}{
		{"grok overwritten generic alias", "grok", "agent.cmd", "@echo off\n\"C:\\Users\\user\\AppData\\Local\\autohand\\autohand.exe\" %*\nexit /b %ERRORLEVEL%\n"},
		{"cursor overwritten generic alias", "cursor", "agent.cmd", "@echo off\n\"C:\\Users\\user\\AppData\\Local\\autohand\\autohand.exe\" %*\n"},
		{"cursor misleading filename", "cursor", "cursor-agent.cmd", "@echo off\n\"%~dp0autohand.exe\" %*\n"},
		{"grok batch wrapper", "grok", "grok.bat", "@call \"%~dp0autohand.exe\" %*\n"},
		{"extensionless batch call", "grok", "grok.cmd", "@call autohand %*\n"},
		{"quoted extensionless batch call", "grok", "grok.cmd", "@CALL \"autohand\" %*\n"},
		{"extensionless powershell invocation", "grok", "grok.ps1", "& autohand @args\n"},
		{"quoted extensionless powershell invocation", "grok", "grok.ps1", "&'autohand' @args\n"},
		{"extensionless powershell dot invocation", "grok", "grok.ps1", ". autohand @args\n"},
		{"extensionless quoted path", "grok", "grok.ps1", "& 'C:/path with spaces/autohand' @args\n"},
		{"powershell wrapper", "grok", "grok.ps1", "& \"$PSScriptRoot/autohand.exe\" $args\n"},
		{"posix wrapper", "grok", "grok.cmd", "#!/bin/sh\nexec autohand \"$@\"\n"},
		{"npm wrapper", "cursor", "cursor-agent.cmd", "@\"node.exe\" \"%~dp0/node_modules/autohand-cli/dist/cli.js\" %*\n"},
		{"direct foreign binary", "grok", "autohand.exe", "MZ fixture"},
		{"different foreign agent", "grok", "grok.cmd", "@\"%~dp0cursor-agent.exe\" %*\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDiscoveryLauncher(t, t.TempDir(), tc.filename, tc.body)
			_, err := verifyDiscoveryExecutable(context.Background(), tc.agent, path)
			if err == nil || !strings.Contains(err.Error(), "instead of "+tc.agent) {
				t.Fatalf("verification = %v, want a foreign-agent rejection", err)
			}
		})
	}
}

func TestDiscoveryExecutableAllowsAgentOwnedLaunchers(t *testing.T) {
	for _, tc := range []struct {
		name, agent, filename, body string
	}{
		{"native grok", "grok", "grok.exe", "MZ fixture"},
		{"versioned grok", "grok", "grok-1.0.13.exe", "MZ fixture"},
		{"platform grok", "grok", "grok-windows-x86_64.exe", "MZ fixture"},
		{"native cursor", "cursor", "cursor-agent.exe", "MZ fixture"},
		{"grok alias", "grok", "agent.cmd", "@echo off\n\"%~dp0grok.exe\" %*\n"},
		{"extensionless batch call", "grok", "agent.cmd", "@call grok %*\n"},
		{"extensionless powershell invocation", "grok", "agent.ps1", "& 'grok' @args\n"},
		{"cursor launcher", "cursor", "cursor-agent.cmd", "@powershell.exe -File \"%~dp0cursor-agent.ps1\" %*\n"},
		{"npm shim", "grok", "grok.cmd", "@\"node.exe\" \"%~dp0/node_modules/grok/dist/cli.js\" %*\n"},
		{"opencode shared v2 binary", "opencode-v2", "opencode.exe", "MZ fixture"},
		{"command code windows", "command-code", "cmdc.exe", "MZ fixture"},
		{"comments are not redirects", "grok", "grok.cmd", "@echo off\nREM replaced autohand.exe\n:: autohand.exe\n# autohand.exe\n\"%~dp0grok.exe\" %*\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeDiscoveryLauncher(t, t.TempDir(), tc.filename, tc.body)
			verified, err := verifyDiscoveryExecutable(context.Background(), tc.agent, path)
			if err != nil || verified != path {
				t.Fatalf("verification = %q, %v, want %q", verified, err, path)
			}
		})
	}
}

func TestDiscoveryExecutableRejectsUnverifiableRedirect(t *testing.T) {
	for _, invocation := range []string{
		"@call bridge %*", "& bridge @args", ". 'bridge' @args", "exec bridge \"$@\"",
		"@call bridge.bat %*", "@call bridge.cmd %*", "& bridge.ps1 @args", "exec bridge.sh \"$@\"",
		"@bridge.bat %*", "@bridge.cmd %*", "& './bridge.ps1' @args",
		"@call \"%~dp0missing.bat\" %*",
	} {
		t.Run(invocation, func(t *testing.T) {
			root := t.TempDir()
			// Windows resolves bridge.cmd using PATHEXT; POSIX resolves bridge.
			bridge := "bridge"
			if runtime.GOOS == "windows" {
				bridge += ".cmd"
				t.Setenv("PATHEXT", ".CMD;.EXE")
			}
			writeDiscoveryLauncher(t, root, bridge, "@call autohand %*\n")
			for _, name := range []string{"bridge.bat", "bridge.cmd", "bridge.ps1", "bridge.sh"} {
				writeDiscoveryLauncher(t, root, name, "@call autohand %*\n")
			}
			t.Setenv("PATH", root)
			launcher := writeDiscoveryLauncher(t, root, "grok.cmd", invocation+"\n")
			if _, err := verifyDiscoveryExecutable(context.Background(), "grok", launcher); err == nil || !strings.Contains(err.Error(), "cannot verify launcher command") {
				t.Fatalf("verification = %v, want unverifiable intermediate launcher rejected", err)
			}
		})
	}
}

func TestDiscoveryExecutableInspectsNestedLaunchers(t *testing.T) {
	t.Run("inspectable same-agent intermediate", func(t *testing.T) {
		root := t.TempDir()
		launcher := writeDiscoveryLauncher(t, root, "grok.cmd", "@call \"%~dp0bridge.bat\" %*\n")
		writeDiscoveryLauncher(t, root, "bridge.bat", "@\"%~dp0grok.exe\" %*\n")
		writeDiscoveryLauncher(t, root, "grok.exe", "MZ fixture")
		if _, err := verifyDiscoveryExecutable(context.Background(), "grok", launcher); err != nil {
			t.Fatalf("inspectable same-agent launcher rejected: %v", err)
		}
	})
	for _, agent := range []string{"grok", "cursor"} {
		t.Run(agent, func(t *testing.T) {
			root := t.TempDir()
			launcher := writeDiscoveryLauncher(t, root, "agent.cmd", "@call \"%~dp0bridge.cmd\" %*\n")
			writeDiscoveryLauncher(t, root, "bridge.cmd", "@\"%~dp0autohand.exe\" %*\n")
			if _, err := verifyDiscoveryExecutable(context.Background(), agent, launcher); err == nil || !strings.Contains(err.Error(), "autohand") {
				t.Fatalf("verification = %v, want nested redirect rejected", err)
			}
		})
	}
	root := t.TempDir()
	launcher := writeDiscoveryLauncher(t, root, "cursor-agent/agent.cmd", "@powershell.exe -File \"%SCRIPT_DIR%/agent.ps1\" %*\n")
	child := writeDiscoveryLauncher(t, root, "cursor-agent/agent.ps1", "$scriptPath = Split-Path -parent $MyInvocation.MyCommand.Definition\n& \"$scriptPath/versions/$versionName/node.exe\" \"$scriptPath/versions/$versionName/index.js\" $args\n")
	if _, err := verifyDiscoveryExecutable(context.Background(), "cursor", launcher); err != nil {
		t.Fatalf("native Cursor launcher rejected: %v", err)
	}
	if err := os.WriteFile(child, []byte("& \"$PSScriptRoot/autohand.exe\" $args\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyDiscoveryExecutable(context.Background(), "cursor", launcher); err == nil || !strings.Contains(err.Error(), "autohand") {
		t.Fatalf("overwritten Cursor PowerShell launcher accepted: %v", err)
	}
}

func TestDiscoveryExecutableRejectsAmbiguousAliasAndCycles(t *testing.T) {
	root := t.TempDir()
	ambiguous := writeDiscoveryLauncher(t, root, "agent.cmd", "@echo unknown agent\n")
	if _, err := verifyDiscoveryExecutable(context.Background(), "cursor", ambiguous); err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("ambiguous alias accepted: %v", err)
	}
	cycle := writeDiscoveryLauncher(t, root, "grok.cmd", "@call \"%~dp0bridge.cmd\" %*\n")
	writeDiscoveryLauncher(t, root, "bridge.cmd", "@call \"%~dp0grok.cmd\" %*\n")
	if _, err := verifyDiscoveryExecutable(context.Background(), "grok", cycle); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cyclic launcher accepted: %v", err)
	}
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
			_, err := verifyDiscoveryExecutable(context.Background(), "grok", launcher)
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
	// A quoted command matches both reference extractors; repeated lines and
	// branches must also reuse completed results rather than reread each file.
	shared := strings.Repeat("\"%~dp0grok.exe\" %*\n", discoveryLauncherFileLimit+1)
	writeDiscoveryLauncher(t, root, "grok.exe", "MZ fixture")
	writeDiscoveryLauncher(t, root, "left.cmd", shared)
	writeDiscoveryLauncher(t, root, "right.cmd", shared)
	launcher := writeDiscoveryLauncher(t, root, "grok.cmd", "@call \"%~dp0left.cmd\" %*\n@call \"%~dp0right.cmd\" %*\n")
	if _, err := verifyDiscoveryExecutable(context.Background(), "grok", launcher); err != nil {
		t.Fatalf("repeated shared targets rejected: %v", err)
	}
}

func TestDiscoveryExecutableInspectsQuotedPathAndUTF16(t *testing.T) {
	root := t.TempDir()
	launcher := writeDiscoveryLauncher(t, root, "grok.cmd", "@call \"%~dp0path with spaces/bridge.cmd\" %*\n")
	writeDiscoveryLauncher(t, root, "path with spaces/bridge.cmd", "@\"%~dp0autohand.exe\" %*\n")
	if _, err := verifyDiscoveryExecutable(context.Background(), "grok", launcher); err == nil || !strings.Contains(err.Error(), "autohand") {
		t.Fatalf("quoted redirect accepted: %v", err)
	}
	units := utf16.Encode([]rune("& \"$PSScriptRoot/autohand.exe\" $args\n"))
	body := make([]byte, 2+len(units)*2)
	copy(body, []byte{0xff, 0xfe})
	for i, unit := range units {
		binary.LittleEndian.PutUint16(body[2+i*2:], unit)
	}
	launcher = writeDiscoveryLauncher(t, root, "cursor-agent.ps1", string(body))
	if _, err := verifyDiscoveryExecutable(context.Background(), "cursor", launcher); err == nil || !strings.Contains(err.Error(), "autohand") {
		t.Fatalf("UTF-16 redirect accepted: %v", err)
	}
}

func TestDiscoveryExecutableInspectsSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	target := writeDiscoveryLauncher(t, root, "autohand.exe", "MZ fixture")
	link := filepath.Join(root, "grok.exe")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := verifyDiscoveryExecutable(context.Background(), "grok", link); err == nil || !strings.Contains(err.Error(), "autohand") {
		t.Fatalf("foreign symlink target accepted: %v", err)
	}
}

func TestDiscoveryExecutableAllowsPackageSymlink(t *testing.T) {
	root := t.TempDir()
	target := writeDiscoveryLauncher(t, root, "node_modules/@earendil-works/pi-coding-agent/dist/cli.js", strings.Repeat("// application code\n", 5000))
	link := filepath.Join(root, "pi")
	if runtime.GOOS == "windows" {
		link += ".cmd"
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
	verified, err := verifyDiscoveryExecutable(context.Background(), "pi", link)
	if err != nil || verified != link {
		t.Fatalf("package symlink verification = %q, %v, want launcher %q", verified, err, link)
	}
}

func TestDiscoveryPreservesSymlinkInvocationName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a POSIX shell script")
	}
	root := t.TempDir()
	target := writeDiscoveryLauncher(t, root, "opencode", "#!/bin/sh\nprintf 'anthropic/%s\\n' \"${0##*/}\"\n")
	link := filepath.Join(root, "opencode2")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	verified, err := verifyDiscoveryExecutable(context.Background(), "opencode-v2", "opencode2")
	if err != nil || verified != link {
		t.Fatalf("verification = %q, %v, want PATH launcher %q", verified, err, link)
	}
	catalog, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{
		AgentID: "opencode-v2", Binary: "opencode2", WorkingDir: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		if model.ID == "anthropic/opencode2" {
			return
		}
	}
	t.Fatalf("discovery did not preserve symlink invocation name: %#v", catalog.Models)
}

func TestDiscoveryUsesVerifiedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI is a POSIX shell script")
	}
	root := t.TempDir()
	path := writeDiscoveryLauncher(t, root, "opencode", "#!/bin/sh\nprintf 'anthropic/guard-test-model\\n'\n")
	request := ports.AgentModelDiscoveryRequest{AgentID: "opencode", Binary: path, WorkingDir: root}
	catalog, err := (Discoverer{}).Discover(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		if model.ID == "anthropic/guard-test-model" {
			return
		}
	}
	t.Fatalf("verified model command returned no fixture model: %#v", catalog.Models)
}

func TestDiscoveryExecutableHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifyDiscoveryExecutable(ctx, "grok", "grok"); !errors.Is(err, context.Canceled) {
		t.Fatalf("verification = %v, want cancellation", err)
	}
}

func TestDiscoveryExecutableChecksPATHAlias(t *testing.T) {
	root := t.TempDir()
	name := "agent"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	writeDiscoveryLauncher(t, root, name, "#!/bin/sh\nexec autohand \"$@\"\n")
	t.Setenv("PATH", root)
	for _, agent := range []string{"grok", "cursor"} {
		if _, err := verifyDiscoveryExecutable(context.Background(), agent, "agent"); err == nil || !strings.Contains(err.Error(), "autohand") {
			t.Fatalf("%s PATH alias verification = %v, want foreign target rejected", agent, err)
		}
	}
}

func TestDiscoveryRejectsRedirectBeforeAnyProbe(t *testing.T) {
	previous := signInProbe
	t.Cleanup(func() { signInProbe = previous })
	signInProbe = func(context.Context, string, []string, string, map[string]string) ([]byte, error) {
		t.Fatal("authentication probe ran through a foreign launcher")
		return nil, nil
	}
	for _, agent := range []string{"grok", "cursor", "kiro", "fx"} {
		t.Run(agent, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "login-started")
			launcher := writeDiscoveryLauncher(t, root, "agent.cmd", "@echo login > \""+marker+"\"\n\"%~dp0autohand.exe\" %*\n")
			_, err := (Discoverer{}).Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: agent, Binary: launcher})
			if err == nil || !strings.Contains(err.Error(), "autohand") {
				t.Fatalf("discovery = %v, want launcher rejected before execution", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("launcher ran before rejection: marker stat = %v", err)
			}
		})
	}
}
