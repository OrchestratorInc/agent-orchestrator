package sessionimport

import (
	"context"
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
