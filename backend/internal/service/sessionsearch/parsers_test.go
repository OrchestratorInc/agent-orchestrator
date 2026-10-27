package sessionsearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// These tests call the transcript parsers directly. The integration tests cover
// the same formats end to end, but a parser is where a provider's shape is
// interpreted, and a format change should fail here with a precise message
// rather than as "the search found nothing".

func TestParseClaudeTranscriptLine(t *testing.T) {
	t.Run("user and assistant prose", func(t *testing.T) {
		units := parseClaudeTranscriptLine([]byte(`{"type":"user","message":{"role":"user",` +
			`"content":"why is the build failing"},"timestamp":"2026-08-09T12:00:00Z"}`))
		if len(units) != 1 || units[0].kind != domain.SessionSearchKindUser || units[0].text != "why is the build failing" {
			t.Fatalf("units = %+v", units)
		}
		if !units[0].at.Equal(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("timestamp = %v", units[0].at)
		}

		units = parseClaudeTranscriptLine([]byte(`{"type":"assistant","message":{"role":"assistant",` +
			`"content":[{"type":"text","text":"the linker is missing a symbol"}]},"timestamp":"2026-08-09T12:00:01Z"}`))
		if len(units) != 1 || units[0].kind != domain.SessionSearchKindAssistant {
			t.Fatalf("units = %+v", units)
		}
	})

	t.Run("tool call and tool result", func(t *testing.T) {
		call := parseClaudeTranscriptLine([]byte(`{"type":"assistant","message":{"role":"assistant",` +
			`"content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]}}`))
		if len(call) != 1 || call[0].kind != domain.SessionSearchKindTool {
			t.Fatalf("tool call units = %+v", call)
		}
		if call[0].text == "" || !strings.Contains(call[0].text, "go build ./...") {
			t.Fatalf("tool call text lost its arguments: %q", call[0].text)
		}

		result := parseClaudeTranscriptLine([]byte(`{"type":"user","message":{"role":"user",` +
			`"content":[{"type":"tool_result","content":"undefined symbol zzNeedle"}]}}`))
		if len(result) != 1 || result[0].kind != domain.SessionSearchKindCommand {
			t.Fatalf("tool result units = %+v", result)
		}
		if !strings.Contains(result[0].text, "zzNeedle") {
			t.Fatalf("tool output text = %q", result[0].text)
		}
	})

	t.Run("thinking is agent-authored", func(t *testing.T) {
		units := parseClaudeTranscriptLine([]byte(`{"type":"assistant","message":{"role":"assistant",` +
			`"content":[{"type":"thinking","thinking":"the retry loop never resets its counter"}]}}`))
		if len(units) != 1 || units[0].kind != domain.SessionSearchKindAssistant {
			t.Fatalf("units = %+v", units)
		}
	})

	t.Run("sidechain and meta records are skipped", func(t *testing.T) {
		for _, line := range []string{
			`{"type":"user","isSidechain":true,"message":{"role":"user","content":"subagent chatter"}}`,
			`{"type":"user","isMeta":true,"message":{"role":"user","content":"caveat metadata"}}`,
			`{"type":"file-history-snapshot","messageId":"x"}`,
			`{"type":"mode","mode":"normal"}`,
		} {
			if units := parseClaudeTranscriptLine([]byte(line)); len(units) != 0 {
				t.Fatalf("%s produced %+v", line, units)
			}
		}
	})

	t.Run("malformed line is skipped, not fatal", func(t *testing.T) {
		for _, line := range []string{``, `{`, `not json`, `{"type":"user"}`} {
			if units := parseClaudeTranscriptLine([]byte(line)); len(units) != 0 {
				t.Fatalf("%q produced %+v", line, units)
			}
		}
	})
}

