package openhands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func openHandsImportFixture(t *testing.T) ports.ImportedSessionRelocation {
	t.Helper()
	r := ports.ImportedSessionRelocation{NativeSessionID: "12345678-1234-1234-1234-123456789012", ConfigDir: t.TempDir(), LaunchCWD: t.TempDir(), DataDir: t.TempDir()}
	dataDir, err := filepath.EvalSymlinks(r.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	r.DataDir = dataDir
	dir := filepath.Join(r.ConfigDir, strings.ReplaceAll(r.NativeSessionID, "-", ""))
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.TranscriptPath = filepath.Join(dir, "base_state.json")
	raw, err := json.Marshal(map[string]any{"id": r.NativeSessionID, "workspace": map[string]string{"kind": "LocalWorkspace", "working_dir": t.TempDir()}, "agent": map[string]any{"llm": map[string]string{"api_key": "**********"}}, "leaf_event_id": "87654321-1234-1234-1234-123456789012"})
	if err != nil {
		t.Fatal(err)
	}
	writeOpenHandsFixture(t, r.TranscriptPath, raw)
	writeOpenHandsFixture(t, filepath.Join(dir, "events", "event-00000-87654321-1234-1234-1234-123456789012.json"), []byte(`{"id":"87654321-1234-1234-1234-123456789012","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Synthetic history"}]}}`))
	r.SourceFingerprint, err = ConversationFingerprint(context.Background(), r.ConfigDir, r.NativeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func writeOpenHandsFixture(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpenHandsPrivatePreparationAndCheckpointRetry(t *testing.T) {
	r := openHandsImportFixture(t)
	ctx := context.Background()
	before, err := os.ReadFile(r.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	// Profile files and other conversations are outside the snapshot boundary.
	writeOpenHandsFixture(t, filepath.Join(r.ConfigDir, "agent_settings.json"), []byte("synthetic-profile-not-to-copy"))
	ref, err := New().PrepareImportedSession(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID != r.NativeSessionID || ref.ConfigDir == r.ConfigDir || !strings.HasPrefix(ref.ConfigDir, r.DataDir+string(filepath.Separator)) {
		t.Fatalf("private native ref = %+v", ref)
	}
	raw, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Workspace struct {
			Kind string `json:"kind"`
			CWD  string `json:"working_dir"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &state); err != nil || state.Workspace.CWD != r.LaunchCWD || state.Workspace.Kind != "LocalWorkspace" {
		t.Fatalf("private workspace = %+v, %v", state, err)
	}
	paths, err := ConversationFiles(ctx, r.ConfigDir, r.NativeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	privatePaths, err := ConversationFiles(ctx, ref.ConfigDir, ref.NativeSessionID)
	if err != nil || len(paths) != len(privatePaths) {
		t.Fatalf("private files = %v, %v", privatePaths, err)
	}
	for i := 1; i < len(paths); i++ {
		source, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(privatePaths[i])
		if err != nil || !bytes.Equal(source, copied) {
			t.Fatalf("native event changed: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(ref.ConfigDir, "agent_settings.json")); !os.IsNotExist(err) {
		t.Fatalf("profile copied: %v", err)
	}
	after, err := os.ReadFile(r.TranscriptPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source state changed: %v", err)
	}
	// Publication succeeded but AO's following checkpoint failed. A different
	// adapter instance recovers the same proof even if the original disappears.
	if err := os.RemoveAll(r.ConfigDir); err != nil {
		t.Fatal(err)
	}
	retry, err := New().PrepareImportedSession(ctx, r)
	if err != nil || retry != ref {
		t.Fatalf("checkpoint retry = %+v, %v; want %+v", retry, err, ref)
	}
	available, err := New().ProbeNativeSession(ctx, retry)
	if err != nil || available != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("private probe = %s, %v", available, err)
	}
}

func TestOpenHandsPrivatePreparationRefusesChangedEvidence(t *testing.T) {
	for _, change := range []string{"source before copy", "private state", "private event", "proof", "selection"} {
		t.Run(change, func(t *testing.T) {
			r := openHandsImportFixture(t)
			if change == "source before copy" {
				writeOpenHandsFixture(t, r.TranscriptPath, []byte(`{"id":"12345678-1234-1234-1234-123456789012","workspace":{"working_dir":"changed"}}`))
				if _, err := New().PrepareImportedSession(context.Background(), r); err == nil {
					t.Fatal("accepted changed source")
				}
				return
			}
			ref, err := New().PrepareImportedSession(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "private state":
				writeOpenHandsFixture(t, ref.TranscriptPath, []byte(`{"changed":true}`))
			case "private event":
				paths, err := ConversationFiles(context.Background(), ref.ConfigDir, ref.NativeSessionID)
				if err != nil {
					t.Fatal(err)
				}
				writeOpenHandsFixture(t, paths[1], []byte(`{"changed":true}`))
			case "proof":
				proofs, err := filepath.Glob(filepath.Join(r.DataDir, "native-imports", "openhands", "*.json"))
				if err != nil || len(proofs) != 1 {
					t.Fatalf("proofs = %v, %v", proofs, err)
				}
				writeOpenHandsFixture(t, proofs[0], []byte(`{"version":999}`))
			case "selection":
				r.SourceFingerprint = "openhands:v1:unrelated"
			}
			if _, err := New().PrepareImportedSession(context.Background(), r); err == nil {
				t.Fatal("accepted mismatched private proof or changed copy")
			}
		})
	}
}

func TestOpenHandsPrivatePreparationRejectsCredentials(t *testing.T) {
	for _, agent := range []string{
		`{"llm":{"api_key":"synthetic-plaintext"}}`,
		`{"llm":{"api_key":"synthetic-ciphertext"}}`,
		`{"llm":{"extra_headers":{"Authorization":"synthetic"}}}`,
		`{"mcp_config":{"env":{"CUSTOM_AUTH":"synthetic"}}}`,
	} {
		r := openHandsImportFixture(t)
		raw, err := os.ReadFile(r.TranscriptPath)
		if err != nil {
			t.Fatal(err)
		}
		var state map[string]json.RawMessage
		if err := json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		state["agent"] = json.RawMessage(agent)
		raw, err = json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		writeOpenHandsFixture(t, r.TranscriptPath, raw)
		if _, err := New().PrepareImportedSession(context.Background(), r); err == nil {
			t.Fatal("copied stored credential configuration")
		}
		copies, err := filepath.Glob(filepath.Join(r.DataDir, "native-imports", "openhands", ".native-*"))
		if err != nil || len(copies) != 0 {
			t.Fatalf("credential rejection left copies = %v, %v", copies, err)
		}
	}
}
