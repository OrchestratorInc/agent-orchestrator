package mcpserver

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConnectorListSaysWhichConnectorsThisSessionHas(t *testing.T) {
	startFakeDaemon(t, map[string]func() (int, string){
		"GET /api/v1/connectors": fixed(http.StatusOK, `{"connectors":[
			{"id":"c1","key":"linear","name":"Linear","transport":"http","url":"https://mcp.linear.app/mcp","status":"connected","enabled":true,"headerKeys":["Authorization"]},
			{"id":"c2","key":"paper","name":"paper","transport":"stdio","command":"npx","status":"unchecked","enabled":true,"projectId":"other"}]}`),
		"GET /api/v1/sessions/ao-1/connectors": fixed(http.StatusOK, `{"connectors":[{"key":"linear","name":"Linear","status":"connected","enabled":true,"calls":3}]}`),
	})
	text, isError := callTool(t, "connector_list", nil)
	if isError || !strings.Contains(text, `"key":"linear"`) || !strings.Contains(text, `"inThisSession":true`) ||
		!strings.Contains(text, `"key":"paper"`) || !strings.Contains(text, `"inThisSession":false`) {
		t.Fatalf("connector_list = %s", text)
	}
	if strings.Contains(text, "Authorization") || strings.Contains(text, "mcp.linear.app") {
		t.Fatalf("connector_list gave more than names and states: %s", text)
	}
}

func TestConnectorProposeWaitsForTheUsersAnswer(t *testing.T) {
	var polls atomic.Int32
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"POST /api/v1/connectors/proposals": fixed(http.StatusCreated, `{"proposal":{"id":"p1","key":"sentry","state":"pending"}}`),
		"GET /api/v1/connectors/proposals/p1": func() (int, string) {
			if polls.Add(1) < 2 {
				return http.StatusOK, `{"proposal":{"id":"p1","key":"sentry","state":"pending"}}`
			}
			return http.StatusOK, `{"proposal":{"id":"p1","key":"sentry","state":"approved","connectorKey":"sentry"}}`
		},
	})
	text, isError := callTool(t, "connector_propose", map[string]any{
		"key": "sentry", "url": "https://mcp.sentry.dev/mcp", "reason": "To read the crash report.",
	})
	if isError || !strings.Contains(text, `"state":"approved"`) || !strings.Contains(text, `"connectorKey":"sentry"`) ||
		!strings.Contains(text, "This session keeps the tools it started with") {
		t.Fatalf("connector_propose = %s", text)
	}
	sent := daemon.last(t, http.MethodPost, "/api/v1/connectors/proposals").body
	for _, want := range []string{`"sessionId":"ao-1"`, `"transport":"http"`, `"url":"https://mcp.sentry.dev/mcp"`, `"reason":"To read the crash report."`} {
		if !strings.Contains(sent, want) {
			t.Errorf("proposal body %s lacks %s", sent, want)
		}
	}
	if polls.Load() < 2 {
		t.Fatalf("polls = %d", polls.Load())
	}
}

func TestConnectorProposeSendsACommandWithValueNamesOnlyAndReportsADecline(t *testing.T) {
	daemon := startFakeDaemon(t, map[string]func() (int, string){
		"POST /api/v1/connectors/proposals": fixed(http.StatusCreated, `{"proposal":{"id":"p2","key":"paper","state":"declined"}}`),
	})
	text, isError := callTool(t, "connector_propose", map[string]any{
		"key": "paper", "command": "npx", "args": []any{"-y", "paper-mcp"}, "envKeys": []any{"PAPER_KEY"}, "reason": "To read the design file.",
	})
	if isError || !strings.Contains(text, `"state":"declined"`) || !strings.Contains(text, "Do not ask again") {
		t.Fatalf("connector_propose = %s", text)
	}
	sent := daemon.last(t, http.MethodPost, "/api/v1/connectors/proposals").body
	for _, want := range []string{`"transport":"stdio"`, `"command":"npx"`, `"args":["-y","paper-mcp"]`, `"envKeys":["PAPER_KEY"]`} {
		if !strings.Contains(sent, want) {
			t.Errorf("proposal body %s lacks %s", sent, want)
		}
	}
}

func TestConnectorProposeGivesTheDaemonsRefusal(t *testing.T) {
	startFakeDaemon(t, map[string]func() (int, string){
		"POST /api/v1/connectors/proposals": fixed(http.StatusConflict, `{"message":"A connector with the key sentry already exists","code":"CONNECTOR_KEY_EXISTS"}`),
	})
	text, isError := callTool(t, "connector_propose", map[string]any{"key": "sentry", "url": "https://mcp.sentry.dev/mcp", "reason": "x"})
	if !isError || !strings.Contains(text, "already exists") {
		t.Fatalf("connector_propose = %s (error %v)", text, isError)
	}
}

func TestAWaitForTheUsersAnswerDoesNotHoldTheOtherCalls(t *testing.T) {
	for name, want := range map[string]mcpLineKind{"session_wait": mcpLineWait, "connector_propose": mcpLineWait, "connector_list": mcpLineOther} {
		id, kind := classifyMCPLine([]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"` + name + `"}}`))
		if kind != want || (want == mcpLineWait && id != "4") {
			t.Errorf("%s = %q, %v", name, id, kind)
		}
	}
}