func TestParseCodexTranscriptLine(t *testing.T) {
	// The item stream (newer Codex) and the response_item stream (every Codex)
	// describe the same turn; a rollout is read from exactly one of them.
	userItem := `{"timestamp":"2026-08-09T12:00:00Z","type":"event_msg",` +
		`"payload":{"type":"item_completed","item":{"type":"UserMessage",` +
		`"content":[{"type":"text","text":"why does the reaper leak goroutines"}]}}}`
	userResponse := `{"timestamp":"2026-08-09T12:00:00Z","type":"response_item",` +
		`"payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does the reaper leak goroutines"}]}}`

	t.Run("item stream messages and commands", func(t *testing.T) {
		user := parseCodexTranscriptLine([]byte(userItem), true)
		if len(user) != 1 || user[0].kind != domain.SessionSearchKindUser {
			t.Fatalf("user units = %+v", user)
		}
		agent := parseCodexTranscriptLine([]byte(`{"timestamp":"2026-08-09T12:00:01Z","type":"event_msg",`+
			`"payload":{"type":"item_completed","item":{"type":"AgentMessage",`+
			`"content":[{"type":"Text","text":"it never closes the ticker"}]}}}`), true)
		if len(agent) != 1 || agent[0].kind != domain.SessionSearchKindAssistant {
			t.Fatalf("agent units = %+v", agent)
		}
		command := parseCodexTranscriptLine([]byte(`{"timestamp":"2026-08-09T12:00:02Z","type":"event_msg",`+
			`"payload":{"type":"item_completed","item":{"type":"CommandExecution",`+
			`"command":["/bin/zsh","-lc","npm run typecheck"],"cwd":"/w","exit_code":2,`+
			`"aggregated_output":"error TS2304: cannot find name zzNeedle","status":"completed"}}}`), true)
		if len(command) != 1 || command[0].kind != domain.SessionSearchKindCommand ||
			!strings.Contains(command[0].text, "TS2304") || !strings.Contains(command[0].text, "npm run typecheck") {
			t.Fatalf("command units = %+v", command)
		}
		if strings.Contains(command[0].text, "/bin/zsh") {
			t.Fatalf("shell wrapper indexed: %q", command[0].text)
		}
		// Older rollouts record the command as a string and the output as output.
		legacy := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"CommandExecution","command":"go vet ./...","output":"vet: zzLegacy"}}}`), true)
		if len(legacy) != 1 || !strings.Contains(legacy[0].text, "go vet") || !strings.Contains(legacy[0].text, "zzLegacy") {
			t.Fatalf("legacy command units = %+v", legacy)
		}
	})

	t.Run("a rollout with both streams yields each message once", func(t *testing.T) {
		withItems, withoutItems := 0, 0
		for _, line := range []string{userItem, userResponse} {
			withItems += len(parseCodexTranscriptLine([]byte(line), true))
			withoutItems += len(parseCodexTranscriptLine([]byte(line), false))
		}
		if withItems != 1 || withoutItems != 1 {
			t.Fatalf("units = %d with the item stream, %d without; want 1 and 1", withItems, withoutItems)
		}
	})

	t.Run("file changes index their paths and changed lines", func(t *testing.T) {
		units := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"FileChange","changes":{`+
			`"src/retry.ts":{"type":"update","unified_diff":"--- a/src/retry.ts\n+++ b/src/retry.ts\n@@ -1 +1 @@\n context line\n-const backoff = 1\n+const backoff = retryWithJitter()\n","move_path":null},`+
			`"docs/NEW.md":{"type":"add","content":"# Quokka guide"}}}}}`), true)
		if len(units) != 1 || units[0].kind != domain.SessionSearchKindPath {
			t.Fatalf("units = %+v", units)
		}
		if strings.Join(units[0].paths, ",") != "docs/NEW.md,src/retry.ts" {
			t.Fatalf("paths = %v", units[0].paths)
		}
		body := units[0].text
		if !strings.Contains(body, "retryWithJitter") || !strings.Contains(body, "const backoff = 1") || !strings.Contains(body, "Quokka guide") {
			t.Fatalf("changed lines missing: %q", body)
		}
		if strings.Contains(body, "context line") || strings.Contains(body, "+++") {
			t.Fatalf("diff context or headers indexed: %q", body)
		}
	})

	t.Run("MCP calls, web searches and reasoning summaries", func(t *testing.T) {
		mcp := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"McpToolCall","server":"github","tool":"search_issues","arguments":{"query":"flaky login"},`+
			`"result":{"content":[{"type":"text","text":"issue 4821 flaky login"}],"isError":false}}}}`), true)
		if len(mcp) != 1 || mcp[0].kind != domain.SessionSearchKindTool || !strings.Contains(mcp[0].text, "issue 4821") ||
			!strings.Contains(mcp[0].text, "search_issues") {
			t.Fatalf("mcp units = %+v", mcp)
		}
		web := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"Extension","kind":"web.search","query":"sqlite busy_timeout","action":{"type":"openPage","url":"https://sqlite.org/pragma.html"},`+
			`"results":[{"type":"text_result","domain":"sqlite.org","title":"Pragma statements supported by SQLite","snippet":"busy_timeout sets a handler","url":"https://sqlite.org/pragma.html"}]}}}`), true)
		if len(web) != 1 || !strings.Contains(web[0].text, "busy_timeout") || !strings.Contains(web[0].text, "sqlite.org") ||
			!strings.Contains(web[0].text, "Pragma statements") {
			t.Fatalf("web units = %+v", web)
		}
		// Most Extension items only time an internal step.
		if units := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"Extension","kind":"thinking","durationMs":1200}}}`), true); len(units) != 0 {
			t.Fatalf("empty extension produced %+v", units)
		}
		reasoning := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"Reasoning","summary_text":["weighing the ticker leak"],"raw_content":[]}}}`), true)
		if len(reasoning) != 1 || reasoning[0].kind != domain.SessionSearchKindAssistant {
			t.Fatalf("reasoning units = %+v", reasoning)
		}
		// Encrypted reasoning with no summary has nothing to index.
		if units := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"Reasoning","summary_text":[],"raw_content":[]}}}`), true); len(units) != 0 {
			t.Fatalf("empty reasoning produced %+v", units)
		}
	})

	t.Run("response_item stream for older rollouts", func(t *testing.T) {
		message := parseCodexTranscriptLine([]byte(`{"timestamp":"2026-08-09T12:00:03Z","type":"response_item",`+
			`"payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the fix is in retry.ts"}]}}`), false)
		if len(message) != 1 || message[0].kind != domain.SessionSearchKindAssistant {
			t.Fatalf("message units = %+v", message)
		}
		call := parseCodexTranscriptLine([]byte(`{"timestamp":"2026-08-09T12:00:04Z","type":"response_item",`+
			`"payload":{"type":"function_call","name":"shell","arguments":{"cmd":"rg backoff src"}}}`), false)
		if len(call) != 1 || call[0].kind != domain.SessionSearchKindTool || !strings.Contains(call[0].text, "rg backoff src") {
			t.Fatalf("call units = %+v", call)
		}
		output := parseCodexTranscriptLine([]byte(`{"timestamp":"2026-08-09T12:00:05Z","type":"response_item",`+
			`"payload":{"type":"function_call_output","output":"src/upload/retry.ts:41"}}`), false)
		if len(output) != 1 || output[0].kind != domain.SessionSearchKindCommand {
			t.Fatalf("output units = %+v", output)
		}
		summary := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"response_item",`+
			`"payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"checking the ticker"}],"encrypted_content":"opaque"}}`), false)
		if len(summary) != 1 || !strings.Contains(summary[0].text, "checking the ticker") {
			t.Fatalf("reasoning summary units = %+v", summary)
		}
	})

	t.Run("instructions and metadata are skipped", func(t *testing.T) {
		for _, itemStream := range []bool{true, false} {
			for _, line := range []string{
				`{"timestamp":"x","type":"session_meta","payload":{"id":"abc"}}`,
				`{"timestamp":"x","type":"turn_context","payload":{}}`,
				`{"timestamp":"x","type":"response_item","payload":{"type":"message","role":"developer",` +
					`"content":[{"type":"input_text","text":"system instructions"}]}}`,
				`{"timestamp":"x","type":"event_msg","payload":{"type":"task_started"}}`,
				`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count"}}`,
				`{`,
			} {
				if units := parseCodexTranscriptLine([]byte(line), itemStream); len(units) != 0 {
					t.Fatalf("%s produced %+v", line, units)
				}
			}
		}
	})
}

