package memcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestPrivateInstructionsAndWitnessPreserveUserHooks(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(workspace, ".memcode")
	_ = os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "hooks.json")
	_ = os.WriteFile(path, []byte(`{"native_extra":true,"hooks":{"session_start":[{"command":"printf user","timeout":4,"custom":true}],"session_end":[{"command":"printf end"}]}}`), 0600)
	cfg := ports.WorkspaceHookConfig{DataDir: data, SessionID: "ao-1", WorkspacePath: workspace, SystemPrompt: "PRIVATE standing context"}
	p := New()
	for i := 0; i < 2; i++ {
		if err := p.GetAgentHooks(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	var config struct {
		Hooks map[string][]map[string]json.RawMessage `json:"hooks"`
		Extra bool                                    `json:"native_extra"`
	}
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if !config.Extra || len(config.Hooks["session_start"]) != maxInstructionChunks+1 || len(config.Hooks["session_end"]) != 1 {
		t.Fatal(string(b))
	}
	if bytes.Contains(b, []byte("PRIVATE")) {
		t.Fatal("standing context leaked to workspace hook config")
	}
	ignored, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil || !strings.Contains(string(ignored), "/sessions/\n") {
		t.Fatalf("Memcode history is not ignored: %q, %v", ignored, err)
	}
	t.Setenv("AO_MEMCODE_DATA_DIR", data)
	t.Setenv("AO_MEMCODE_SESSION", "ao-1")
	t.Setenv("AO_MEMCODE_GENERATION", "launch-1")
	t.Setenv("MEMCODE_SESSION_ID", "sess_b1b1b1b1")
	var out bytes.Buffer
	if _, err := EmitStartWitness(&out); err != nil {
		t.Fatal(err)
	}
	w, err := readWitness(data, "ao-1", "launch-1")
	if err != nil || w.Sequence != 1 || w.NativeID != "sess_b1b1b1b1" {
		t.Fatal(w, err)
	}
	if out.String() != cfg.SystemPrompt {
		t.Fatal("private context not emitted")
	}
	t.Setenv("MEMCODE_SESSION_ID", testID)
	if _, err := EmitStartWitness(&out); err != nil {
		t.Fatal(err)
	}
	w, _ = readWitness(data, "ao-1", "launch-1")
	if w.Sequence != 2 || w.NativeID != testID {
		t.Fatal(w)
	}
	cfg.SystemPrompt = strings.Repeat("x", (512<<10)+1)
	if err := p.GetAgentHooks(context.Background(), cfg); err == nil {
		t.Fatal("native output cap ignored")
	}
}

func TestEmitStartWitnessCommitsOnlyAfterContextEmission(t *testing.T) {
	for _, test := range []struct {
		name       string
		writeChunk bool
		failWrite  bool
	}{
		{name: "missing instruction file"},
		{name: "output failure", writeChunk: true, failWrite: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := t.TempDir()
			t.Setenv("AO_MEMCODE_DATA_DIR", data)
			t.Setenv("AO_MEMCODE_SESSION", "ao-1")
			t.Setenv("AO_MEMCODE_GENERATION", "launch-1")
			t.Setenv("MEMCODE_SESSION_ID", "sess_b1b1b1b1")
			var out io.Writer = &bytes.Buffer{}
			if test.writeChunk {
				dir := filepath.Join(data, "agent-launches", "memcode", "ao-1")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "instructions.json"), []byte(`["Y29udGV4dA=="]`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.failWrite {
				out = failingWriter{}
			}
			if _, err := EmitStartWitness(out); err == nil {
				t.Fatal("context emission failure accepted")
			}
			w, err := readWitness(data, "ao-1", "launch-1")
			if err != nil || w.Sequence != 0 {
				t.Fatal("witness committed before context emission", w, err)
			}
		})
	}
}

func TestGetAgentHooksAcceptsNullHooks(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(workspace, ".memcode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New().GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		DataDir: data, SessionID: "ao-1", WorkspacePath: workspace, SystemPrompt: "context",
	}); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]map[string]json.RawMessage `json:"hooks"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Hooks["session_start"]) != maxInstructionChunks || len(config.Hooks["pre_tool_use"]) != 1 || len(config.Hooks["post_tool_use"]) != 0 {
		t.Fatal(string(b))
	}
}

func TestCleanupWorkspaceRemovesOnlyValidatedSessionArtifacts(t *testing.T) {
	data := t.TempDir()
	root := filepath.Join(data, "agent-launches", "memcode")
	for _, session := range []string{"ao-1", "ao-2"} {
		dir := filepath.Join(root, session, "launch-1")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "witness.json"), []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	if err := p.CleanupWorkspace(context.Background(), ports.WorkspaceHookConfig{DataDir: data, SessionID: "ao-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ao-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleaned session artifacts stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ao-2", "launch-1", "witness.json")); err != nil {
		t.Fatalf("sibling session artifact stat = %v, want preserved", err)
	}
	if err := p.CleanupWorkspace(context.Background(), ports.WorkspaceHookConfig{DataDir: data, SessionID: "../ao-2"}); err == nil {
		t.Fatal("path-traversing session id accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "ao-2", "launch-1", "witness.json")); err != nil {
		t.Fatalf("sibling session artifact after rejected cleanup = %v, want preserved", err)
	}
}

func TestLongPrivateContextIsNotTruncated(t *testing.T) {
	data := []byte(strings.Repeat("Standing instruction αβ.\n\n", 1200))
	chunks, err := instructionChunks(data)
	if err != nil || len(chunks) < 2 {
		t.Fatal("not split", err)
	}
	var recovered []string
	for _, chunk := range chunks {
		if len(chunk) > 8192 {
			t.Fatal("native output cap exceeded")
		}
		recovered = append(recovered, strings.TrimSpace(string(chunk)))
	}
	if strings.Join(recovered, "\n\n") != strings.TrimSpace(string(data)) {
		t.Fatal("native combination altered context")
	}
	for _, indivisible := range []string{strings.Repeat("α", 5000), strings.Repeat("one line\n", 1200), strings.Repeat("x", 8189) + "\n\n    indented boundary"} {
		if _, err := instructionChunks([]byte(indivisible)); err == nil {
			t.Fatal("accepted altered or oversized context")
		}
	}
}

func TestInstructionChunksWalkBackPastIndentedBoundary(t *testing.T) {
	data := []byte(strings.Repeat("a", 4000) + "\n\n" + strings.Repeat("b", 4000) + "\n\n    indented code\n\n" + strings.Repeat("c", 1000))
	chunks, err := instructionChunks(data)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, chunk := range chunks {
		parts = append(parts, strings.TrimSpace(string(chunk)))
	}
	if strings.Join(parts, "\n\n") != string(data) {
		t.Fatal("walked-back split altered instruction text")
	}
}

func TestSharedWorkspaceKeepsEachSessionsContext(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	p := New()
	long := strings.TrimSpace(strings.Repeat("private long paragraph\n\n", 1000))
	for _, order := range [][]string{{long, "short"}, {"short", long}} {
		envs := make([]map[string]string, len(order))
		for i, prompt := range order {
			envs[i] = map[string]string{}
			if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
				DataDir: data, WorkspacePath: workspace, SessionID: fmt.Sprintf("ao-%d", i), SystemPrompt: prompt,
				Env: envs[i],
			}); err != nil {
				t.Fatal(err)
			}
		}
		for i, prompt := range order {
			t.Setenv("AO_MEMCODE_DATA_DIR", data)
			t.Setenv("AO_MEMCODE_SESSION", fmt.Sprintf("ao-%d", i))
			t.Setenv(instructionChunkCountEnv, envs[i][instructionChunkCountEnv])
			var parts []string
			for slot := 0; slot < maxInstructionChunks; slot++ {
				var out bytes.Buffer
				if err := EmitInstructionChunk(&out, fmt.Sprintf("context-chunk-%d", slot)); err != nil {
					t.Fatal(err)
				}
				if out.Len() > 8192 {
					t.Fatal("native per-hook output limit exceeded")
				}
				if text := strings.TrimSpace(out.String()); text != "" {
					parts = append(parts, text)
				}
			}
			if got := strings.Join(parts, "\n\n"); got != prompt {
				t.Fatalf("session %d context changed after shared hook installation", i)
			}
		}
	}
}

func TestUnusedContextSlotSkipsInstructionSnapshot(t *testing.T) {
	t.Setenv("AO_MEMCODE_DATA_DIR", t.TempDir())
	t.Setenv("AO_MEMCODE_SESSION", "ao-1")
	t.Setenv(instructionChunkCountEnv, "1")
	var out bytes.Buffer
	// There is no snapshot to open: only the active slot may require it.
	if err := EmitInstructionChunk(&out, "context-chunk-1"); err != nil || out.Len() != 0 {
		t.Fatalf("unused slot = %q, %v", out.String(), err)
	}
	if err := EmitInstructionChunk(&out, "context-chunk-0"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active slot error = %v, want missing snapshot", err)
	}
	for _, invalid := range []string{"0", "65", "-1", "invalid"} {
		t.Setenv(instructionChunkCountEnv, invalid)
		if err := EmitInstructionChunk(&out, "context-chunk-1"); err == nil {
			t.Fatalf("accepted invalid launch chunk count %q", invalid)
		}
	}
}

func TestLaunchChunkCountMustMatchPreparedContext(t *testing.T) {
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), WorkspacePath: t.TempDir(), SessionID: "ao-1",
		SystemPrompt: "context", Env: map[string]string{instructionChunkCountEnv: "64"}}
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Env[instructionChunkCountEnv] != "1" {
		t.Fatalf("launch count = %q, want prepared count 1", cfg.Env[instructionChunkCountEnv])
	}
	t.Setenv("AO_MEMCODE_DATA_DIR", cfg.DataDir)
	t.Setenv("AO_MEMCODE_SESSION", cfg.SessionID)
	t.Setenv(instructionChunkCountEnv, "2")
	if err := EmitInstructionChunk(io.Discard, "context-chunk-0"); err == nil {
		t.Fatal("accepted stale launch chunk count")
	}
	// Processes launched before the optimization retain the decode fallback.
	t.Setenv(instructionChunkCountEnv, "")
	var out bytes.Buffer
	if err := EmitInstructionChunk(&out, "context-chunk-0"); err != nil || out.String() != "context" {
		t.Fatalf("legacy launch = %q, %v", out.String(), err)
	}
}

func TestHooksReconcileAndUninstallOnlyOwnedCommands(t *testing.T) {
	workspace, data := t.TempDir(), t.TempDir()
	dir := filepath.Join(workspace, ".memcode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hooks.json")
	initial := `{"extra":true,"hooks":{"session_start":[{"command":"echo ao hooks memcode user-command","custom":true},{"command":"ao hooks memcode context-chunk-1","timeout":2}],"post_tool_use":[{"command":"ao hooks memcode post-tool-use"},{"command":"user-post","matcher":"Read"}]}}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		DataDir: data, WorkspacePath: workspace, SessionID: "ao-1", SystemPrompt: "context",
	}); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Extra bool `json:"extra"`
		Hooks map[string][]struct {
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
			Custom  bool   `json:"custom"`
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Hooks["session_start"]) != maxInstructionChunks+1 || !config.Hooks["session_start"][0].Custom {
		t.Fatal("user hook lost or old AO hooks duplicated")
	}
	for _, entry := range config.Hooks["session_start"][1:] {
		if entry.Timeout < 30 {
			t.Fatalf("startup hook timeout = %d, want at least 30s", entry.Timeout)
		}
	}
	if len(config.Hooks["post_tool_use"]) != 1 || config.Hooks["post_tool_use"][0].Matcher != "Read" {
		t.Fatal("no-op AO post-tool hook retained or user hook lost")
	}
	if installed, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || !installed {
		t.Fatal(installed, err)
	}
	if err := p.UninstallHooks(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	if installed, err := p.AreHooksInstalled(context.Background(), workspace); err != nil || installed {
		t.Fatal(installed, err)
	}
	b, _ = os.ReadFile(path)
	if !bytes.Contains(b, []byte("echo ao hooks memcode user-command")) || !bytes.Contains(b, []byte(`"extra": true`)) {
		t.Fatal("uninstall removed user configuration")
	}
}

