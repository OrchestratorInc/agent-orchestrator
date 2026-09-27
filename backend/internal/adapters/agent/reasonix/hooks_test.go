package reasonix

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReasonixHooksPreserveNativeSettingsAndUserOrder(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".reasonix")
	path := filepath.Join(dir, "settings.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"unknown":{"nested":true},"hooks":{"PreToolUse":[{"command":"user-first","match":"bash","unknown":123},{"command":"echo ao hooks reasonix pre-tool-use","description":"user"}],"Custom":[{"command":"user-custom","future":[1,2]}]}}`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{}
	cfg := ports.WorkspaceHookConfig{WorkspacePath: workspace}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("reinstall changed settings")
	}
	var top struct {
		Unknown json.RawMessage                         `json:"unknown"`
		Hooks   map[string][]map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(second, &top); err != nil {
		t.Fatal(err)
	}
	if string(bytes.TrimSpace(top.Unknown)) == "null" || !bytes.Contains(top.Unknown, []byte(`true`)) {
		t.Fatal("lost unknown setting")
	}
	pre := top.Hooks["PreToolUse"]
	if len(pre) != 3 || string(pre[0]["command"]) != `"user-first"` || string(pre[1]["command"]) != `"echo ao hooks reasonix pre-tool-use"` || string(pre[0]["unknown"]) != "123" {
		t.Fatalf("lost user hooks/order: %s", second)
	}
	if len(top.Hooks["Custom"]) != 1 || !bytes.Contains(top.Hooks["Custom"][0]["future"], []byte("2")) {
		t.Fatal("lost custom event")
	}
	if _, ok := pre[2]["hooks"]; ok {
		t.Fatal("wrote matcher groups instead of native direct array")
	}
	if ok, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || !ok {
		t.Fatalf("installed=%v err=%v", ok, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("changed user file mode: %v", info.Mode())
	}
	if runtime.GOOS == "windows" && info.Mode().Perm()&0o200 == 0 {
		t.Fatal("changed writable user file to read-only")
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ignore), "/settings.json\n") {
		t.Fatal("settings not ignored")
	}
	if err := p.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	if ok, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || ok {
		t.Fatalf("installed=%v err=%v", ok, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(original, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("uninstall changed user settings: %s", after)
	}
}

func TestReasonixHooksRejectMalformedSettingsWithoutChangingFiles(t *testing.T) {
	for _, body := range []string{"", `null`, `[]`, `{`, `{"hooks":null}`, `{"hooks":[]}`, `{"hooks":{"PreToolUse":null}}`, `{"hooks":{"PreToolUse":{}}}`, `{"hooks":{"PreToolUse":[null]}}`, `{"hooks":{"PreToolUse":[{"command":17}]}}`} {
		t.Run(body, func(t *testing.T) {
			workspace := t.TempDir()
			dir := filepath.Join(workspace, ".reasonix")
			path := filepath.Join(dir, "settings.json")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			p := &Plugin{}
			for _, run := range []func() error{func() error {
				return p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace})
			}, func() error { return p.UninstallHooks(context.Background(), workspace) }, func() error { _, err := p.AreHooksInstalled(context.Background(), workspace); return err }} {
				if err := run(); err == nil {
					t.Fatal("accepted malformed settings")
				}
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != body {
				t.Fatal("modified malformed settings")
			}
			if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
				t.Fatal("wrote ignore after rejected settings")
			}
		})
	}
}

func TestReasonixObserverCommandSwallowsOutputAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command execution")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake ao's executable")
	input := filepath.Join(dir, "input")
	script := "#!/bin/sh\ncat > \"$TEST_INPUT\"\nprintf '%s' '{\"decision\":\"approve\",\"additionalContext\":\"bad\"}'\necho private >&2\nexit 2\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", reasonixObserverCommand(exe, "pre-tool-use"))
	cmd.Env = append(os.Environ(), "TEST_INPUT="+input)
	cmd.Stdin = strings.NewReader(`{"sessionId":"native-1"}`)
	output, err := cmd.CombinedOutput()
	if err != nil || len(output) != 0 {
		t.Fatalf("observer output=%q err=%v", output, err)
	}
	got, err := os.ReadFile(input)
	if err != nil || string(got) != `{"sessionId":"native-1"}` {
		t.Fatalf("stdin=%q err=%v", got, err)
	}
}

func TestReasonixHooksRejectSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(outside, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(workspace, ".reasonix")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err == nil {
		t.Fatal("accepted settings symlink")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != `{}` {
		t.Fatal("changed symlink target")
	}
}

func TestReasonixSettingsSnapshotRefusesConcurrentChanges(t *testing.T) {
	for _, change := range []string{"replace", "edit", "create"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if change != "create" {
				if err := os.WriteFile(path, []byte(`{"user":1}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := readReasonixSettings(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if change == "replace" {
				replacement := path + ".replacement"
				if err := os.WriteFile(replacement, []byte(`{"user":2}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte(`{"user":2}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := snapshot.write(context.Background(), []byte(`{"ao":true}`)); err == nil {
				t.Fatal("overwrote a concurrently changed settings file")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != `{"user":2}` {
				t.Fatalf("user settings=%q err=%v", got, err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("left transaction files: %v", entries)
			}
		})
	}
}

func TestReasonixHooksKeepUserGitignore(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".reasonix")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("user-pattern\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: workspace}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "user-pattern\n" {
		t.Fatalf("gitignore=%q err=%v", got, err)
	}
}
