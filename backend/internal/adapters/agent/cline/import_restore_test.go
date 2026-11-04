package cline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const clineImportedTestID = "saved-import-native"

func importedClineFixture(t *testing.T, root string) ports.SessionRef {
	t.Helper()
	source, worktree := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "sessions", clineImportedTestID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]string{"session_id": clineImportedTestID, "cwd": source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, clineImportedTestID+".json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, clineImportedTestID+".messages.json"), []byte(`{"sessionId":"saved-import-native","messages":[{"role":"user","content":"make a greeting"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return ports.SessionRef{WorkspacePath: worktree, Metadata: map[string]string{
		ports.MetadataKeyAgentSessionID:       clineImportedTestID,
		ports.MetadataKeyImportSourceNativeID: clineImportedTestID,
		ports.MetadataKeyImportConfigDir:      root,
	}}
}

func TestImportedClineUsesExactIDAndPinsAOExecutionWorkspace(t *testing.T) {
	root := t.TempDir()
	ref := importedClineFixture(t, root)
	p := &Plugin{resolvedBinary: "cline"}
	found, err := p.NativeConversationExists(context.Background(), ref, clineImportedTestID, map[string]string{"CLINE_DATA_DIR": root})
	if err != nil || !found {
		t.Fatalf("imported native history = %v, %v", found, err)
	}
	cmd, id, ok, err := p.GetRestoreCommandWithSessionID(context.Background(), ports.RestoreConfig{Session: ref})
	if err != nil || !ok || id != clineImportedTestID {
		t.Fatalf("imported restore = %v, %q, %v, %v", cmd, id, ok, err)
	}
	want := []string{"--cwd", ref.WorkspacePath, "--id", clineImportedTestID}
	if !reflect.DeepEqual(cmd[len(cmd)-4:], want) {
		t.Fatalf("worktree and exact id are not pinned: %v", cmd)
	}
}

func TestImportedClineRefusesWrongIdentityRootAndMissingNativeHistory(t *testing.T) {
	for _, mode := range []string{"wrong-id", "wrong-root", "wrong-launch-root", "missing", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			ref := importedClineFixture(t, root)
			env := map[string]string{"CLINE_DATA_DIR": root}
			switch mode {
			case "wrong-id":
				ref.Metadata[ports.MetadataKeyAgentSessionID] = "another-session"
			case "wrong-root":
				ref.Metadata[ports.MetadataKeyImportConfigDir] = t.TempDir()
			case "wrong-launch-root":
				env["CLINE_DATA_DIR"] = t.TempDir()
			case "missing":
				if err := os.Remove(filepath.Join(root, "sessions", clineImportedTestID, clineImportedTestID+".messages.json")); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(filepath.Join(root, "sessions", clineImportedTestID, clineImportedTestID+".messages.json"), []byte(`{"sessionId":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p := &Plugin{resolvedBinary: "cline"}
			found, err := p.NativeConversationExists(context.Background(), ref, clineImportedTestID, env)
			if found || err == nil {
				t.Fatalf("invalid import accepted = %v, %v", found, err)
			}
			if mode == "wrong-launch-root" {
				return // Command construction has no launch-env argument.
			}
			cmd, id, ok, err := p.GetRestoreCommandWithSessionID(context.Background(), ports.RestoreConfig{Session: ref})
			if err == nil || ok || cmd != nil || id != "" {
				t.Fatalf("invalid import recovered/fell back = %v, %q, %v, %v", cmd, id, ok, err)
			}
		})
	}
}

func TestImportedClineDoesNotRecoverAnotherID(t *testing.T) {
	root := t.TempDir()
	ref := importedClineFixture(t, root)
	ref.Metadata[ports.MetadataKeyImportResumeNativeID] = "missing-prepared-session"
	ref.Metadata[ports.MetadataKeyAgentSessionID] = "missing-prepared-session"
	// A valid original conversation exists, but the explicit prepared identity
	// is missing. The adapter must not select the original or scan recent chats.
	p := &Plugin{resolvedBinary: "cline"}
	cmd, id, ok, err := p.GetRestoreCommandWithSessionID(context.Background(), ports.RestoreConfig{Session: ref})
	if !errors.Is(err, ports.ErrNativeHistoryUnavailable) || ok || cmd != nil || id != "" {
		t.Fatalf("selected alternate native identity = %v, %q, %v, %v", cmd, id, ok, err)
	}
}

func TestImportedClineKeepsOrdinaryWorkspaceRecoveryRestriction(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".cline", "data")
	ref := importedClineFixture(t, root)
	p := &Plugin{}
	env := map[string]string{"HOME": home, "USERPROFILE": home}
	ordinary := ports.SessionRef{WorkspacePath: ref.WorkspacePath}
	if found, err := p.NativeConversationExists(context.Background(), ordinary, clineImportedTestID, env); err != nil || found {
		t.Fatalf("ordinary recovery crossed workspaces = %v, %v", found, err)
	}
	if found, err := p.NativeConversationExists(context.Background(), ref, clineImportedTestID, env); err != nil || !found {
		t.Fatalf("explicit imported identity was refused = %v, %v", found, err)
	}
}
