package sessionsearch

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
		if call[0].text != "Bash: go build ./..." {
			t.Fatalf("tool call text = %q, want its name and command", call[0].text)
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

	t.Run("thinking is not indexed", func(t *testing.T) {
		units := parseClaudeTranscriptLine([]byte(`{"type":"assistant","message":{"role":"assistant",` +
			`"content":[{"type":"thinking","thinking":"the retry loop never resets its counter"}]}}`))
		if len(units) != 0 {
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

	t.Run("file changes index their paths, not their diffs", func(t *testing.T) {
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
		if body := units[0].text; body != "apply_patch: docs/NEW.md\nsrc/retry.ts" {
			t.Fatalf("body = %q, want the tool and the files it touched", body)
		}
	})

	t.Run("MCP calls and web searches; reasoning is not indexed", func(t *testing.T) {
		mcp := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"McpToolCall","server":"github","tool":"search_issues","arguments":{"query":"flaky login"},`+
			`"result":{"content":[{"type":"text","text":"issue 4821 flaky login"}],"isError":false}}}}`), true)
		if len(mcp) != 1 || mcp[0].kind != domain.SessionSearchKindTool ||
			!strings.HasPrefix(mcp[0].text, "github.search_issues: flaky login") || !strings.Contains(mcp[0].text, "issue 4821") {
			t.Fatalf("mcp units = %+v", mcp)
		}
		web := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"event_msg","payload":{"type":"item_completed",`+
			`"item":{"type":"Extension","kind":"web.search","query":"sqlite busy_timeout","action":{"type":"openPage","url":"https://sqlite.org/pragma.html"},`+
			`"results":[{"type":"text_result","domain":"sqlite.org","title":"Pragma statements supported by SQLite","snippet":"busy_timeout sets a handler","url":"https://sqlite.org/pragma.html"}]}}}`), true)
		if len(web) != 1 || !strings.HasPrefix(web[0].text, "web_search: sqlite busy_timeout") ||
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
		if len(reasoning) != 0 {
			t.Fatalf("reasoning units = %+v", reasoning)
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
		if len(call) != 1 || call[0].kind != domain.SessionSearchKindTool || call[0].text != "shell: rg backoff src" {
			t.Fatalf("call units = %+v", call)
		}
		output := parseCodexTranscriptLine([]byte(`{"timestamp":"2026-08-09T12:00:05Z","type":"response_item",`+
			`"payload":{"type":"function_call_output","output":"src/upload/retry.ts:41"}}`), false)
		if len(output) != 1 || output[0].kind != domain.SessionSearchKindCommand {
			t.Fatalf("output units = %+v", output)
		}
		summary := parseCodexTranscriptLine([]byte(`{"timestamp":"x","type":"response_item",`+
			`"payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"checking the ticker"}],"encrypted_content":"opaque"}}`), false)
		if len(summary) != 0 {
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
	if !codexHasItemStream(withItems, math.MaxInt64) || codexHasItemStream(withoutItems, math.MaxInt64) || codexHasItemStream(filepath.Join(dir, "missing"), math.MaxInt64) {
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
		// The reasoning part is not indexed.
		if strings.Join(kinds, ",") != "assistant,command,path,path,tool,command" {
			t.Fatalf("kinds = %v", kinds)
		}
		if !strings.HasPrefix(units[1].text, "shell: go test ./reaper") || !strings.Contains(units[1].text, "TestShutdown deadlock") {
			t.Fatalf("shell unit = %q", units[1].text)
		}
		// An edit or a write keeps its path, not the text it wrote.
		if units[2].paths[0] != "reaper/reaper.go" || units[2].text != "edit: reaper/reaper.go" {
			t.Fatalf("edit unit = %+v", units[2])
		}
		if units[3].paths[0] != "docs/SHUTDOWN.md" || units[3].text != "write: docs/SHUTDOWN.md" {
			t.Fatalf("write unit = %+v", units[3])
		}
		// A fetch has none of the remembered arguments; its output is kept.
		if units[4].text != "webfetch\n\nmutex faq" {
			t.Fatalf("webfetch unit = %q", units[4].text)
		}
		if !strings.Contains(units[5].text, "lint timed out") {
			t.Fatalf("failed shell unit = %q", units[5].text)
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
	lines, _, err := readNativeTranscript(context.Background(), path, 0, math.MaxInt64, codexParser(true))
	if err != nil || len(lines) != 2 {
		t.Fatalf("lines = %d, err = %v", len(lines), err)
	}
	// A command row is the command and the first 3,072 characters of its
	// output (coverage.go); prose keeps as many chunks as a message may become.
	if got := utf8.RuneCountInString(lines[0].text); got > len("make")+2+toolOutputChars {
		t.Fatalf("command unit holds %d characters, want the command and %d of output", got, toolOutputChars)
	}
	if got := len(lines[1].text); got > maxDocBodyBytes*maxMessageChunks {
		t.Fatalf("user unit holds %d bytes, want at most %d", got, maxDocBodyBytes*maxMessageChunks)
	}
	if !strings.HasPrefix(lines[0].text, "make\n\nnoise noise") {
		t.Fatalf("command row = %.40q, want the command, then the start of its output", lines[0].text)
	}
	if strings.Contains(lines[0].text, "zzFinalError") {
		t.Fatal("output past its first 3,072 characters is not indexed")
	}
	if !strings.HasPrefix(lines[1].text, "zzOpening") {
		t.Fatal("prose must keep its start")
	}
}

// Found in review: the format probe and the read open the live file
// separately, so a first item record appended between them was skipped by the
// legacy parser while the cursor moved past it. The read stops at the size the
// probe saw; the next pass reads the record and its probe finds it.
func TestNativeReadStopsAtTheSizeTheProbeSaw(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	legacy := `{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"legacy turn"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	probed := int64(len(legacy))
	if codexHasItemStreamFrom(path, 0, probed) {
		t.Fatal("the legacy rollout has no item record yet")
	}
	// The first item record arrives after the probe, before the read.
	item := `{"timestamp":"2026-08-09T12:05:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","npm test"],"aggregated_output":"zzRaceNeedle","status":"completed"}}}` + "\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	if _, err := file.WriteString(item); err != nil {
		t.Fatalf("append item: %v", err)
	}
	_ = file.Close()

	lines, next, err := readNativeTranscript(context.Background(), path, 0, probed, codexParser(false))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if next != probed || len(lines) != 1 {
		t.Fatalf("read to %d with %d units, want to stop at the probed %d with the legacy turn", next, len(lines), probed)
	}
	if !codexHasItemStreamFrom(path, next, math.MaxInt64) {
		t.Fatal("the next pass's probe from the cursor must find the item record")
	}
}

// Found in review: the probes could see a record finished after the file was
// measured while the read, bounded to that size, did not read it: the source
// was saved as the item stream without its first item, and the next pass
// indexed that item next to its legacy copy. The probes stop at the same size.
func TestFormatProbeSeesOnlyWhatWasThereWhenMeasured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	legacy := `{"timestamp":"2026-08-09T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"legacy turn"}]}}` + "\n"
	item := `{"timestamp":"2026-08-09T12:05:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"legacy turn"}]}}}` + "\n"
	half := len(item) / 2
	// Measured while the first item record is half written.
	if err := os.WriteFile(path, []byte(legacy+item[:half]), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	measured := int64(len(legacy) + half)
	// Codex finishes the record before the probe runs.
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	if _, err := file.WriteString(item[half:]); err != nil {
		t.Fatalf("finish item: %v", err)
	}
	_ = file.Close()

	if _, format := nativeParserFor(domain.HarnessCodex, path, "", measured); format != nativeFormat {
		t.Fatalf("head probe format = %q, want the legacy format the measured file has", format)
	}
	if codexHasItemStreamFrom(path, 0, measured) {
		t.Fatal("the unread-part check saw a record finished after the file was measured")
	}
	// The next pass, which measures the whole file, sees it.
	if !codexHasItemStreamFrom(path, 0, int64(len(legacy)+len(item))) {
		t.Fatal("the next pass must see the finished item record")
	}
}
