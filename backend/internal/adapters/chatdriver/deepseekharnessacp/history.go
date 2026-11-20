package deepseekharnessacp

import (
	"context"
	"encoding/json"
	"errors"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/deepseekharness"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// DeepSeek's session/resume restores model context but emits no history. Read
// the prepared native event log after that real resume, then use the SAME ACP
// updates and normalizer as live output. Never substitute an archive for a
// failed native resume or inspect the original mutable source on reopen.
func nativeHistoryReplay(ctx context.Context, cfg acpdriver.LaunchConfig, id string) ([]acpsdk.SessionNotification, bool, error) {
	root := cfg.Env["DSH_HOME"]
	if !deepseekharness.IsPreparedImportHome(root) {
		return nil, false, nil
	}
	h := nativeReplay{nodes: make(map[int64]*nativeReplayNode), calls: make(map[string]nativeReplayCall)}
	if err := deepseekharness.VisitPrivateHistory(ctx, root, id, cfg.WorkspacePath, h.observe); err != nil {
		return nil, true, err
	}
	var out []acpsdk.SessionNotification
	for node := h.first; node != nil; node = node.next {
		for _, update := range node.updates {
			if err := ctx.Err(); err != nil {
				return nil, true, err
			}
			wire, err := json.Marshal(map[string]any{"sessionId": id, "update": update})
			if err != nil {
				return nil, true, err
			}
			var notification acpsdk.SessionNotification
			if err := json.Unmarshal(wire, &notification); err != nil {
				return nil, true, err
			}
			out = append(out, notification)
		}
	}
	return out, true, nil
}

type nativeReplayCall struct {
	CallID    string `json:"callId"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type nativeReplayMessage struct {
	ID         string `json:"id"`
	ToolCallID string `json:"toolCallId"`
	IsError    bool   `json:"isError"`
	Source     struct {
		Kind string `json:"kind"`
	} `json:"source"`
	Content []struct {
		Type, Text, ID, Name, Arguments string
	} `json:"content"`
}

type nativeReplayNode struct {
	seq        int64
	updates    []map[string]any
	prev, next *nativeReplayNode
}

type nativeReplay struct {
	first, last *nativeReplayNode
	nodes       map[int64]*nativeReplayNode
	calls       map[string]nativeReplayCall
}

func (h *nativeReplay) observe(e deepseekharness.NativeHistoryEvent) error {
	if e.Type == "tool/call" {
		var call nativeReplayCall
		if err := json.Unmarshal(e.Data, &call); err != nil {
			return err
		}
		if call.CallID == "" {
			return errors.New("deepseek: native tool call has no identity")
		}
		h.calls[call.CallID] = call
		return nil
	}
	if len(e.SurfaceOp) == 0 {
		return nil
	}
	updates, err := h.updates(e)
	if err != nil {
		return err
	}
	node := &nativeReplayNode{seq: e.Seq, updates: updates}
	if string(e.SurfaceOp) == `"append"` {
		node.prev = h.last
		if h.last == nil {
			h.first = node
		} else {
			h.last.next = node
		}
		h.last, h.nodes[e.Seq] = node, node
		return nil
	}
	var op struct {
		Op       string `json:"op"`
		StartSeq *int64 `json:"startSeq"`
		EndSeq   *int64 `json:"endSeq"`
	}
	if err := json.Unmarshal(e.SurfaceOp, &op); err != nil || op.Op != "replace" || op.StartSeq == nil || op.EndSeq == nil || *op.StartSeq > *op.EndSeq {
		return errors.New("deepseek: invalid native replacement")
	}
	start, end := h.nodes[*op.StartSeq], h.nodes[*op.EndSeq]
	if start == nil || end == nil {
		return errors.New("deepseek: native replacement target is missing")
	}
	node.prev, node.next = start.prev, end.next
	found := false
	for old := start; old != nil; old = old.next {
		delete(h.nodes, old.seq)
		if old == end {
			found = true
			break
		}
	}
	if !found {
		return errors.New("deepseek: reversed native replacement")
	}
	if node.prev == nil {
		h.first = node
	} else {
		node.prev.next = node
	}
	if node.next == nil {
		h.last = node
	} else {
		node.next.prev = node
	}
	h.nodes[e.Seq] = node
	return nil
}

func (h *nativeReplay) updates(e deepseekharness.NativeHistoryEvent) ([]map[string]any, error) {
	var m nativeReplayMessage
	if e.Type == "user/message" {
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return nil, err
		}
		if m.Source.Kind != "user" {
			return nil, nil
		}
	} else if e.Type == "assistant/message" || e.Type == "tool/result" {
		var data struct {
			Message nativeReplayMessage `json:"message"`
		}
		if err := json.Unmarshal(e.Data, &data); err != nil {
			return nil, err
		}
		m = data.Message
	} else {
		return nil, nil
	}
	if m.ID == "" {
		return nil, errors.New("deepseek: native message has no identity")
	}
	if e.Type == "tool/result" {
		call, ok := h.calls[m.ToolCallID]
		if !ok {
			return nil, errors.New("deepseek: native tool result has no call")
		}
		var input any
		if json.Unmarshal([]byte(call.Arguments), &input) != nil {
			input = call.Arguments // the native ACP bridge uses this same fallback
		}
		var content []map[string]any
		for _, block := range m.Content {
			if block.Type == "text" {
				content = append(content, map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": block.Text}})
			}
		}
		status := "completed"
		if m.IsError {
			status = "failed"
		}
		return []map[string]any{
			{"sessionUpdate": "tool_call", "toolCallId": call.CallID, "title": call.Name, "kind": "other", "status": "in_progress", "rawInput": input},
			{"sessionUpdate": "tool_call_update", "toolCallId": call.CallID, "status": status, "content": content},
		}, nil
	}
	var updates []map[string]any
	for _, block := range m.Content {
		if block.Type == "tool-call" && e.Type == "assistant/message" {
			// Crash recovery can publish TOOL_NOT_STARTED without a tool/call.
			h.calls[block.ID] = nativeReplayCall{CallID: block.ID, Name: block.Name, Arguments: block.Arguments}
			continue
		}
		if block.Text == "" || (block.Type != "text" && block.Type != "reasoning") {
			continue
		}
		kind := "user_message_chunk"
		if e.Type == "assistant/message" {
			kind = "agent_message_chunk"
			if block.Type == "reasoning" {
				kind = "agent_thought_chunk"
			}
		}
		updates = append(updates, map[string]any{"sessionUpdate": kind, "messageId": m.ID, "content": map[string]string{"type": "text", "text": block.Text}})
	}
	return updates, nil
}
