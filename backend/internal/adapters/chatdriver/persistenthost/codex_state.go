package persistenthost

import (
	"bytes"
	"encoding/json"
)

func (h *host) observeCodexFrameLocked(frame []byte) {
	if h.protocol != ProtocolRaw || h.acp != nil {
		return
	}
	if h.codexRequestID == "" && !bytes.Contains(frame, []byte(`"thread/settings/updated"`)) {
		return
	}
	var value struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
			Model  string `json:"model"`
			Effort string `json:"reasoningEffort"`
		} `json:"result"`
		Params struct {
			ThreadID string `json:"threadId"`
			Settings struct {
				Model  string `json:"model"`
				Effort string `json:"effort"`
			} `json:"threadSettings"`
		} `json:"params"`
	}
	if json.Unmarshal(frame, &value) != nil {
		return
	}
	if h.codexRequestID != "" && string(value.ID) == h.codexRequestID && value.Method == "" {
		h.codexRequestID = ""
		if value.Result.Thread.ID != "" && value.Result.Model != "" {
			h.codex = &CodexState{ThreadID: value.Result.Thread.ID, Model: value.Result.Model, Effort: value.Result.Effort}
		}
	}
	if value.Method == "thread/settings/updated" && h.codex != nil && value.Params.ThreadID == h.codex.ThreadID && value.Params.Settings.Model != "" {
		h.codex = &CodexState{ThreadID: value.Params.ThreadID, Model: value.Params.Settings.Model, Effort: value.Params.Settings.Effort}
	}
}

func (h *host) observeCodexClientFrameLocked(frame []byte) {
	if h.protocol != ProtocolRaw || h.acp != nil {
		return
	}
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(frame, &request) == nil && len(request.ID) > 0 && (request.Method == "thread/start" || request.Method == "thread/resume") {
		h.codexRequestID = string(request.ID)
	}
}
