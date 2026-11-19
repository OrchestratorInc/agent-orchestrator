package sessionimport

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A thread continued through another Codex client or version mixes record
// formats: an earlier turn written only as response items must survive a later
// turn that also writes events, and an event-only reply must survive a later
// reply written as a response item.
func TestCodexMixedFormatsKeepEveryTurn(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-09-01T00-00-00-"+codexThread+".jsonl"), `{"type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"/work/repo","source":"cli"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/repo"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first prompt, response only"}]}}
{"type":"event_msg","payload":{"type":"agent_message","message":"first answer, event only"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/work/repo</cwd>\n</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second prompt"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"second prompt"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","id":"msg_2","content":[{"type":"output_text","text":"second answer"}]}}
{"type":"event_msg","payload":{"type":"agent_message","message":"second answer"}}
`)
	got, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: first prompt, response only", "assistant: first answer, event only", "user: second prompt", "assistant: second answer")
	if got.title != "first prompt, response only" || got.shortened {
		t.Fatalf("title=%q shortened=%v", got.title, got.shortened)
	}
}

func TestCodexRollbackAcrossSegmentsRetractsMixedRepresentations(t *testing.T) {
	root := t.TempDir()
	first := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-09-01T00-00-00-"+codexThread+".jsonl"), `{"type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"/work/repo","source":"cli"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":"keep prompt"}}
{"type":"event_msg","payload":{"type":"user_message","message":"keep prompt"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"keep reply"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":"edited prompt"}}
{"type":"event_msg","payload":{"type":"user_message","message":"edited prompt"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","id":"reused","content":"retracted reply"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"retracted reply"}}
`)
	second := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-09-02T00-00-00-"+codexThread+".jsonl"), `{"type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":1}}
{"type":"event_msg","payload":{"type":"user_message","message":"replacement prompt"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","id":"reused","content":"replacement reply"}}
`)
	got, err := readTranscript(context.Background(), domain.HarnessCodex, root, first, second)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: keep prompt", "assistant: keep reply", "user: replacement prompt", "assistant: replacement reply")
}

func TestCodexRollbackCountsUserTurnsWithoutReplies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		turns uint64
		want  []string
	}{
		{"zero", 0, []string{"user: first", "assistant: first reply", "user: pending", "user: replacement", "assistant: replacement reply"}},
		{"pending", 1, []string{"user: first", "assistant: first reply", "user: replacement", "assistant: replacement reply"}},
		{"all", 2, []string{"user: replacement", "assistant: replacement reply"}},
		{"excessive", 100, []string{"user: replacement", "assistant: replacement reply"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-09-01T00-00-00-"+codexThread+".jsonl"), fmt.Sprintf(`{"type":"session_meta","payload":{"id":"%s","cwd":"/work/repo","source":"cli"}}
{"type":"event_msg","payload":{"type":"user_message","message":"first"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"first reply"}}
{"type":"event_msg","payload":{"type":"user_message","message":"pending"}}
{"type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":%d}}
{"type":"event_msg","payload":{"type":"user_message","message":"replacement"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"replacement reply"}}
`, codexThread, tc.turns))
			got, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
			if err != nil {
				t.Fatal(err)
			}
			wantTexts(t, got.messages, tc.want...)
		})
	}
}