func TestCodexHasItemStream(t *testing.T) {
	dir := t.TempDir()
	withItems := filepath.Join(dir, "new.jsonl")
	withoutItems := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(withItems, []byte(`{"type":"session_meta","payload":{}}`+"\n"+
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage"}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(withoutItems, []byte(`{"type":"session_meta","payload":{}}`+"\n"+
		`{"type":"response_item","payload":{"type":"message","role":"user"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !codexHasItemStream(withItems) || codexHasItemStream(withoutItems) || codexHasItemStream(filepath.Join(dir, "missing")) {
		t.Fatal("item stream detection is wrong")
	}
}

func TestParseClaudeAttachments(t *testing.T) {
	queued := parseClaudeTranscriptLine([]byte(`{"type":"attachment","timestamp":"2026-08-09T12:00:00Z",` +
		`"attachment":{"type":"queued_command","prompt":"also check the ticker leak","commandMode":"prompt"}}`))
	if len(queued) != 1 || queued[0].kind != domain.SessionSearchKindUser || queued[0].text != "also check the ticker leak" {
		t.Fatalf("queued prompt units = %+v; a prompt typed while the agent was busy is the person's own words", queued)
	}
	edited := parseClaudeTranscriptLine([]byte(`{"type":"attachment","timestamp":"x",` +
		`"attachment":{"type":"edited_text_file","filename":"src/reaper.go","snippet":"ticker.Stop()"}}`))
	if len(edited) != 1 || edited[0].kind != domain.SessionSearchKindPath || len(edited[0].paths) != 1 || edited[0].paths[0] != "src/reaper.go" {
		t.Fatalf("edited file units = %+v", edited)
	}
	for _, line := range []string{
		`{"type":"attachment","attachment":{"type":"total_tokens_reminder","text":"tokens used"}}`,
		`{"type":"attachment","attachment":{"type":"environment","text":"cwd"}}`,
		`{"type":"attachment","isSidechain":true,"attachment":{"type":"queued_command","prompt":"subagent prompt"}}`,
		`{"type":"ai-title","aiTitle":"Reaper leak"}`,
		`{"type":"api-request-blob","message":{"role":"user","content":[{"type":"text","text":"duplicate"}]}}`,
	} {
		if units := parseClaudeTranscriptLine([]byte(line)); len(units) != 0 {
			t.Fatalf("%s produced %+v", line, units)
		}
	}
}

func TestOpenCodeMessageUnits(t *testing.T) {
	t.Run("user text and attached prose", func(t *testing.T) {
		units := openCodeMessageUnits("user", `{"text":"the deadlock happens on shutdown","files":[]}`)
		if len(units) != 1 || units[0].kind != domain.SessionSearchKindUser {
			t.Fatalf("units = %+v", units)
		}
	})
	t.Run("every part of an assistant row", func(t *testing.T) {
		units := openCodeMessageUnits("assistant", `{"content":[`+
			`{"type":"reasoning","text":"the shutdown hook races the writer"},`+
			`{"type":"text","text":"Found it."},`+
			`{"type":"tool","name":"shell","state":{"status":"completed","input":{"command":"go test ./reaper"},`+
			`"content":[{"type":"text","text":"--- FAIL: TestShutdown deadlock"}],"metadata":{"exit":1}}},`+
			`{"type":"tool","name":"edit","state":{"input":{"path":"reaper/reaper.go","oldString":"mu.Lock()","newString":"mu.TryLock()"},"content":[]}},`+
			`{"type":"tool","name":"write","state":{"input":{"path":"docs/SHUTDOWN.md","content":"Quokka shutdown notes"},"content":[]}},`+
			`{"type":"tool","name":"webfetch","state":{"input":{"url":"https://go.dev/doc/faq","format":"text"},"content":[{"type":"text","text":"mutex faq"}]}},`+
			`{"type":"tool","name":"shell","state":{"status":"error","input":{"command":"make lint"},"error":{"message":"lint timed out","type":"timeout"}}}`+
			`]}`)
		kinds := make([]string, 0, len(units))
		for _, unit := range units {
			kinds = append(kinds, string(unit.kind))
		}
		if strings.Join(kinds, ",") != "assistant,assistant,command,path,path,tool,command" {
			t.Fatalf("kinds = %v", kinds)
		}
		if !strings.Contains(units[2].text, "go test ./reaper") || !strings.Contains(units[2].text, "TestShutdown deadlock") {
			t.Fatalf("shell unit = %q", units[2].text)
		}
		if units[3].paths[0] != "reaper/reaper.go" || !strings.Contains(units[3].text, "TryLock") {
			t.Fatalf("edit unit = %+v", units[3])
		}
		if units[4].paths[0] != "docs/SHUTDOWN.md" || !strings.Contains(units[4].text, "Quokka shutdown") {
			t.Fatalf("write unit = %+v", units[4])
		}
		if !strings.Contains(units[5].text, "go.dev/doc/faq") || !strings.Contains(units[5].text, "mutex faq") {
			t.Fatalf("webfetch unit = %q", units[5].text)
		}
		if !strings.Contains(units[6].text, "lint timed out") {
			t.Fatalf("failed shell unit = %q", units[6].text)
		}
	})
	t.Run("control payloads carry nothing", func(t *testing.T) {
		if units := openCodeMessageUnits("assistant", `{"outcome":"ok"}`); len(units) != 0 {
			t.Fatalf("units = %+v", units)
		}
		if units := openCodeMessageUnits("assistant", `not json`); len(units) != 0 {
			t.Fatalf("units = %+v", units)
		}
	})
}

func TestOpenCodeLegacyMessage(t *testing.T) {
	role, text := openCodeLegacyMessage(`{"role":"user","content":[{"type":"text","text":"legacy schema probe"}]}`)
	if role != "user" || text != "legacy schema probe" {
		t.Fatalf("role=%q text=%q", role, text)
	}
	role, text = openCodeLegacyMessage(`{"role":"assistant","content":[{"type":"tool_use","text":"ignored"}]}`)
	if role != "assistant" || text != "" {
		t.Fatalf("role=%q text=%q", role, text)
	}
	if _, text := openCodeLegacyMessage(`{`); text != "" {
		t.Fatalf("malformed payload produced %q", text)
	}
}

// A tool call's arguments are JSON of arbitrary shape; every string leaf has to
// be searchable, because that is where a person's remembered value lives.
func TestRawJSONTextFlattensEveryStringLeaf(t *testing.T) {
	got := rawJSONText([]byte(`{"command":"go build ./...","nested":{"paths":["src/auth","src/upload"]},"count":3,"ok":true}`))
	for _, want := range []string{"go build ./...", "src/auth", "src/upload"} {
		if !strings.Contains(got, want) {
			t.Fatalf("flattened text %q is missing %q", got, want)
		}
	}
	// Numbers and booleans are not prose and must not be indexed as words.
	for _, unwanted := range []string{"3", "true"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("flattened text %q includes %q", got, unwanted)
		}
	}
}

func TestReadNativeTranscriptBoundsEachUnit(t *testing.T) {
	output := strings.Repeat("noise ", 200_000) + "zzFinalError"
	prompt := "zzOpening " + strings.Repeat("words ", 200_000)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution",`+
			`"command":["bash","-lc","make"],"aggregated_output":"`+output+`"}}}`+"\n"+
			`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage",`+
			`"content":[{"type":"text","text":"`+prompt+`"}]}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _, err := readNativeTranscript(context.Background(), path, 0, codexParser(true))
	if err != nil || len(lines) != 2 {
		t.Fatalf("lines = %d, err = %v", len(lines), err)
	}
	for _, line := range lines {
		if len(line.text) > maxDocBodyBytes {
			t.Fatalf("%s unit holds %d bytes, want at most %d", line.kind, len(line.text), maxDocBodyBytes)
		}
	}
	if !strings.HasSuffix(lines[0].text, "zzFinalError") {
		t.Fatal("command output must keep its end, where the failure is")
	}
	if !strings.HasPrefix(lines[1].text, "zzOpening") {
		t.Fatal("prose must keep its start")
	}
}
