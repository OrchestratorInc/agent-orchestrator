package persistenthost

import (
	"encoding/json"
	"testing"
)

const initialACPSettings = `{"sessionId":"live","configOptions":[{"id":"model","currentValue":"haiku"}],"models":{"currentModelId":"haiku"},"modes":{"currentModeId":"plan"}}`

func TestACPRelayReconnectRetainsAcceptedSettings(t *testing.T) {
	for _, tc := range []struct{ name, method, params, result, field, want string }{
		{"rebuilt catalog", "session/set_config_option", `{"sessionId":"live","configId":"model","value":"opus"}`, `{"configOptions":[{"id":"model","currentValue":"opus"},{"id":"effort","currentValue":"high"}]}`, "configOptions", `[{"id":"model","currentValue":"opus"},{"id":"effort","currentValue":"high"}]`},
		{"omitted catalog", "session/set_config_option", `{"sessionId":"live","configId":"model","value":"opus"}`, `{}`, "configOptions", `[{"id":"model","currentValue":"opus"}]`},
		{"empty setter catalog", "session/set_config_option", `{"sessionId":"live","configId":"model","value":"opus"}`, `{"configOptions":[]}`, "configOptions", `[{"id":"model","currentValue":"opus"}]`},
		{"legacy model", "session/set_model", `{"sessionId":"live","modelId":"opus"}`, `{}`, "models", `{"currentModelId":"opus"}`},
		{"legacy mode", "session/set_mode", `{"sessionId":"live","modeId":"default"}`, `{}`, "modes", `{"currentModeId":"default"}`},
		{"boolean", "session/set_config_option", `{"sessionId":"live","configId":"model","value":false}`, `{}`, "configOptions", `[{"id":"model","currentValue":false}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay := settingsRelay(t)
			provider := relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":1,"method":"`+tc.method+`","params":`+tc.params+`}`), 1)
			relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+frameID(t, provider)+`,"result":`+tc.result+`}`), 2, false)
			var setup map[string]json.RawMessage
			if err := json.Unmarshal(relay.snapshot().SessionResult, &setup); err != nil {
				t.Fatal(err)
			}
			assertACPJSON(t, setup[tc.field], tc.want)
		})
	}
}

func TestACPRelaySettingErrorsAndOtherSessionsPreserveSnapshot(t *testing.T) {
	for _, tc := range []struct{ name, session, response string }{
		{"provider error", "live", `"error":{"code":-1,"message":"rejected"}`},
		{"different session", "other", `"result":{"configOptions":[]}`},
		{"malformed result", "live", `"result":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay := settingsRelay(t)
			provider := relayClientFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":1,"method":"session/set_config_option","params":{"sessionId":"`+tc.session+`","configId":"model","value":"opus"}}`), 1)
			relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","id":`+frameID(t, provider)+`,`+tc.response+`}`), 1, true)
			assertACPJSON(t, relay.snapshot().SessionResult, initialACPSettings)
		})
	}
}

func TestACPRelayConfigUpdateRetainsAuthoritativeCatalogDuringPrompt(t *testing.T) {
	relay := settingsRelay(t)
	relay.state.ActivePrompt = true
	before := relay.snapshot()
	relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"other","update":{"sessionUpdate":"config_option_update","configOptions":[]}}}`), 2, false)
	assertACPJSON(t, relay.snapshot().SessionResult, initialACPSettings)
	relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"live","update":{"sessionUpdate":"config_option_update","configOptions":[{"id":"model","currentValue":"opus"},{"id":"effort","currentValue":"high"}]}}}`), 2, false)
	assertACPJSON(t, before.SessionResult, initialACPSettings)
	state := relay.snapshot()
	if !state.ActivePrompt || len(relayReplayFrames(t, relay)) != 2 {
		t.Fatalf("settings update changed prompt replay: %+v", state)
	}
	var setup map[string]json.RawMessage
	if err := json.Unmarshal(state.SessionResult, &setup); err != nil {
		t.Fatal(err)
	}
	assertACPJSON(t, setup["configOptions"], `[{"id":"model","currentValue":"opus"},{"id":"effort","currentValue":"high"}]`)
	relayProviderFrame(t, relay, []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"live","update":{"sessionUpdate":"config_option_update","configOptions":[]}}}`), 2, false)
	if err := json.Unmarshal(relay.snapshot().SessionResult, &setup); err != nil {
		t.Fatal(err)
	}
	assertACPJSON(t, setup["configOptions"], `[]`)
}

func settingsRelay(t *testing.T) *acpRelay {
	t.Helper()
	relay := newTestACPRelay(t)
	relay.state.SessionID = "live"
	relay.state.SessionResult = json.RawMessage(initialACPSettings)
	return relay
}
func assertACPJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	actual, err := canonicalJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := canonicalJSON(json.RawMessage(want))
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("reconnect setup = %s, want %s", actual, expected)
	}
}
