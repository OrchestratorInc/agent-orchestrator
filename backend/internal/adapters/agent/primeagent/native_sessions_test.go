package primeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const primeListFixture = `{"sessions":[
 {"sessionId":"01a-top","activeSessionId":"live-1","activity":"working","cwd":"/work/one","created":"2026-11-05T13:04:00.076Z","rlmDepth":0,"runtimeKind":"top-level"},
 {"sessionId":"01b-idle","activeSessionId":"live-2","activity":"idle","cwd":"/work/two","created":"2026-11-05T13:05:00.000Z","rlmDepth":0,"runtimeKind":"top-level"},
 {"sessionId":"01c-child","activeSessionId":"live-3","activity":"working","cwd":"/work/one","created":"2026-11-05T13:06:00.000Z","rlmDepth":1,"runtimeKind":"subagent"},
 {"sessionId":"01d-kind","activeSessionId":"live-4","activity":"working","cwd":"/work/one","created":"2026-11-05T13:06:00.000Z","rlmDepth":0,"runtimeKind":"background"},
 {"sessionId":"../../etc/passwd","activeSessionId":"live-5","activity":"idle","cwd":"/work/three","created":"2026-11-05T13:06:00.000Z","rlmDepth":0},
 {"sessionId":"01e-parked","activeSessionId":"live-6","activity":"parked","cwd":"/work/four","created":"2026-11-05T13:06:00.000Z","rlmDepth":0},
 {"sessionId":"","activeSessionId":"","activity":"idle","cwd":"/work/five","created":"2026-11-05T13:06:00.000Z","rlmDepth":0},
 {"sessionId":"01f-nocwd","activeSessionId":"live-7","activity":"idle","cwd":"","created":"2026-11-05T13:06:00.000Z","rlmDepth":0}
]}`

func primePlugin(run primeCommandRunner, acceptsExtension bool) *Plugin {
	return &Plugin{
		resolvedBinary: "prime-agent",
		extensionFlag:  map[string]bool{"prime-agent": acceptsExtension},
		runCommand:     run,
	}
}

func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func transcriptMessage(role, at, content string) string {
	return fmt.Sprintf(`{"type":"message","id":"x","timestamp":%q,"message":{"role":%q,"content":%s}}`, at, role, content)
}

func TestObserveNativeSessionsReportsTopLevelSessions(t *testing.T) {
	dataDir := t.TempDir()
	sessions, err := sessionDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(sessions, "01a-top.jsonl")
	writeTranscript(t, transcript,
		`{"type":"session","version":3,"id":"01a-top","timestamp":"2026-11-05T13:04:00.076Z","cwd":"/work/one"}`,
		transcriptMessage("user", "2026-11-05T13:04:00.137Z", `[{"type":"text","text":"do it"}]`),
		transcriptMessage("assistant", "2026-11-05T13:04:25.989Z", `[{"type":"text","text":"done"}]`),
	)
	var args [][]string
	var gotEnv map[string]string
	plugin := primePlugin(func(_ context.Context, _, _ string, env map[string]string, cmd ...string) ([]byte, error) {
		args, gotEnv = append(args, cmd), env
		return []byte(primeListFixture), nil
	}, false)

	got, ok, err := plugin.ObserveNativeSessions(context.Background(), dataDir)
	if err != nil || !ok {
		t.Fatalf("ObserveNativeSessions ok = %v, err = %v", ok, err)
	}
	if len(args) != 1 || !slices.Equal(args[0], []string{"list", "--json"}) {
		t.Fatalf("commands = %v", args)
	}
	profile, _ := primeDataDir(dataDir)
	if gotEnv[primeAgentCodingAgentDirEnv] != profile {
		t.Fatalf("env = %v, want the session profile", gotEnv)
	}
	ids := make([]string, 0, len(got))
	for _, snapshot := range got {
		ids = append(ids, snapshot.AgentSessionID)
	}
	if want := []string{"01a-top", "01b-idle"}; !slices.Equal(ids, want) {
		t.Fatalf("sessions = %v, want %v", ids, want)
	}
	first := got[0]
	wantStarted := time.Date(2026, 11, 5, 13, 4, 0, 76_000_000, time.UTC)
	if !first.Working || first.WorkspacePath != "/work/one" || first.TranscriptPath != transcript || !first.StartedAt.Equal(wantStarted) {
		t.Fatalf("first = %#v", first)
	}
	if first.Prompt.Text != "do it" || first.Reply.Text != "done" {
		t.Fatalf("messages = %q, %q", first.Prompt.Text, first.Reply.Text)
	}
	if got[1].Working || !got[1].Prompt.At.IsZero() {
		t.Fatalf("second = %#v, want idle with no transcript", got[1])
	}
}

