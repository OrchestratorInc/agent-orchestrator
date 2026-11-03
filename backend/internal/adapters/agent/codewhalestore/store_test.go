package codewhalestore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const syntheticID = "33333333-3333-4333-8333-333333333333"

func fixture(t *testing.T, root, cwd string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"metadata": map[string]any{
			"id": syntheticID, "workspace": cwd, "title": "Synthetic native chat", "model": "synthetic-model",
			"created_at": "2026-11-03T00:00:00Z", "updated_at": "2026-11-03T00:01:00Z", "message_count": 2, "total_tokens": 0,
			"runtime_store": map[string]any{"data_dir": "/old/runtime", "execution_scope": "old-owner"}, "publication_id": "old-publication",
		},
		"messages": []any{},
		"journal": map[string]any{
			"schema_version": 1, "leaf_id": "answer", "entries": []any{
				map[string]any{"id": "prompt", "kind": "user", "text": "remember the synthetic orca", "created_at": "2026-11-03T00:00:00Z"},
				map[string]any{"id": "answer", "parent_id": "prompt", "kind": "assistant", "text": "synthetic answer", "created_at": "2026-11-03T00:01:00Z"},
				map[string]any{"id": "abandoned", "parent_id": "prompt", "kind": "assistant", "text": "abandoned branch", "created_at": "2026-11-03T00:00:30Z"},
			},
		},
		"leaf_id": "answer", "system_prompt": "synthetic historical context",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sessions", syntheticID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func change(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	edit(raw)
	body, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadNativeActiveBranch(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := fixture(t, root, cwd)
	archive, err := ReadImport(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	if archive.ID != syntheticID || archive.CWD != cwd || len(archive.Messages) != 2 || archive.Messages[0].Role != "user" || archive.Messages[1].Text != "synthetic answer" {
		t.Fatalf("archive = %#v", archive)
	}
}

func TestCopyPreservesNativeGraphAndSource(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	source := fixture(t, root, cwd)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	prepared, target, err := Copy(context.Background(), root, source, launch)
	if err != nil {
		t.Fatal(err)
	}
	wantCWD, pathErr := filepath.EvalSymlinks(launch)
	if pathErr != nil || prepared.ID == syntheticID || prepared.CWD != wantCWD || len(prepared.Messages) != 2 {
		t.Fatalf("prepared = %#v", prepared)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source changed: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(raw["metadata"], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["runtime_store"] != nil || metadata["publication_id"] != nil {
		t.Fatal("copy retained original runtime ownership")
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(before, &original); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw["journal"], original["journal"]) || !bytes.Equal(raw["system_prompt"], original["system_prompt"]) {
		t.Fatal("rewrote native conversation graph")
	}
	info, err := os.Stat(target)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %v, %v", info, err)
	}
	change(t, target, func(raw map[string]any) {
		raw["metadata"].(map[string]any)["title"] = "later AO title"
		raw["work_state"] = map[string]any{"todos": []any{}}
	})
	retry, retryPath, err := Copy(context.Background(), root, source, launch)
	if err != nil || retryPath != target || retry.ID != prepared.ID || retry.Title != "later AO title" {
		t.Fatalf("retry = %#v, %q, %v", retry, retryPath, err)
	}
}

func TestImportRefusesDependentAndBrokenSnapshots(t *testing.T) {
	for _, name := range []string{"artifacts", "context_references", "approval_receipts", "work_state", "identity", "schema", "cycle", "missing_parent", "child", "goal", "checkpoint"} {
		t.Run(name, func(t *testing.T) {
			root, cwd := t.TempDir(), t.TempDir()
			path := fixture(t, root, cwd)
			change(t, path, func(raw map[string]any) {
				switch name {
				case "identity":
					raw["metadata"].(map[string]any)["id"] = "44444444-4444-4444-8444-444444444444"
				case "schema":
					raw["schema_version"] = 999
				case "child":
					raw["metadata"].(map[string]any)["spawn_depth"] = 1
				case "cycle", "missing_parent":
					entry := raw["journal"].(map[string]any)["entries"].([]any)[0].(map[string]any)
					entry["parent_id"] = "answer"
					if name == "missing_parent" {
						entry["parent_id"] = "missing"
					}
				case "work_state":
					raw[name] = map[string]any{"graph": map[string]any{}}
				case "goal", "checkpoint":
				default:
					raw[name] = []any{map[string]any{"synthetic": true}}
				}
			})
			if name == "goal" || name == "checkpoint" {
				dir := ".goals"
				if name == "checkpoint" {
					dir = "checkpoints"
				}
				if err := os.MkdirAll(filepath.Join(root, "sessions", dir), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "sessions", dir, syntheticID+".json"), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ReadImport(context.Background(), root, path); err == nil {
				t.Fatal("offered unsupported native snapshot")
			}
		})
	}
}

func TestReadRefusesEscapesSymlinksAndCancellation(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	outside := fixture(t, t.TempDir(), cwd)
	if _, err := ReadImport(context.Background(), root, outside); err == nil {
		t.Fatal("read outside provider root")
	}
	path := fixture(t, root, cwd)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadImport(ctx, root, path); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadImport(context.Background(), root, path); err == nil {
		t.Fatal("followed a native file symlink")
	}
}
