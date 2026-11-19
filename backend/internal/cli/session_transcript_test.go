package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTranscriptFlags_Validation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "zero", args: []string{"session", "get", "demo-1", "--transcript", "0"}, want: "--transcript must be an integer from 1 to 500"},
		{name: "negative", args: []string{"session", "get", "demo-1", "--transcript", "-1"}, want: "--transcript must be an integer from 1 to 500"},
		{name: "too big", args: []string{"session", "get", "demo-1", "--transcript", "501"}, want: "--transcript must be an integer from 1 to 500"},
		{name: "not integer", args: []string{"session", "get", "demo-1", "--transcript", "nope"}, want: "--transcript must be an integer from 1 to 500"},
		{name: "before alone", args: []string{"session", "get", "demo-1", "--before", "10"}, want: "--before requires --transcript"},
		{name: "cap alone", args: []string{"session", "get", "demo-1", "--cap", "80"}, want: "--cap requires --transcript"},
		{name: "cap 79", args: []string{"session", "get", "demo-1", "--transcript", "1", "--cap", "79"}, want: "--cap must be 0 or an integer from 80 to 1000000"},
		{name: "cap negative", args: []string{"session", "get", "demo-1", "--transcript", "1", "--cap", "-1"}, want: "--cap must be 0 or an integer from 80 to 1000000"},
		{name: "before zero", args: []string{"session", "get", "demo-1", "--transcript", "1", "--before", "0"}, want: "--before must be an integer >= 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tc.args...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("err = %v, want usage error\nstderr=%s", err, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestSessionGet_TranscriptRequestShapeAndProjectStop(t *testing.T) {
	cfg := setConfigEnv(t)
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/telemetry/cli-invoked" {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "transcript") || strings.Contains(string(body), "demo-1") {
				t.Errorf("telemetry body included flag or session data: %s", body)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
		case "/api/v1/sessions/demo-1/conversation":
			_, _ = io.WriteString(w, `{"conversationId":"cnv","messages":[],"activities":[],"turns":[],"hasMoreBefore":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "20")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "\n") != "GET /api/v1/sessions/demo-1\nGET /api/v1/sessions/demo-1/conversation?limit=20" {
		t.Fatalf("calls = %v", calls)
	}

	calls = nil
	_, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "20", "--before", "138")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "conversation?beforeSequence=138&limit=20") {
		t.Fatalf("calls = %v", calls)
	}

	calls = nil
	_, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "5", "-p", "other")
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("err = %v, want usage", err)
	}
	for _, call := range calls {
		if strings.Contains(call, "conversation") {
			t.Fatalf("project mismatch still called conversation: %v", calls)
		}
	}
}

func TestSessionGet_TranscriptHumanAndJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := transcriptFixtureServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "6")
	if err != nil {
		t.Fatalf("human transcript: %v\n%s", err, errOut)
	}
	for _, want := range []string{
		"id: demo-1",
		"transcript: 6 of 143 entries (sequence 1 to 6), oldest first",
		"controller: busy",
		"turn t_41 running",
		"plan 1/2 done",
		"incomplete: turn t_41 running; entry 4 streaming; entry 5 pending; entry 6 running",
		"older: ao session get demo-1 --transcript 6 --before 1",
		"#1 ",
		"user/human",
		"#2 ",
		"command failed exit 1",
		"RUNNING",
		"#3 ",
		"user/automation from orch-1 (orchestrator)",
		"[worker reports delivered with this message]",
		"worker checkpoint",
		"#4 ",
		"STREAMING",
		"attachment: image shot.png image/png",
		"chars omitted; full entry: ao session get demo-1 --transcript 1 --before 3 --cap 0",
		"Re-run with --cap 0 for full text.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "file:///secret") || strings.Contains(out, "\x1b") || strings.Contains(out, "\x01") {
		t.Fatalf("human output leaked uri, ansi, or control text:\n%s", out)
	}
	if strings.Contains(out, "<ao-worker-reports>") {
		t.Fatalf("piggyback tags leaked into human output:\n%s", out)
	}
	seqs := transcriptSequences(out)
	if strings.Join(seqs, ",") != "1,2,3,4,5,6" {
		t.Fatalf("human sequence order = %v", seqs)
	}

	raw, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "6", "--json")
	if err != nil {
		t.Fatalf("json transcript: %v\n%s", err, errOut)
	}
	var decoded struct {
		Session struct {
			ID   string `json:"id"`
			Mode string `json:"mode"`
		} `json:"session"`
		Transcript struct {
			Source          string `json:"source"`
			Order           string `json:"order"`
			Requested       int    `json:"requested"`
			Returned        int    `json:"returned"`
			HasMoreBefore   bool   `json:"hasMoreBefore"`
			TruncatedFields int    `json:"truncatedFields"`
			Next            struct {
				Before  int64  `json:"before"`
				Command string `json:"command"`
			} `json:"next"`
			Incomplete []map[string]any `json:"incomplete"`
			Entries    []map[string]any `json:"entries"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Session.ID != "demo-1" || decoded.Session.Mode != "chat" {
		t.Fatalf("session = %#v", decoded.Session)
	}
	tr := decoded.Transcript
	if tr.Source != "ao_conversation" || tr.Order != "oldest_first" || tr.Requested != 6 || tr.Returned != 6 {
		t.Fatalf("transcript meta = %#v", tr)
	}
	if !tr.HasMoreBefore || tr.Next.Before != 1 || !strings.Contains(tr.Next.Command, "--before 1") {
		t.Fatalf("next = %#v", tr.Next)
	}
	if tr.TruncatedFields < 1 {
		t.Fatalf("truncatedFields = %d", tr.TruncatedFields)
	}
	var prev int64
	seen := map[int64]int{}
	for i, entry := range tr.Entries {
		seq := int64(entry["sequence"].(float64))
		if i > 0 && seq < prev {
			t.Fatalf("sequences not ascending: %v then %v", prev, seq)
		}
		prev = seq
		seen[seq]++
		encoded, _ := json.Marshal(entry)
		if strings.Contains(string(encoded), "file:///secret") || strings.Contains(string(encoded), "\x1b") {
			t.Fatalf("json entry leaked private or ansi data: %s", encoded)
		}
	}
	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("sequence %d repeated %d times", seq, n)
		}
	}
	msg := tr.Entries[2]
	if msg["text"] != "CI is red" || msg["workerReports"] != "worker checkpoint" {
		t.Fatalf("piggyback split = %#v", msg)
	}
	if _, ok := msg["workerReports"].(string); !ok {
		t.Fatal("workerReports missing")
	}
}

