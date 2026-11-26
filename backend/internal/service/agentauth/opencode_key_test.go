package agentauth

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenCodeKeyUsesNativeStoreWithoutLaunchingCommand(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OPENCODE_DATA_DIR", root)
	path := filepath.Join(root, "auth.json")
	if err := os.WriteFile(path, []byte(`{"existing":{"type":"oauth","access":"keep"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("opencode"), nil)
	t.Cleanup(svc.Close)
	svc.commandContext = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("API key appeared in a command")
		return nil
	}
	result, err := svc.StartLogin(t.Context(), "opencode", "anthropic_key", "test-secret")
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "test-secret") {
		t.Fatal("credential leaked in operation")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if entries["existing"]["access"] != "keep" || entries["anthropic"]["type"] != "api" || entries["anthropic"]["key"] != "test-secret" {
		t.Fatal("native credential entries not preserved")
	}
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("credential file permissions: %v %v", info, err)
	}
}

func TestOpenCodeKeyRefusesInvalidNativeStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OPENCODE_DATA_DIR", root)
	path := filepath.Join(root, "auth.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(foundExecutable("opencode"), nil)
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "opencode", "openai_key", "test-secret"); err == nil {
		t.Fatal("overwrote invalid native store")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "not json" {
		t.Fatal("modified invalid native store")
	}
}
