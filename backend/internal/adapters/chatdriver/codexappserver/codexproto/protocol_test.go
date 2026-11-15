package codexproto

import (
	"encoding/json"
	"testing"
)

func TestRawMessageAliasesDecodeEveryJSONKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"number", `{"requestId":4}`},
		{"string", `{"requestId":"req-4"}`},
		{"object", `{"requestId":{"nested":true}}`},
		{"null", `{"requestId":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				RequestID RequestID `json:"requestId"`
			}
			if err := json.Unmarshal([]byte(tc.body), &got); err != nil {
				t.Fatalf("decode %s: %v\nRequestID must be an alias to json.RawMessage, "+
					"not a defined type over it", tc.body, err)
			}
		})
	}
}

func TestGeneratedSurfaceIsPopulated(t *testing.T) {
	if len(Methods) == 0 {
		t.Fatal("no methods generated")
	}
	if ProviderVersion == "" || ProviderVersion == "unknown" {
		t.Errorf("ProviderVersion = %q; the generated file cannot say which build it describes", ProviderVersion)
	}
	if len(ProtocolDigest) == 0 {
		t.Error("ProtocolDigest is empty")
	}

	for _, m := range Methods {
		if m.Direction == "" {
			t.Errorf("%s has no direction", m.Name)
		}
		if m.Result != "" && m.Direction != "ClientRequest" && m.Direction != "ServerRequest" {
			t.Errorf("%s is a %s yet names result %q", m.Name, m.Direction, m.Result)
		}
	}

	if !Declares(MethodThreadStart) {
		t.Error("Declares() does not recognize thread/start")
	}
	if Declares("thread/definitelyNotAMethod") {
		t.Error("Declares() accepted a method that does not exist")
	}
}
