package systeminstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// nativeClaude lays out the native installer's files under home and returns
// the launcher.
func nativeClaude(t *testing.T, home string) string {
	t.Helper()
	version := writeFileAt(t, filepath.Join(home, ".local", "share", "claude", "versions", "2.1.294"), "")
	launcher := filepath.Join(home, ".local", "bin", "claude")
	linkBinary(t, launcher, version)
	writeFileAt(t, filepath.Join(home, ".claude", "settings.json"), "{}")
	writeFileAt(t, filepath.Join(home, ".claude.json"), "{}")
	return launcher
}

func TestClaudeNativeRemovalMatchesDocumentedLayoutOnly(t *testing.T) {
	home := t.TempDir()
	launcher := nativeClaude(t, home)
	want := []string{launcher, filepath.Join(home, ".local", "share", "claude")}
	if got := vendorRemovalPaths(TargetClaudeCode, home, "darwin", launcher); !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	elsewhere := writeFileAt(t, filepath.Join(home, "opt", "bin", "claude"), "")
	if got := vendorRemovalPaths(TargetClaudeCode, home, "darwin", elsewhere); got != nil {
		t.Fatalf("a copy outside the native layout got removal paths %v", got)
	}
	if got := vendorRemovalPaths(TargetGrok, home, "darwin", launcher); got != nil {
		t.Fatalf("a vendor without documented removal got %v", got)
	}
}

func TestRemoveProgramFilesKeepsUserData(t *testing.T) {
	home := t.TempDir()
	launcher := nativeClaude(t, home)
	var out bytes.Buffer
	if err := removeProgramFiles(vendorRemovalPaths(TargetClaudeCode, home, "darwin", launcher), &out); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{launcher, filepath.Join(home, ".local", "share", "claude")} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", gone)
		}
	}
	for _, kept := range []string{filepath.Join(home, ".claude", "settings.json"), filepath.Join(home, ".claude.json")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("user data %s was removed: %v", kept, err)
		}
	}
	if !strings.Contains(out.String(), "Removed "+launcher) {
		t.Fatalf("output = %q", out.String())
	}
	if err := removeProgramFiles([]string{launcher}, &out); err != nil {
		t.Fatalf("removing an already removed path: %v", err)
	}
}

func TestClaudeNativeUninstallRunsDocumentedRemoval(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	launcher := nativeClaude(t, home)
	s := newTestService("darwin", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		if _, err := os.Lstat(launcher); err != nil {
			return VerifyResult{}, err
		}
		return VerifyResult{ResolvedPath: launcher, Output: "2.1.294 (Claude Code)"}, nil
	})
	if _, err := s.StartAgentOperation(context.Background(), TargetClaudeCode, "official-installer", AgentOperationUninstall); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	waitForStatus(t, s, TargetClaudeCode, StatusSucceeded)
	if _, err := os.Lstat(launcher); !os.IsNotExist(err) {
		t.Fatal("Claude launcher still exists after uninstall")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Fatalf("settings removed: %v", err)
	}
}

func TestClaudeUninstallRefusesCopyOutsideNativeLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	elsewhere := writeFileAt(t, filepath.Join(home, "opt", "bin", "claude"), "")
	s := newTestService("darwin", "bash")
	s.verifier = harnessVerifierFunc(func(context.Context, Target) (VerifyResult, error) {
		return VerifyResult{ResolvedPath: elsewhere, Output: "2.1.294 (Claude Code)"}, nil
	})
	job, err := s.StartAgentOperation(context.Background(), TargetClaudeCode, "official-installer", AgentOperationUninstall)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusUnsupported {
		t.Fatalf("job = %+v, want unsupported", job)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Fatalf("a copy outside the native layout was removed: %v", err)
	}
}

func TestUninstallGuideIsPerOSAndOnlyForConfirmedHarnesses(t *testing.T) {
	unix := uninstallGuideFor(TargetClaudeCode, "darwin")
	if unix == nil || !unix.Documented || !slices.Equal(unix.ProgramPaths, []string{"~/.local/bin/claude", "~/.local/share/claude"}) || !slices.Contains(unix.UserDataPaths, "~/.claude") {
		t.Fatalf("Unix guide = %+v", unix)
	}
	windows := uninstallGuideFor(TargetClaudeCode, "windows")
	if windows == nil || !strings.HasSuffix(windows.ProgramPaths[0], `claude.exe`) {
		t.Fatalf("Windows guide = %+v", windows)
	}
	if guide := uninstallGuideFor(TargetUnreal, "darwin"); guide != nil {
		t.Fatalf("a harness without confirmed paths got a guide: %+v", guide)
	}
}

func TestUninstallGuidesNameOnlyOwnedUserScopedPaths(t *testing.T) {
	shared := []string{"~/.local/bin/agent", "~/.cursor", "~/.gemini", `%USERPROFILE%\.cursor`, `%USERPROFILE%\.gemini`}
	for target, spec := range uninstallGuides {
		platforms := []uninstallGuidePlatform{spec.unix, spec.windows}
		if spec.darwin != nil {
			platforms = append(platforms, *spec.darwin)
		}
		if spec.docsURL != "" && !strings.HasPrefix(spec.docsURL, "https://") {
			t.Errorf("%s docs URL %q is not https", target, spec.docsURL)
		}
		for _, platform := range platforms {
			for _, path := range append(slices.Clone(platform.program), platform.userData...) {
				if slices.Contains(shared, path) {
					t.Errorf("%s lists %q, which other tools also own", target, path)
				}
				userScoped := strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "/Applications/") ||
					strings.HasPrefix(path, `%USERPROFILE%\`) || strings.HasPrefix(path, `%LOCALAPPDATA%\`) || strings.HasPrefix(path, `%APPDATA%\`)
				if !userScoped {
					t.Errorf("%s lists %q outside the user's directories", target, path)
				}
			}
		}
	}
	if guide := uninstallGuideFor(TargetKiro, "darwin"); guide == nil || guide.Command != "kiro-cli uninstall" || guide.ProgramPaths[0] != "/Applications/Kiro CLI.app" {
		t.Fatalf("Kiro macOS guide = %+v", guide)
	}
	if guide := uninstallGuideFor(TargetFX, "windows"); guide != nil {
		t.Fatalf("fx has no Windows installer but got %+v", guide)
	}
}
