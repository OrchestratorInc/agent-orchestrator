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

	t.Run("409 mismatch", func(t *testing.T) {
		cfg := setConfigEnv(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/conversation") {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"code":"SESSION_MODE_MISMATCH","message":"no chat"}`)
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
		if err == nil || !strings.Contains(err.Error(), "Terminal UI mode") {
			t.Fatalf("err = %v", err)
		}
	})

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
	// The duplicate-sequence fixture above intentionally repeats sequence 1 so the
	// renderer is asserted not to invent a third copy. Two rows in, two rows out.
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
