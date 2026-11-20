package deepseekharnessacp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

func replayEvent(seq int64, kind, data, op string) deepseekharness.NativeHistoryEvent {
	return deepseekharness.NativeHistoryEvent{Seq: seq, Type: kind, Data: json.RawMessage(data), SurfaceOp: json.RawMessage(op)}
}

func TestNativeReplayKeepsMessagesReasoningAndRealToolHistory(t *testing.T) {
	h := nativeReplay{nodes: make(map[int64]*nativeReplayNode), calls: make(map[string]nativeReplayCall)}
	for _, e := range []deepseekharness.NativeHistoryEvent{
		replayEvent(0, "user/message", `{"id":"u0","source":{"kind":"user"},"content":[{"type":"text","text":"run pwd"}]}`, `"append"`),
		replayEvent(1, "user/message", `{"id":"hidden","source":{"kind":"runtime-context"},"content":[{"type":"text","text":"private runtime prompt"}]}`, `"append"`),
		replayEvent(2, "assistant/message", `{"message":{"id":"a2","content":[{"type":"reasoning","text":"checking cwd"},{"type":"text","text":"running shell"}]}}`, `"append"`),
		replayEvent(3, "tool/call", `{"callId":"call3","name":"bash","arguments":"{\"command\":\"pwd\"}"}`, ""),
		replayEvent(4, "tool/result", `{"message":{"id":"result4","toolCallId":"call3","content":[{"type":"text","text":"/ao/worktree"}]}}`, `"append"`),
	} {
		if err := h.observe(e); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.first.updates) != 1 || h.first.updates[0]["messageId"] != "u0" || len(h.first.next.updates) != 0 {
		t.Fatal("user identity or hidden runtime filtering changed")
	}
	assistant := h.first.next.next.updates
	if len(assistant) != 2 || assistant[0]["sessionUpdate"] != "agent_thought_chunk" || assistant[1]["messageId"] != "a2" {
		t.Fatalf("assistant replay = %#v", assistant)
	}
	tool := h.last.updates
	if len(tool) != 2 || tool[0]["toolCallId"] != "call3" || tool[1]["status"] != "completed" {
		t.Fatalf("tool replay = %#v", tool)
	}
	input, ok := tool[0]["rawInput"].(map[string]any)
	if !ok || input["command"] != "pwd" {
		t.Fatalf("tool input = %#v", tool[0]["rawInput"])
	}
}

func TestNativeReplayFoldsExplicitZeroReplacementAndRejectsIncompleteRanges(t *testing.T) {
	for _, op := range []string{`{"op":"replace","startSeq":0,"endSeq":0}`, `{"op":"replace","endSeq":0}`, `{"op":"replace","startSeq":null,"endSeq":0}`, `{"op":"replace","startSeq":0,"endSeq":7}`} {
		h := nativeReplay{nodes: make(map[int64]*nativeReplayNode), calls: make(map[string]nativeReplayCall)}
		if err := h.observe(replayEvent(0, "user/message", `{"id":"u0","source":{"kind":"user"},"content":[{"type":"text","text":"before"}]}`, `"append"`)); err != nil {
			t.Fatal(err)
		}
		err := h.observe(replayEvent(1, "user/message", `{"id":"u1","source":{"kind":"user"},"content":[{"type":"text","text":"after"}]}`, op))
		if op == `{"op":"replace","startSeq":0,"endSeq":0}` {
			if err != nil || h.first != h.last || h.first.seq != 1 || h.nodes[0] != nil {
				t.Fatalf("explicit zero replacement failed: %v", err)
			}
		} else if err == nil {
			t.Fatalf("accepted incomplete replacement %s", op)
		}
	}
}

func TestNativeReplayDoesNotReadOrdinaryHome(t *testing.T) {
	updates, available, err := nativeHistoryReplay(context.Background(), acpdriver.LaunchConfig{Env: map[string]string{"DSH_HOME": t.TempDir()}}, "native-id")
	if err != nil || available || len(updates) != 0 {
		t.Fatalf("ordinary resume acquired invented history: %v, %v", available, err)
	}
}