func TestObserveNativeSessionsLeavesVersionsWithHooksAlone(t *testing.T) {
	plugin := primePlugin(func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
		t.Fatal("a version with the extension must not be polled")
		return nil, nil
	}, true)

	got, ok, err := plugin.ObserveNativeSessions(context.Background(), t.TempDir())
	if ok || err != nil || got != nil {
		t.Fatalf("ObserveNativeSessions = %v, %v, %v", got, ok, err)
	}
}

func TestObserveNativeSessionsReportsFailures(t *testing.T) {
	tests := []struct {
		name    string
		dataDir string
		output  string
		err     error
	}{
		{name: "command failed", dataDir: t.TempDir(), output: "no daemon", err: errors.New("exit 1")},
		{name: "not json", dataDir: t.TempDir(), output: "starting up"},
		{name: "blank data dir", dataDir: " "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := primePlugin(func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
				return []byte(tt.output), tt.err
			}, false)
			if _, ok, err := plugin.ObserveNativeSessions(context.Background(), tt.dataDir); ok || err == nil {
				t.Fatalf("ok = %v, err = %v, want a failure", ok, err)
			}
		})
	}
}

func TestReadLatestMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscript(t, path,
		`{"type":"session","id":"s","timestamp":"2026-11-05T13:00:00Z"}`,
		transcriptMessage("user", "2026-11-05T13:00:01Z", `"first"`),
		transcriptMessage("assistant", "2026-11-05T13:00:02Z", `[{"type":"thinking","thinking":"hm"},{"type":"text","text":"line one"},{"type":"text","text":"line two"}]`),
		`not json`,
		`{"type":"model_change","timestamp":"2026-11-05T13:00:03Z"}`,
		transcriptMessage("user", "not a time", `"undated"`),
		transcriptMessage("toolResult", "2026-11-05T13:00:04Z", `"output"`),
		transcriptMessage("user", "2026-11-05T13:00:05Z", `[{"type":"image"}]`),
	)

	prompt, reply := readLatestMessages(path)

	if prompt.Text != "" || !prompt.At.Equal(time.Date(2026, 11, 5, 13, 0, 5, 0, time.UTC)) {
		t.Fatalf("prompt = %#v, want the newest dated user message", prompt)
	}
	if reply.Text != "line one\nline two" {
		t.Fatalf("reply = %#v", reply)
	}
}

func TestReadLatestMessagesAbortedReplyHasNoText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscript(t, path, transcriptMessage("assistant", "2026-11-05T13:00:02Z", `[]`))

	_, reply := readLatestMessages(path)

	if reply.Text != "" || reply.At.IsZero() {
		t.Fatalf("reply = %#v, want a dated reply with no text", reply)
	}
}

func TestReadLatestMessagesMissingTranscript(t *testing.T) {
	prompt, reply := readLatestMessages(filepath.Join(t.TempDir(), "missing.jsonl"))
	if !prompt.At.IsZero() || !reply.At.IsZero() {
		t.Fatalf("messages = %#v, %#v, want none", prompt, reply)
	}
}

func TestReadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte("first line\nsecond\nthird\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		limit int64
		want  string
	}{
		{name: "whole file", limit: 1024, want: "first line\nsecond\nthird\n"},
		{name: "drops the partial line", limit: 16, want: "second\nthird\n"},
		{name: "no complete line", limit: 3, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readTail(path, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("readTail = %q, want %q", got, tt.want)
			}
		})
	}
}
