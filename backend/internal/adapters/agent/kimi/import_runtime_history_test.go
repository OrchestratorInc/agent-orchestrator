package kimi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKimiImportedRuntimeAllowsHistoryAndMediaGrowth(t *testing.T) {
	r := kimiImportFixture(t)
	kimiMediaFixture(t, r)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	blobDir := filepath.Join(filepath.Dir(ref.TranscriptPath), "blobs")
	for i := range 5 {
		blob, err := os.CreateTemp(blobDir, "new-blob-")
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.New()
		encoder := base64.NewEncoder(base64.StdEncoding, sum)
		chunk := strings.Repeat(string(rune('a'+i)), 1<<20)
		for range 13 {
			if _, err := io.WriteString(io.MultiWriter(blob, encoder), chunk); err != nil {
				t.Fatal(err)
			}
		}
		if err := encoder.Close(); err != nil {
			t.Fatal(err)
		}
		if err := blob.Close(); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%x", sum.Sum(nil))
		if err := os.Rename(blob.Name(), filepath.Join(blobDir, hash)); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(f, "{\"type\":\"context.append_message\",\"agentId\":\"main\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"image_url\",\"imageUrl\":{\"url\":\"blobref:image/png;%s\"}}]}}\n", hash); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(ref.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	rel, err := filepath.Rel(ref.ConfigDir, filepath.Dir(ref.TranscriptPath))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readKimiBlobs(t.Context(), root, rel, wire); err == nil {
		t.Fatal("new snapshot accepted oversized media total")
	}
	if _, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil); err != nil {
		t.Fatalf("native media growth could not reopen: %v", err)
	}
	f, err = os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Many ordinary records exceed the snapshot budget without a large single
	// record or an allocation proportional to the whole live journal.
	record := "{\"type\":\"opaque\",\"payload\":\"" + strings.Repeat("x", 1<<20) + "\"}\n"
	for range 65 {
		if _, err := io.WriteString(f, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadImportedSession(t.Context(), ref.ConfigDir, r.NativeSessionID); err == nil {
		t.Fatal("new snapshot accepted oversized journal")
	}
	env, imported, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil)
	if err != nil || !imported || env["KIMI_CODE_HOME"] != ref.ConfigDir {
		t.Fatalf("growing native conversation could not reopen: %v", err)
	}
}

func TestKimiImportedRuntimeRefusesLostOrChangedPrefix(t *testing.T) {
	for _, mode := range []string{"journal changed", "journal truncated", "journal missing", "blob changed", "blob missing", "state moved"} {
		t.Run(mode, func(t *testing.T) {
			r := kimiImportFixture(t)
			hash, _, _ := kimiMediaFixture(t, r)
			ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil {
				t.Fatal(err)
			}
			blob := filepath.Join(filepath.Dir(ref.TranscriptPath), "blobs", hash)
			switch mode {
			case "journal changed":
				data, err := os.ReadFile(ref.TranscriptPath)
				if err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(ref.TranscriptPath, []byte(strings.Replace(string(data), "MARKER", "FORGED", 1)), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			case "journal truncated":
				err = os.Truncate(ref.TranscriptPath, 10)
			case "journal missing":
				err = os.Remove(ref.TranscriptPath)
			case "blob changed":
				err = os.WriteFile(blob, []byte("changed payload"), 0o600)
			case "blob missing":
				err = os.Remove(blob)
			case "state moved":
				path := filepath.Join(kimiSessionDirForWire(ref.TranscriptPath), "state.json")
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var state kimiImportState
				if err := json.Unmarshal(data, &state); err != nil {
					t.Fatal(err)
				}
				state.CWD += "-other"
				data, err = json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(path, data, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, imported, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil); err == nil || !imported {
				t.Fatal("lost or changed private history accepted")
			}
		})
	}
}

func TestKimiRuntimeRecoversLegacyPrefixProof(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint("changed source=", changed), func(t *testing.T) {
			r := kimiImportFixture(t)
			ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
			if err != nil {
				t.Fatal(err)
			}
			proofPath := filepath.Join(ref.ConfigDir, ".ao-import.json")
			data, err := os.ReadFile(proofPath)
			if err != nil {
				t.Fatal(err)
			}
			var proof kimiImportProof
			if err := json.Unmarshal(data, &proof); err != nil {
				t.Fatal(err)
			}
			proof.WireBytes, proof.WireHash, proof.Blobs = 0, "", nil
			data, err = json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(proofPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{ref.TranscriptPath, r.TranscriptPath} {
				if path == r.TranscriptPath && !changed {
					continue
				}
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("{\"type\":\"opaque\",\"payload\":1e400}\n")
				_ = f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, _, err = ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil)
			if (err != nil) != changed {
				t.Fatalf("legacy proof recovery: %v", err)
			}
			if !changed {
				// Future reopens no longer need the source conversation at all.
				if err := os.RemoveAll(kimiSessionDirForWire(r.TranscriptPath)); err != nil {
					t.Fatal(err)
				}
				if _, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// Kimi adds each subagent a conversation starts to state.json. An imported chat
// that has used one must still reopen, while the main agent stays required.
func TestKimiImportedRuntimeReopensAfterSubagentUse(t *testing.T) {
	r := kimiImportFixture(t)
	ref, err := prepareKimiImport(t.Context(), r, "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Dir(filepath.Dir(filepath.Dir(ref.TranscriptPath)))
	statePath := filepath.Join(sessionDir, "state.json")
	original, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	rewrite := func(edit func(agents map[string]any)) {
		t.Helper()
		var state map[string]any
		if err := json.Unmarshal(original, &state); err != nil {
			t.Fatal(err)
		}
		agents, ok := state["agents"].(map[string]any)
		if !ok {
			t.Fatalf("state has no agents map: %s", original)
		}
		edit(agents)
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reopen := func() error {
		_, _, err := ImportedRuntimeEnv(t.Context(), ref.ConfigDir, r.DataDir, r.NativeSessionID, r.LaunchCWD, "", nil)
		return err
	}

	subagentHome := filepath.Join(sessionDir, "agents", "subagent-1")
	if err := os.MkdirAll(subagentHome, 0o700); err != nil {
		t.Fatal(err)
	}
	rewrite(func(agents map[string]any) {
		agents["subagent-1"] = map[string]any{"homedir": subagentHome, "type": "subagent"}
	})
	if err := reopen(); err != nil {
		t.Fatalf("imported chat with a subagent could not reopen: %v", err)
	}

	rewrite(func(agents map[string]any) {
		agents["subagent-1"] = map[string]any{"homedir": subagentHome, "type": "subagent"}
		delete(agents, "main")
	})
	if err := reopen(); err == nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatalf("state without its main agent reopened: %v", err)
	}

	rewrite(func(agents map[string]any) {
		agents["main"].(map[string]any)["type"] = "subagent"
	})
	if err := reopen(); err == nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatalf("state whose main agent changed type reopened: %v", err)
	}
}
