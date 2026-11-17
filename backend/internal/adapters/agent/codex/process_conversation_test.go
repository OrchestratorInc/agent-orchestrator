package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessConversationIDPicksNewestNonSubagentRollout(t *testing.T) {
	const (
		left     = "01a1e89d-06d0-7cf0-9c9a-80fe33e88516"
		subagent = "01a1e89d-791a-7732-aee3-b295c4500014"
		current  = "01a1e89e-42ec-78f0-a8d4-180a4fa9e741"
	)
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "11", "17")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	write := func(id, source string, age time.Duration) string {
		t.Helper()
		path := filepath.Join(dir, "rollout-2026-11-17T13-56-08-"+id+".jsonl")
		body := `{"type":"session_meta","payload":{"id":"` + id + `","source":` + source + `}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, base.Add(-age), base.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return path
	}
	open := []string{
		"/dev/null",
		write(left, `"cli"`, 3*time.Minute),
		filepath.Join(filepath.Dir(dir), "thread-writer-locks", current+".lock"),
		write(subagent, `{"subagent":{"thread_spawn":{"parent_thread_id":"`+left+`"}}}`, 0),
		write(current, `"cli"`, time.Minute),
	}
	processOpenFiles = func(context.Context, int) ([]string, error) { return open, nil }
	t.Cleanup(func() { processOpenFiles = defaultProcessOpenFiles })

	got, ok, err := (&Plugin{}).ProcessConversationID(context.Background(), 4242)
	if err != nil || !ok || got != current {
		t.Fatalf("ProcessConversationID = %q, %v, %v; want %q", got, ok, err, current)
	}

	open = open[:1]
	if got, ok, err := (&Plugin{}).ProcessConversationID(context.Background(), 4242); err != nil || ok || got != "" {
		t.Fatalf("ProcessConversationID with no rollout open = %q, %v, %v; want none", got, ok, err)
	}

	processOpenFiles = func(context.Context, int) ([]string, error) { return nil, errors.New("gone") }
	if _, ok, err := (&Plugin{}).ProcessConversationID(context.Background(), 4242); err == nil || ok {
		t.Fatalf("ProcessConversationID after a failed read = %v, %v; want an error", ok, err)
	}
}
