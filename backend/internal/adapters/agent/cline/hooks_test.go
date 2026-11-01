package cline

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func TestClineHookWrappersOverrideSharedHubEnvironment(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	// Shell metacharacters and quotes in real paths must remain literal.
	dir := filepath.Join(t.TempDir(), "owner's $data `dir` \u4e16\u754c")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "ao")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	build := process.CommandContext(t.Context(), goBinary, "build", "-o", executable, "./testdata/hook-dispatcher")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build dispatcher fixture: %v\n%s", err, out)
	}
	for _, shell := range []string{"bash", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			shellPath, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s unavailable: %v", shell, err)
			}
			for _, key := range []string{"AO_SESSION_ID", "AO_RUNTIME_LAUNCH_ID", "AO_DATA_DIR", "AO_RUN_FILE", "AO_REVIEW_SESSION_ID", "AO_REVIEW_WORKER_SESSION_ID"} {
				t.Setenv(key, "wrong-hub-owner")
			}
			t.Setenv("CLINE_TEST_PROVIDER_ENV", "provider-env-kept")
			capturePath := filepath.Join(dir, "capture.json")
			t.Setenv("CLINE_TEST_CAPTURE", capturePath)
			for _, sessionID := range []string{"cl-4", "cl-5"} {
				cfg := ports.WorkspaceHookConfig{
					SessionID: sessionID, DataDir: dir,
					Env: map[string]string{"AO_RUNTIME_LAUNCH_ID": "launch-" + sessionID, "AO_RUN_FILE": filepath.Join(dir, "running.json"), "SECRET": "must-not-be-written"},
				}
				for _, event := range []string{"session-start", "permission-resolved", "stop"} {
					script := renderBashClineHookScript(event, filepath.ToSlash(executable), cfg)
					scriptPath := filepath.Join(dir, "hook")
					args := []string{filepath.ToSlash(scriptPath)}
					if shell == "powershell" {
						script = renderPowerShellClineHookScript(event, executable, cfg)
						scriptPath += ".ps1"
						args = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
					}
					if strings.Contains(script, "must-not-be-written") {
						t.Fatal("wrapper leaked unrelated launch environment")
					}
					if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
						t.Fatal(err)
					}
					payload := `{"sessionContext":{"rootSessionId":"native-` + sessionID + `"},"text":"hello 世界"}`
					cmd := process.CommandContext(t.Context(), shellPath, args...)
					cmd.Stdin = strings.NewReader(payload)
					out, err := cmd.CombinedOutput()
					if err != nil || strings.TrimSpace(string(out)) != `{"cancel": false}` {
						t.Fatalf("%s/%s continuation: %v\n%s", sessionID, event, err, out)
					}
					data, err := os.ReadFile(capturePath)
					if err != nil {
						t.Fatal(err)
					}
					var got struct {
						Args    []string
						Env     map[string]string
						Payload string
					}
					if err := json.Unmarshal(data, &got); err != nil {
						t.Fatal(err)
					}
					wantEnv := map[string]string{
						"AO_SESSION_ID": sessionID, "AO_RUNTIME_LAUNCH_ID": "launch-" + sessionID,
						"AO_DATA_DIR": dir, "AO_RUN_FILE": filepath.Join(dir, "running.json"),
						"AO_REVIEW_SESSION_ID": "", "AO_REVIEW_WORKER_SESSION_ID": "", "CLINE_TEST_PROVIDER_ENV": "provider-env-kept",
					}
					if !reflect.DeepEqual(got.Env, wantEnv) || !reflect.DeepEqual(got.Args, []string{"hooks", "cline", event}) || got.Payload != payload {
						t.Fatalf("%s/%s wrong callback: %+v", sessionID, event, got)
					}
				}
			}
		})
	}
}

func TestClinePrepareRuntimeLaunchRefreshesManagedHooks(t *testing.T) {
	p := New()
	cfg := ports.WorkspaceHookConfig{
		SessionID: "cl-5", WorkspacePath: t.TempDir(), DataDir: t.TempDir(),
		Env: map[string]string{"AO_RUNTIME_LAUNCH_ID": "launch-old", "AO_RUN_FILE": "owner-running.json"},
	}
	if err := p.GetAgentHooks(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(clineHooksDir(cfg.WorkspacePath), clineHookScriptName("TaskCancel"))
	userHook := []byte("# user-owned hook\n")
	if err := os.WriteFile(userPath, userHook, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.Env["AO_RUNTIME_LAUNCH_ID"] = "launch-new"
	if err := p.PrepareRuntimeLaunch(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, spec := range clineManagedHooks {
		data, err := os.ReadFile(filepath.Join(clineHooksDir(cfg.WorkspacePath), clineHookScriptName(spec.Event)))
		if err != nil {
			t.Fatal(err)
		}
		if spec.Event == "TaskCancel" {
			if !bytes.Equal(data, userHook) {
				t.Fatal("launch preparation overwrote user hook")
			}
			continue
		}
		if !bytes.Contains(data, []byte("launch-new")) || bytes.Contains(data, []byte("launch-old")) || !bytes.Contains(data, []byte("cl-5")) {
			t.Fatalf("%s did not refresh owner generation:\n%s", spec.Event, data)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.PrepareRuntimeLaunch(ctx, cfg); err == nil {
		t.Fatal("launch preparation ignored cancellation")
	}
}