func TestProjectEndpointRejectsAOModelOverride(t *testing.T) {
	cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), WorkspacePath: t.TempDir(), SessionID: "ao-1",
		Config: ports.AgentConfig{Model: "opus"}, Env: map[string]string{"MEMCODE_ENDPOINT_URL": "http://127.0.0.1:11434/v1"}}
	if err := New().GetAgentHooks(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "custom endpoint") {
		t.Fatalf("project endpoint model override error = %v", err)
	}
	cfg.Config.Model = ""
	if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
		t.Fatalf("native model selection should remain available: %v", err)
	}
}

func BenchmarkInstructionHookSlots(b *testing.B) {
	for _, test := range []struct {
		name       string
		paragraphs int
		legacy     bool
	}{
		{name: "one-chunk/legacy", paragraphs: 1, legacy: true},
		{name: "one-chunk/launch-count", paragraphs: 1},
		{name: "four-chunks/legacy", paragraphs: 1000, legacy: true},
		{name: "four-chunks/launch-count", paragraphs: 1000},
	} {
		b.Run(test.name, func(b *testing.B) {
			cfg := ports.WorkspaceHookConfig{
				DataDir: b.TempDir(), WorkspacePath: b.TempDir(), SessionID: "ao-bench",
				SystemPrompt: strings.Repeat("private context paragraph\n\n", test.paragraphs), Env: map[string]string{},
			}
			if err := New().GetAgentHooks(context.Background(), cfg); err != nil {
				b.Fatal(err)
			}
			b.Setenv("AO_MEMCODE_DATA_DIR", cfg.DataDir)
			b.Setenv("AO_MEMCODE_SESSION", cfg.SessionID)
			for key, value := range cfg.Env {
				b.Setenv(key, value)
			}
			if test.legacy {
				b.Setenv(instructionChunkCountEnv, "")
			}
			events := make([]string, maxInstructionChunks)
			for i := range events {
				events[i] = fmt.Sprintf("context-chunk-%d", i)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, event := range events {
					if err := EmitInstructionChunk(io.Discard, event); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
