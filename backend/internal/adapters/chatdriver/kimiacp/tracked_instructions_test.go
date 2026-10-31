package kimiacp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 7 && os.Args[1] == "chat-host" {
		protocol := persistenthost.ProtocolRaw
		fingerprint := ""
		separator := 5
		if os.Args[5] == string(persistenthost.ProtocolACP) {
			protocol = persistenthost.ProtocolACP
			if len(os.Args) > 6 {
				fingerprint = os.Args[6]
			}
			separator = 7
		}
		if len(os.Args) <= separator || os.Args[separator] != "--" {
			os.Exit(2)
		}
		err := persistenthost.Run(context.Background(), persistenthost.Config{
			SessionID: os.Args[2], DataDir: os.Args[3], Workdir: os.Args[4],
			Env: os.Environ(), Argv: os.Args[separator+1:], Protocol: protocol,
			OwnershipFingerprint: fingerprint,
		})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestKimiHandoffReachesKimiCodeWhenTheProjectCommitsItsInstructions(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		t.Run(fmt.Sprintf("tracked=%v", tracked), func(t *testing.T) {
			workspace := t.TempDir()
			t.Setenv("KIMI_CODE_HOME", t.TempDir())
			path := filepath.Join(workspace, ".kimi-code", "AGENTS.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			const original = "# Project rules\nUse the project's conventions.\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if tracked {
				for _, args := range [][]string{{"init", "-q"}, {"add", ".kimi-code/AGENTS.md"}} {
					cmd := exec.Command("git", args...)
					cmd.Dir = workspace
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("git %v: %v: %s", args, err, out)
					}
				}
			}
			fake := filepath.Join(t.TempDir(), "kimi-repro")
			capture := filepath.Join(t.TempDir(), "requests.jsonl")
			script := `#!/usr/bin/env python3
import sys,json,os
for line in sys.stdin:
    request=json.loads(line)
    with open(os.environ["AO_KIMI_REPRO_CAPTURE"],"a") as out:
        out.write(json.dumps(request)+"\n")
    if "id" not in request: continue
    method=request.get("method")
    result={}
    if method=="initialize": result={"protocolVersion":1,"agentCapabilities":{},"authMethods":[]}
    if method=="session/new": result={"sessionId":"kimi-repro-session"}
    if method=="session/prompt": result={"stopReason":"end_turn"}
    print(json.dumps({"jsonrpc":"2.0","id":request["id"],"result":result}),flush=True)
`
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			driver := New(fakePlugin{binary: fake, status: ports.AgentAuthStatusAuthorized}, nil)
			const note = "<ao-handoff-request>Earlier user: remember PINEAPPLE-42</ao-handoff-request>"
			conv, err := driver.Start(ctx, ports.ChatStartConfig{SessionID: "kimi-repro", DataDir: t.TempDir(), WorkspacePath: workspace, Permissions: ports.PermissionModeDefault, SystemPrompt: note, Env: map[string]string{"AO_KIMI_REPRO_CAPTURE": capture}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conv.(ports.ChatProviderTerminator).Terminate() }()
			ref, err := conv.SendTurn(ctx, ports.ChatUserMessage{Text: "What is the codeword?"})
			if err != nil {
				t.Fatal(err)
			}
			if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case ev := <-conv.Events():
					if ev.Kind == ports.ChatEventTurnCompleted {
						goto settled
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		settled:
			loaded, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tracked && string(loaded) != original {
				t.Fatal("tracked project rules were modified")
			}
			raw, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var channels []string
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var frame struct {
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &frame); err != nil {
					t.Fatal(err)
				}
				if frame.Method == "session/new" || frame.Method == "session/prompt" {
					channels = append(channels, string(frame.Params))
				}
			}
			if !strings.Contains(string(loaded), note) && !strings.Contains(strings.Join(channels, "\n"), "PINEAPPLE-42") {
				t.Fatal("handoff missing from every Kimi Code delivery channel: project instructions, session/new metadata, and first prompt")
			}
		})
	}
}
