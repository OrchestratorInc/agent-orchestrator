package memcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if !config.Extra || len(config.Hooks["session_start"]) != 2 || len(config.Hooks["session_end"]) != 1 {
		t.Fatal(string(b))
	}
	if bytes.Contains(b, []byte("PRIVATE")) {
		t.Fatal("standing context leaked to workspace hook config")
	}
	t.Setenv("AO_MEMCODE_DATA_DIR", data)
	t.Setenv("AO_MEMCODE_SESSION", "ao-1")
	t.Setenv("AO_MEMCODE_GENERATION", "launch-1")
	t.Setenv("MEMCODE_SESSION_ID", "sess_b1b1b1b1")
	var out bytes.Buffer
	if err := EmitStartWitness(&out); err != nil {
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
	if err := EmitStartWitness(&out); err != nil {
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
				if err := os.WriteFile(filepath.Join(dir, "instructions-0.txt"), []byte("context"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.failWrite {
				out = failingWriter{}
			}
			if err := EmitStartWitness(out); err == nil {
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
	if len(config.Hooks["session_start"]) != 1 || len(config.Hooks["pre_tool_use"]) != 1 || len(config.Hooks["post_tool_use"]) != 1 {
		t.Fatal(string(b))
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
