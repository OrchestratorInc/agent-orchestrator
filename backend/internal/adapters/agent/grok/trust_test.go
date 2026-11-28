package grok

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type grokTrustFixture struct {
	Folders map[string]struct {
		Trusted   bool  `toml:"trusted"`
		DecidedAt int64 `toml:"decided_at"`
	} `toml:"folders"`
}

func readGrokTrust(t *testing.T, path string) grokTrustFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trust file: %v", err)
	}
	var trust grokTrustFixture
	if err := toml.Unmarshal(data, &trust); err != nil {
		t.Fatalf("decode trust file: %v\n%s", err, data)
	}
	return trust
}

func preLaunchGrok(t *testing.T, grokHome, workspace string) {
	t.Helper()
	if err := (&Plugin{}).PreLaunch(context.Background(), ports.LaunchConfig{
		SessionID:     "agent-orchestrator-8244",
		WorkspacePath: workspace,
		Env:           map[string]string{"GROK_HOME": grokHome},
	}); err != nil {
		t.Fatalf("PreLaunch: %v", err)
	}
}

func TestPreLaunchSeedsGrokWorkspaceTrust(t *testing.T) {
	grokHome := filepath.Join(t.TempDir(), "grok-home")
	workspace := filepath.Join(t.TempDir(), "agent-orchestrator-8244")

	preLaunchGrok(t, grokHome, workspace)

	trust := readGrokTrust(t, filepath.Join(grokHome, grokTrustedFoldersFile))
	entry, ok := trust.Folders[workspace]
	if !ok {
		t.Fatalf("folders = %#v, want entry for %q", trust.Folders, workspace)
	}
	if !entry.Trusted {
		t.Fatalf("trusted = false, want true")
	}
	if entry.DecidedAt <= 0 {
		t.Fatalf("decided_at = %d, want positive unix seconds", entry.DecidedAt)
	}
}

func TestPreLaunchKeepsExistingGrokTrustEntriesAndIsIdempotent(t *testing.T) {
	grokHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "ws")
	other := filepath.Join(t.TempDir(), "user-project")
	trustPath := filepath.Join(grokHome, grokTrustedFoldersFile)
	key, ok := grokTOMLKey(other)
	if !ok {
		t.Fatalf("grokTOMLKey(%q) not ok", other)
	}
	existing := []byte("[folders." + key + "]\ntrusted = true\ndecided_at = 1794510537\n")
	if err := os.WriteFile(trustPath, existing, 0o600); err != nil {
		t.Fatal(err)
	}

	preLaunchGrok(t, grokHome, workspace)
	first, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(first, existing) {
		t.Fatalf("existing entries were rewritten:\n%s", first)
	}
	trust := readGrokTrust(t, trustPath)
	if !trust.Folders[other].Trusted || trust.Folders[other].DecidedAt != 1794510537 {
		t.Fatalf("existing entry changed: %#v", trust.Folders[other])
	}
	if !trust.Folders[workspace].Trusted {
		t.Fatalf("workspace not trusted: %#v", trust.Folders)
	}

	preLaunchGrok(t, grokHome, workspace)
	second, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("second PreLaunch changed the trust file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestPreLaunchLeavesRecordedGrokDecisionAlone(t *testing.T) {
	grokHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "ws")
	trustPath := filepath.Join(grokHome, grokTrustedFoldersFile)
	key, _ := grokTOMLKey(workspace)
	existing := []byte("[folders." + key + "]\ntrusted = false\ndecided_at = 1794510537\n")
	if err := os.WriteFile(trustPath, existing, 0o600); err != nil {
		t.Fatal(err)
	}

	preLaunchGrok(t, grokHome, workspace)

	got, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, existing) {
		t.Fatalf("recorded decision was rewritten:\n%s", got)
	}
	if readGrokTrust(t, trustPath).Folders[workspace].Trusted {
		t.Fatal("a recorded \"no\" was flipped to trusted")
	}
}

func TestPreLaunchLeavesUnsupportedGrokTrustFileUntouched(t *testing.T) {
	for name, content := range map[string]string{
		"invalid toml":           "[folders\ntrusted = ",
		"folders is not a table": "folders = \"nope\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			grokHome := t.TempDir()
			trustPath := filepath.Join(grokHome, grokTrustedFoldersFile)
			if err := os.WriteFile(trustPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			preLaunchGrok(t, grokHome, filepath.Join(t.TempDir(), "ws"))

			got, err := os.ReadFile(trustPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != content {
				t.Fatalf("trust file changed:\n%s", got)
			}
		})
	}
}

func TestEnsureGrokFolderTrustedQuotesPathWithSingleQuote(t *testing.T) {
	grokHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "it's-a-repo")
	trustPath := filepath.Join(grokHome, grokTrustedFoldersFile)

	if err := ensureGrokFolderTrusted(trustPath, workspace, time.Unix(1794510537, 0)); err != nil {
		t.Fatalf("ensureGrokFolderTrusted: %v", err)
	}

	entry := readGrokTrust(t, trustPath).Folders[workspace]
	if !entry.Trusted || entry.DecidedAt != 1794510537 {
		t.Fatalf("entry = %#v, want trusted at 1794510537", entry)
	}
}

func TestPreLaunchWithoutWorkspaceIsNoop(t *testing.T) {
	grokHome := t.TempDir()

	preLaunchGrok(t, grokHome, "")

	if _, err := os.Stat(filepath.Join(grokHome, grokTrustedFoldersFile)); !os.IsNotExist(err) {
		t.Fatalf("trust file stat err = %v, want not exist", err)
	}
}

func TestPreLaunchWritesThroughDanglingGrokTrustSymlink(t *testing.T) {
	grokHome := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "ws")
	trustPath := filepath.Join(grokHome, grokTrustedFoldersFile)
	target := filepath.Join(grokHome, "dotfiles", "grok-trust.toml")
	if err := os.Symlink(filepath.Join("dotfiles", "grok-trust.toml"), trustPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	preLaunchGrok(t, grokHome, workspace)

	info, err := os.Lstat(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("trust file mode = %v, want the symlink kept", info.Mode())
	}
	if !readGrokTrust(t, target).Folders[workspace].Trusted {
		t.Fatalf("workspace not trusted in the link target %s", target)
	}
}