func TestSessionGet_TranscriptACPToolResult(t *testing.T) {
	fileText := "package main\n" + strings.Repeat("read content line\n", 100)
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/conversation") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"conversationId": "cnv", "latestSequence": 2, "oldestSequence": 2,
				"activities": []any{map[string]any{
					"kind": "activity", "sequence": 2, "activityKind": "mcp_tool", "status": "completed",
					"summary": "Read /repo/main.go", "detail": map[string]any{
						"protocol": "acp", "input": map[string]any{"file_path": "/repo/main.go"},
						"output": "read complete", "content": []any{map[string]any{
							"type": "content", "content": map[string]any{"type": "text", "text": fileText},
						}},
					},
				}},
			})
			return
		}
		_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "1", "--cap", "0")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Read /repo/main.go", `input: {"file_path":"/repo/main.go"}`, "output: read complete", "content: package main"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ACP transcript missing %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "  read content line\n"); got != 100 {
		t.Fatalf("ACP transcript retained %d of 100 read lines", got)
	}

	capped, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "1", "--cap", "1000", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Transcript struct {
			Entries []struct {
				Detail struct {
					Content []struct {
						Content struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"content"`
				} `json:"detail"`
				Truncations []fieldTruncation `json:"truncations"`
			} `json:"entries"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(capped), &decoded); err != nil {
		t.Fatal(err)
	}
	entry := decoded.Transcript.Entries[0]
	if got := utf8.RuneCountInString(entry.Detail.Content[0].Content.Text); got != 1000 {
		t.Fatalf("ACP content length = %d, want 1000", got)
	}
	if len(entry.Truncations) != 1 || entry.Truncations[0].Field != "detail.content.0.content.text" {
		t.Fatalf("ACP truncation records = %#v", entry.Truncations)
	}
}

func TestSessionGet_TranscriptEdges(t *testing.T) {
	t.Run("tui skips conversation", func(t *testing.T) {
		cfg := setConfigEnv(t)
		var conv bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/conversation") {
				conv = true
			}
			if strings.HasSuffix(r.URL.Path, "/sessions/demo-1") {
				_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "tui")+`}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)
		_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "3")
		if err == nil || strings.Contains(err.Error(), "usage") {
			t.Fatalf("err = %v, want runtime failure\n%s", err, errOut)
		}
		var usage usageError
		if errors.As(err, &usage) {
			t.Fatal("TUI refusal must not be a usage error")
		}
		if !strings.Contains(err.Error(), "Terminal UI mode") || !strings.Contains(err.Error(), "ao session get demo-1") {
			t.Fatalf("message = %v", err)
		}
		if conv {
			t.Fatal("conversation endpoint was called")
		}
	})

	for _, tc := range []struct {
		name         string
		code         string
		message      string
		wantGuidance string
	}{
		{"409 mismatch", "SESSION_MODE_MISMATCH", "this session was created in Terminal UI mode and has no chat conversation", "Terminal UI mode"},
		{"409 transition", "CHAT_INTERFACE_TRANSITION", "the session is switching interfaces; send again after the handoff completes", "send again after the handoff completes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/conversation") {
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"code":"`+tc.code+`","message":"`+tc.message+`","requestId":"req-transition"}`)
					return
				}
				if strings.Contains(r.URL.Path, "/sessions/") {
					_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "1")
			var apiErr apiResponseError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict ||
				apiErr.ErrorBody.Code != tc.code || apiErr.ErrorBody.RequestID != "req-transition" {
				t.Fatalf("daemon error envelope = %#v, err = %v", apiErr, err)
			}
			if !strings.Contains(err.Error(), tc.wantGuidance) || !strings.Contains(err.Error(), tc.code) || !strings.Contains(err.Error(), "req-transition") {
				t.Fatalf("diagnostics or guidance lost: %v", err)
			}
			if tc.code == "CHAT_INTERFACE_TRANSITION" && strings.Contains(err.Error(), "no durable transcript") {
				t.Fatalf("transition incorrectly described as permanent: %v", err)
			}
		})
	}

	t.Run("empty conversation", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/conversation") {
				_, _ = io.WriteString(w, `{"conversationId":"","controller":"stopped","messages":[],"activities":[],"turns":[],"hasMoreBefore":false,"latestSequence":0}`)
				return
			}
			_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "4", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, `"next"`) {
			t.Fatalf("empty transcript invented a cursor:\n%s", out)
		}
		if !strings.Contains(out, `"entries": []`) || !strings.Contains(out, `"returned": 0`) {
			t.Fatalf("empty json = %s", out)
		}
	})

	t.Run("no older cursor", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/conversation") {
				_, _ = io.WriteString(w, `{"conversationId":"cnv","latestSequence":1,"oldestSequence":1,"hasMoreBefore":false,"messages":[{"kind":"message","sequence":1,"role":"user","origin":"human","text":"hi","createdAt":"2026-10-09T10:00:00Z"}],"activities":[],"turns":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "5")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "older:") {
			t.Fatalf("printed a cursor at the start:\n%s", out)
		}
	})

	t.Run("cap 0 passthrough and unicode", func(t *testing.T) {
		body := strings.Repeat("你", 600) + "MIDDLE" + strings.Repeat("🙂", 600)
		cfg := setConfigEnv(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/conversation") {
				payload, _ := json.Marshal(map[string]any{
					"conversationId": "cnv",
					"latestSequence": 9,
					"oldestSequence": 9,
					"hasMoreBefore":  false,
					"messages": []any{map[string]any{
						"kind": "message", "sequence": 9, "role": "assistant", "origin": "provider",
						"text": body, "createdAt": "2026-10-09T10:00:00Z",
					}},
				})
				_, _ = w.Write(payload)
				return
			}
			_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
		}))
		t.Cleanup(srv.Close)
		writeRunFileFor(t, cfg, srv)
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "1", "--cap", "0", "--json")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, body) || strings.Contains(out, "truncatedFields") {
			t.Fatalf("cap 0 changed the text:\n%s", out)
		}
		if !utf8.ValidString(out) {
			t.Fatal("output is not valid utf-8")
		}

		capped, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1", "--transcript", "1", "--cap", "1000", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Transcript struct {
				Entries []struct {
					Text        string            `json:"text"`
					Truncations []fieldTruncation `json:"truncations"`
				} `json:"entries"`
			} `json:"transcript"`
		}
		if err := json.Unmarshal([]byte(capped), &decoded); err != nil {
			t.Fatal(err)
		}
		text := decoded.Transcript.Entries[0].Text
		if !utf8.ValidString(text) {
			t.Fatal("capped text is not valid utf-8")
		}
		if !strings.HasPrefix(text, "你") || !strings.HasSuffix(text, "🙂") {
			t.Fatalf("head/tail runes lost: %q", text)
		}
		if utf8.RuneCountInString(text) != 1000 {
			t.Fatalf("kept runes = %d, want 1000", utf8.RuneCountInString(text))
		}
		tr := decoded.Transcript.Entries[0].Truncations
		if len(tr) != 1 || tr[0].Strategy != "head_tail" || tr[0].Field != "text" || tr[0].OriginalChars != utf8.RuneCountInString(body) {
			t.Fatalf("truncations = %#v", tr)
		}
	})
}

func TestTranscriptSmallCapKeepsTextAndAccurateOmission(t *testing.T) {
	const fieldCap = 80
	source := strings.Repeat("H", 500) + strings.Repeat("T", 500)
	command := transcriptCommand("demo-1", 1, 2, intPtr(0), "")
	got, receipt := elideField(source, fieldCap, "text", command)
	if utf8.RuneCountInString(got) != fieldCap {
		t.Fatalf("capped length = %d, want %d: %q", utf8.RuneCountInString(got), fieldCap, got)
	}
	if !strings.HasPrefix(got, "H") || !strings.HasSuffix(got, "T") {
		t.Fatalf("source head or tail lost: %q", got)
	}
	if strings.Contains(got, command) {
		t.Fatalf("full command displaced source text: %q", got)
	}
	omitted := len(source) - strings.Count(got, "H") - strings.Count(got, "T")
	if !strings.Contains(got, truncationMarker(omitted, "")) {
		t.Fatalf("omission count does not match retained text: %q, omitted %d", got, omitted)
	}
	if receipt == nil || receipt.OriginalChars != len(source) || receipt.KeptChars != fieldCap {
		t.Fatalf("truncation receipt = %#v", receipt)
	}
}

func TestSplitWorkerReports(t *testing.T) {
	visible, reports, ok := splitWorkerReports("hello\n\n<ao-worker-reports>\nbody\n</ao-worker-reports>")
	if !ok || visible != "hello" || reports != "body" {
		t.Fatalf("split = %q %q %v", visible, reports, ok)
	}
	plain := "hello world"
	visible, reports, ok = splitWorkerReports(plain)
	if ok || visible != plain || reports != "" {
		t.Fatalf("plain changed: %q %q %v", visible, reports, ok)
	}
	malformed := "hello </ao-worker-reports>"
	visible, _, ok = splitWorkerReports(malformed)
	if ok || visible != malformed {
		t.Fatalf("malformed changed: %q %v", visible, ok)
	}
}

func TestSanitizeText(t *testing.T) {
	got := sanitizeText("a\x1b[31mred\x1b[0m\x01b\t\n")
	if got != "aredb\t\n" {
		t.Fatalf("sanitize = %q", got)
	}
	if got := sanitizeText("a\x1b"); got != "a" {
		t.Fatalf("trailing escape survived: %q", got)
	}
	links := "before \x1b]8;;https://example.com/one\x1b\\IMPORTANT\x1b]8;;\x1b\\ between " +
		"\x1b]8;;https://example.com/two\x1b\\SECOND\x1b]8;;\x1b\\ after"
	if got := sanitizeText(links); got != "before IMPORTANT between SECOND after" {
		t.Fatalf("OSC8 labels lost: %q", got)
	}
}

func TestTranscriptCapsStreamedActivityFields(t *testing.T) {
	const fieldLength = 32768
	fields := []struct {
		kind string
		key  string
	}{
		{kind: "mcp_tool", key: "progress"},
		{kind: "file_change", key: "patchOutput"},
	}
	var activities []json.RawMessage
	for i, field := range fields {
		raw, err := json.Marshal(map[string]any{
			"kind": "activity", "sequence": i + 1, "activityKind": field.kind,
			"detail": map[string]any{field.key: strings.Repeat("x", fieldLength)},
		})
		if err != nil {
			t.Fatal(err)
		}
		activities = append(activities, raw)
	}
	doc := buildTranscriptView("demo", "", transcriptRequest{n: len(fields), cap: transcriptCapDefault}, conversationSnapshotWire{
		ConversationID: "cnv", Activities: activities,
	})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		TruncatedFields int `json:"truncatedFields"`
		Entries         []struct {
			Detail      map[string]string `json:"detail"`
			Truncations []fieldTruncation `json:"truncations"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.TruncatedFields != len(fields) || len(output.Entries) != len(fields) {
		t.Fatalf("streamed field truncation counts = %d, %d", output.TruncatedFields, len(output.Entries))
	}
	for i, field := range fields {
		entry := output.Entries[i]
		value := entry.Detail[field.key]
		if utf8.RuneCountInString(value) != transcriptCapDefault || !strings.Contains(value, "chars omitted") {
			t.Errorf("%s was not capped: length %d", field.key, utf8.RuneCountInString(value))
		}
		if len(entry.Truncations) != 1 || entry.Truncations[0].Field != "detail."+field.key ||
			entry.Truncations[0].OriginalChars != fieldLength || entry.Truncations[0].KeptChars != transcriptCapDefault {
			t.Errorf("%s truncations = %#v", field.key, entry.Truncations)
		}
	}
}

func TestSessionGet_TranscriptCapsACPFileSnapshots(t *testing.T) {
	const fieldLength = 12000
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/conversation") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"conversationId": "cnv", "latestSequence": 2, "oldestSequence": 2,
				"activities": []any{map[string]any{
					"kind": "activity", "sequence": 2, "activityKind": "file_change",
					"summary": "Edit /repo/main.go", "detail": map[string]any{
						"protocol": "acp", "files": []any{map[string]any{
							"path": "/repo/main.go", "status": "modified",
							"patch":   strings.Repeat("p", fieldLength),
							"oldText": strings.Repeat("o", fieldLength),
							"newText": strings.Repeat("n", fieldLength),
						}},
					},
				}},
			})
			return
		}
		_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "get", "demo-1", "--transcript", "1", "--json", "--cap", "1000")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Transcript struct {
			TruncatedFields int `json:"truncatedFields"`
			Entries         []struct {
				Detail struct {
					Files []map[string]string `json:"files"`
				} `json:"detail"`
				Truncations []fieldTruncation `json:"truncations"`
			} `json:"entries"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	entry := decoded.Transcript.Entries[0]
	if decoded.Transcript.TruncatedFields != 3 || len(entry.Truncations) != 3 {
		t.Fatalf("ACP file truncation counts = %d, %#v", decoded.Transcript.TruncatedFields, entry.Truncations)
	}
	for _, key := range []string{"patch", "oldText", "newText"} {
		value := entry.Detail.Files[0][key]
		if utf8.RuneCountInString(value) != 1000 || !strings.Contains(value, "chars omitted") {
			t.Errorf("%s was not capped: length %d", key, utf8.RuneCountInString(value))
		}
		found := false
		for _, trunc := range entry.Truncations {
			if trunc.Field == "detail.files.0."+key && trunc.OriginalChars == fieldLength && trunc.KeptChars == 1000 {
				found = true
			}
		}
		if !found {
			t.Errorf("%s truncation receipt missing: %#v", key, entry.Truncations)
		}
	}
}

func TestTranscriptCapsNestedToolArrays(t *testing.T) {
	const fieldLength = 10000
	activity, err := json.Marshal(map[string]any{
		"kind": "activity", "sequence": 1, "activityKind": "mcp_tool",
		"detail": map[string]any{
			"arguments": map[string]any{"values": [][]string{{strings.Repeat("a", fieldLength)}}},
			"result":    map[string]any{"cells": [][]string{{strings.Repeat("b", fieldLength)}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := buildTranscriptView("demo", "", transcriptRequest{n: 1, cap: transcriptCapDefault}, conversationSnapshotWire{
		ConversationID: "cnv", Activities: []json.RawMessage{activity},
	})
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		TruncatedFields int `json:"truncatedFields"`
		Entries         []struct {
			Detail struct {
				Arguments struct {
					Values [][]string `json:"values"`
				} `json:"arguments"`
				Result struct {
					Cells [][]string `json:"cells"`
				} `json:"result"`
			} `json:"detail"`
			Truncations []fieldTruncation `json:"truncations"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.TruncatedFields != 2 || len(output.Entries) != 1 {
		t.Fatalf("truncation counts = %d, %d", output.TruncatedFields, len(output.Entries))
	}
	entry := output.Entries[0]
	for i, value := range []string{entry.Detail.Arguments.Values[0][0], entry.Detail.Result.Cells[0][0]} {
		if utf8.RuneCountInString(value) != transcriptCapDefault || !strings.Contains(value, "chars omitted") {
			t.Errorf("nested tool field %d was not capped: length %d", i, utf8.RuneCountInString(value))
		}
	}
	paths := map[string]bool{"detail.arguments.values.0.0": true, "detail.result.cells.0.0": true}
	if len(entry.Truncations) != len(paths) {
		t.Fatalf("truncations = %#v", entry.Truncations)
	}
	for _, trunc := range entry.Truncations {
		if !paths[trunc.Field] || trunc.OriginalChars != fieldLength || trunc.KeptChars != transcriptCapDefault {
			t.Errorf("truncation = %#v", trunc)
		}
		delete(paths, trunc.Field)
	}
	if len(paths) != 0 {
		t.Errorf("missing truncation fields = %#v", paths)
	}
}

