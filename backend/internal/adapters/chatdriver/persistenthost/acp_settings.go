package persistenthost

import "encoding/json"

func (r *acpRelay) captureSessionSetting(request acpClientRequest, result json.RawMessage) {
	var params struct {
		SessionID string          `json:"sessionId"`
		ConfigID  string          `json:"configId"`
		Value     json.RawMessage `json:"value"`
		ModelID   json.RawMessage `json:"modelId"`
		ModeID    json.RawMessage `json:"modeId"`
	}
	if json.Unmarshal(request.params, &params) != nil || params.SessionID == "" || params.SessionID != r.state.SessionID {
		return
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(result, &response) != nil || response == nil {
		return
	}
	var options []json.RawMessage
	if raw := response["configOptions"]; json.Unmarshal(raw, &options) == nil && len(options) > 0 {
		r.replaceSessionConfigOptions(raw)
		return
	}
	var setup map[string]json.RawMessage
	if json.Unmarshal(r.state.SessionResult, &setup) != nil || setup == nil {
		return
	}
	id, value := params.ConfigID, params.Value
	switch request.method {
	case "session/set_model":
		id, value = "model", params.ModelID
		updateACPCurrent(setup, "models", "currentModelId", value)
	case "session/set_mode":
		id, value = "mode", params.ModeID
		updateACPCurrent(setup, "modes", "currentModeId", value)
	}
	if id == "" || !json.Valid(value) {
		return
	}
	if json.Unmarshal(setup["configOptions"], &options) == nil {
		for i, raw := range options {
			var option map[string]json.RawMessage
			var optionID string
			if json.Unmarshal(raw, &option) != nil || json.Unmarshal(option["id"], &optionID) != nil || optionID != id {
				continue
			}
			option["currentValue"] = value
			options[i], _ = json.Marshal(option)
		}
		setup["configOptions"], _ = json.Marshal(options)
	}
	r.state.SessionResult, _ = json.Marshal(setup)
}

func (r *acpRelay) captureSessionUpdate(method string, raw json.RawMessage) {
	if method != "session/update" {
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind          string          `json:"sessionUpdate"`
			ConfigOptions json.RawMessage `json:"configOptions"`
		} `json:"update"`
	}
	if json.Unmarshal(raw, &params) != nil || params.SessionID == "" || params.SessionID != r.state.SessionID || params.Update.Kind != "config_option_update" {
		return
	}
	var options []json.RawMessage
	if json.Unmarshal(params.Update.ConfigOptions, &options) != nil || string(params.Update.ConfigOptions) == "null" {
		return
	}
	r.replaceSessionConfigOptions(params.Update.ConfigOptions)
}

func (r *acpRelay) replaceSessionConfigOptions(options json.RawMessage) {
	var setup map[string]json.RawMessage
	if json.Unmarshal(r.state.SessionResult, &setup) != nil || setup == nil {
		return
	}
	setup["configOptions"] = options
	r.state.SessionResult, _ = json.Marshal(setup)
}

func updateACPCurrent(setup map[string]json.RawMessage, field, current string, value json.RawMessage) {
	if !json.Valid(value) {
		return
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(setup[field], &state) != nil || state == nil {
		return
	}
	state[current] = value
	setup[field], _ = json.Marshal(state)
}
