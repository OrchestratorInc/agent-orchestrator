package minimaxcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSubmitAcceptanceFollowsStandingContext(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the MiniMax hook contract")
	}
	t.Setenv("MINIMAX_DATA_DIR", t.TempDir())
	const nativeID = "mvs_01234567890123456789012345678901"
	prompt := domain.WrapSessionDelivery("session-send:ordering", "orchestrator direction")
	preload := filepath.Join(t.TempDir(), "acceptance.cjs")
	fixture := `const fs=require('node:fs');
const path=require('node:path');
const child=require('node:child_process');
const mode=process.env.FIXTURE_MODE;
const read=fs.readFileSync;
fs.readFileSync=function(file,...args){
  if(mode==='unreadable' && path.basename(String(file))==='standing.txt') throw new Error('unreadable context');
  return read.call(this,file,...args);
};
let contextFlushed=false;
const write=process.stdout.write;
process.stdout.write=function(chunk,...args){
  if(!String(chunk).includes('"additionalContext"')) return write.call(this,chunk,...args);
  if(mode==='write-error') throw new Error('context output failed');
  const done=args.pop();
  const finish=(error)=>{contextFlushed=!error; done(error);};
  if(mode==='write-callback-error') { process.nextTick(()=>finish(new Error('context output failed'))); return true; }
  return write.call(this,chunk,...args,finish);
};
child.spawnSync=(binary,args,options)=>{
  fs.appendFileSync(process.env.FIXTURE_REPORT,JSON.stringify({args,prompt:JSON.parse(options.input).prompt,contextFlushed})+'\n');
  return {status:0};
};`
	if err := os.WriteFile(preload, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "large", "repeated", "empty", "missing", "oversized", "directory", "unreadable", "write-error", "write-callback-error", "missing-after-first"} {
		t.Run(mode, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "prompt.txt")
			if err := os.WriteFile(file, []byte("private standing context"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := ports.WorkspaceHookConfig{DataDir: t.TempDir(), SessionID: "acceptance", WorkspacePath: t.TempDir(), SystemPromptFile: file}
			cfg.Env = map[string]string{"AO_DATA_DIR": cfg.DataDir, "AO_SESSION_ID": cfg.SessionID, "AO_RUNTIME_LAUNCH_ID": "current"}
			plugin := New()
			if err := plugin.GetAgentHooks(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			if err := plugin.PrepareRuntimeLaunch(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			profile, _ := profilePath(cfg.DataDir, cfg.SessionID)
			if err := os.WriteFile(filepath.Join(profile, "ao-native-id"), []byte(nativeID), 0o600); err != nil {
				t.Fatal(err)
			}
			standing := filepath.Join(profile, "launches", "current", "standing.txt")
			switch mode {
			case "missing", "directory":
				if err := os.Remove(standing); err != nil {
					t.Fatal(err)
				}
				if mode == "directory" {
					if err := os.Mkdir(standing, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "oversized", "large", "empty":
				text := ""
				switch mode {
				case "oversized":
					text = strings.Repeat("x", maxStandingBytes+1)
				case "large":
					text = strings.Repeat("x", maxStandingBytes)
				}
				if err := os.WriteFile(standing, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			turns := 1
			if mode == "repeated" || mode == "missing-after-first" {
				turns = 2
			}
			for turn := range turns {
				if mode == "missing-after-first" && turn == 1 {
					if err := os.Remove(standing); err != nil {
						t.Fatal(err)
					}
				}
				reportFile := filepath.Join(t.TempDir(), "report.jsonl")
				cmd := exec.Command(node, "--require", preload, filepath.Join(profile, "plugins", "ao-activity", "hook.cjs"), hookLaunchArgument(profile, "current"))
				cmd.Env = append(os.Environ(), "FIXTURE_MODE="+mode, "FIXTURE_REPORT="+reportFile)
				payload, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": nativeID, "prompt": prompt})
				if err != nil {
					t.Fatal(err)
				}
				cmd.Stdin = strings.NewReader(string(payload))
				output, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				var decision map[string]any
				if len(output) != 0 {
					if err := json.Unmarshal(output, &decision); err != nil {
						t.Fatalf("invalid hook output %q: %v", output, err)
					}
				}
				accepted := mode == "valid" || mode == "large" || mode == "repeated" || mode == "empty" || mode == "missing-after-first" && turn == 0
				report, err := os.ReadFile(reportFile)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				if !accepted {
					if decision["continue"] != false || len(report) != 0 {
						t.Fatalf("refused turn %d reported semantic acceptance: output=%s report=%s", turn, output, report)
					}
					continue
				}
				var signal struct {
					Args           []string `json:"args"`
					Prompt         string   `json:"prompt"`
					ContextFlushed bool     `json:"contextFlushed"`
				}
				if err := json.Unmarshal(report, &signal); err != nil {
					t.Fatalf("expected exactly one acceptance report: %q, %v", report, err)
				}
				wantContext := turn == 0 && mode != "empty"
				if strings.Join(signal.Args, "/") != "hooks/minimax-code/user-prompt-submit" || signal.Prompt != prompt || signal.ContextFlushed != wantContext || decision["continue"] != nil {
					t.Fatalf("incorrect acceptance ordering: report=%s output=%s", report, output)
				}
				if (decision["hookSpecificOutput"] != nil) != wantContext {
					t.Fatalf("turn %d repeated or omitted context: %s", turn, output)
				}
			}
		})
	}
}
