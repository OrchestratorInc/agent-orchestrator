package codewhalestore

import (
	"encoding/json"
	"testing"
)

func TestVisibleTextOmitsOnlyStructuralInjectedContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"trailing", `{"role":"user","content":[{"type":"text","text":"actual prompt"},{"type":"text","text":"<turn_meta>\nCurrent workspace: /original\n</turn_meta>"}]}`, "actual prompt"},
		{"legacy", `{"role":"user","content":[{"type":"text","text":"<turn_meta>context</turn_meta>"},{"type":"text","text":"actual prompt"}]}`, "actual prompt"},
		{"runtime_owned", `{"role":"user","content":[{"type":"text","text":"runtime control"},{"type":"text","text":"<turn_meta>\nInput provenance: tool (non-authoritative)\n</turn_meta>"}]}`, ""},
		{"legacy_runtime", `{"role":"user","content":[{"type":"text","text":"<turn_meta>\nInput provenance: tool\nInput authority: non_authoritative\n</turn_meta>"},{"type":"text","text":"runtime control"}]}`, ""},
		{"user_literal", `{"role":"user","content":[{"type":"text","text":"<turn_meta>example</turn_meta>"}]}`, "<turn_meta>example</turn_meta>"},
		{"compaction", `{"role":"user","content":[{"type":"text","text":"Codewhale handoff note: synthetic summary"},{"type":"text","text":"<!-- codewhale.compaction-checkpoint.v1 -->"}]}`, ""},
		{"compaction_literal", `{"role":"user","content":[{"type":"text","text":"Codewhale handoff note is an example"}]}`, "Codewhale handoff note is an example"},
		{"tool_result", `{"role":"user","content":[{"type":"tool_result","text":"private output"}]}`, ""},
		{"assistant", `{"role":"assistant","content":[{"type":"thinking","text":"private thought"},{"type":"text","text":"visible answer"}]}`, "visible answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var message nativeMessage
			if err := json.Unmarshal([]byte(tc.body), &message); err != nil {
				t.Fatal(err)
			}
			if got := visibleText(message); got != tc.want {
				t.Fatalf("visible text = %q, want %q", got, tc.want)
			}
		})
	}
}