func transcriptSequences(out string) []string {
	var seqs []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "#") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				seqs = append(seqs, strings.TrimPrefix(fields[0], "#"))
			}
		}
	}
	return seqs
}

func sessionJSONWithMode(id, project, mode string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "projectId": project, "kind": "worker", "harness": "codex",
		"activity":     map[string]any{"state": "active", "lastActivityAt": "2026-10-09T10:41:12Z"},
		"isTerminated": false,
		"createdAt":    "2026-10-09T09:58:10Z",
		"updatedAt":    "2026-10-09T10:41:12Z",
		"status":       "working",
		"mode":         mode,
	})
	return string(b)
}

func transcriptFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	long := strings.Repeat("A", 800) + "\x01" + strings.Repeat("B", 800)
	payload := map[string]any{
		"conversationId": "cnv_8f2a",
		"activeBranchId": "br_1",
		"controller":     "busy",
		"latestSequence": 143,
		"oldestSequence": 1,
		"hasMoreBefore":  true,
		"usage":          map[string]any{"contextUsed": 122000, "contextWindow": 200000},
		"rateLimits":     map[string]any{"primaryUsedPercent": 82},
		"turns": []any{
			map[string]any{"id": "t_40", "state": "completed", "requestedAt": "2026-10-09T10:00:00Z"},
			map[string]any{
				"id": "t_41", "state": "running", "requestedAt": "2026-10-09T10:39:02Z", "startedAt": "2026-10-09T10:39:02Z",
				"plan": map[string]any{"steps": []any{
					map[string]any{"text": "Reproduce", "status": "completed"},
					map[string]any{"text": "Fix", "status": "pending"},
				}},
				"diff": map[string]any{"files": []any{map[string]any{"path": "a.go", "additions": 2, "deletions": 1, "status": "modified"}}},
			},
		},
		"messages": []any{
			map[string]any{
				"kind": "message", "id": "m4", "sequence": 4, "revision": 2, "role": "assistant", "origin": "provider",
				"turnId": "t_41", "text": "still \x1b[32mwriting\x1b[0m", "streaming": true, "createdAt": "2026-10-09T10:41:12Z",
				"content": []any{map[string]any{"type": "image", "name": "shot.png", "mimeType": "image/png", "uri": "file:///secret"}},
			},
			map[string]any{
				"kind": "message", "id": "m1", "sequence": 1, "revision": 1, "role": "user", "origin": "human",
				"turnId": "t_41", "text": "Fix the test", "streaming": false, "createdAt": "2026-10-09T10:39:02Z",
			},
			map[string]any{
				"kind": "message", "id": "m3", "sequence": 3, "revision": 1, "role": "user", "origin": "automation",
				"senderSessionId": "orch-1", "senderDisplayName": "orchestrator",
				"text":      "CI is red\n\n<ao-worker-reports>\nworker checkpoint\n</ao-worker-reports>",
				"createdAt": "2026-10-09T10:40:44Z",
			},
		},
		"activities": []any{
			map[string]any{
				"kind": "activity", "id": "a5", "sequence": 5, "revision": 1, "activityKind": "approval",
				"status": "pending", "summary": "allow test", "requestId": "req-9", "createdAt": "2026-10-09T10:42:00Z",
			},
			map[string]any{
				"kind": "activity", "id": "a2", "sequence": 2, "revision": 3, "turnId": "t_41",
				"activityKind": "command", "status": "failed", "summary": "go test",
				"detail": map[string]any{
					"command": "go test ./internal/foo", "exitCode": 1, "durationMs": 31000,
					"output": long, "outputMayBePartial": true,
				},
				"createdAt": "2026-10-09T10:40:01Z",
			},
			map[string]any{
				"kind": "activity", "id": "a6", "sequence": 6, "revision": 1, "activityKind": "command",
				"status": "running", "summary": "go test -race", "turnId": "t_41",
				"detail":    map[string]any{"command": "go test -race", "outputMayBePartial": true},
				"createdAt": "2026-10-09T10:42:10Z",
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/conversation"):
			_ = json.NewEncoder(w).Encode(payload)
		case strings.Contains(r.URL.Path, "/sessions/"):
			_, _ = io.WriteString(w, `{"session":`+sessionJSONWithMode("demo-1", "demo", "chat")+`}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
