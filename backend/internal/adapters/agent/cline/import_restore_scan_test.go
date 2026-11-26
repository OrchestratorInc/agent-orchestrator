package cline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func scanClineHistoryDoc(t *testing.T, doc string) (string, bool, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "h.messages.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	return scanImportedClineHistory(context.Background(), root, "h.messages.json")
}

func TestScanImportedClineHistoryStructure(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		id        string
		nonEmpty  bool
		bad       bool
	}{
		{name: "basic", doc: `{"sessionId":"abc","messages":[{"role":"user","content":"hi"}]}`, id: "abc", nonEmpty: true},
		{name: "messages first", doc: `{"messages":[1],"sessionId":"abc"}`, id: "abc", nonEmpty: true},
		{name: "whitespace", doc: " \n{ \"sessionId\" : \"abc\" ,\t\"messages\" : [ null ] }\n", id: "abc", nonEmpty: true},
		{name: "empty messages", doc: `{"sessionId":"abc","messages":[ ]}`, id: "abc"},
		{name: "messages not array", doc: `{"sessionId":"abc","messages":{"a":[1]}}`, id: "abc"},
		{name: "missing messages", doc: `{"sessionId":"abc"}`, id: "abc"},
		{name: "empty object", doc: `{}`},
		{name: "escaped quotes and brackets in strings", doc: `{"note":"a \"}]\" b \\","messages":[{"content":"x\"]}\\\"y"},"z"],"sessionId":"abc"}`, id: "abc", nonEmpty: true},
		{name: "nested key named sessionId ignored", doc: `{"sessionId":"abc","messages":[{"sessionId":"other"}]}`, id: "abc", nonEmpty: true},
		{name: "scalars", doc: `{"n":-1.5e3,"t":true,"f":false,"z":null,"sessionId":"abc","messages":[0,1]}`, id: "abc", nonEmpty: true},
		{name: "escaped session id is not a match", doc: `{"sessionId":"a\u0062c","messages":[1]}`, nonEmpty: true},
		{name: "oversized session id is not a match", doc: `{"sessionId":"` + strings.Repeat("a", maxClineHistoryFieldBytes+1) + `","messages":[1]}`, nonEmpty: true},
		{name: "non-string session id", doc: `{"sessionId":7,"messages":[1]}`, nonEmpty: true},
		{name: "truncated", doc: `{"sessionId":`, bad: true},
		{name: "unterminated string", doc: `{"sessionId":"abc`, bad: true},
		{name: "unterminated array", doc: `{"sessionId":"abc","messages":[{"a":1}`, bad: true},
		{name: "not an object", doc: `["abc"]`, bad: true},
		{name: "trailing data", doc: `{"sessionId":"abc","messages":[1]} {}`, bad: true},
		{name: "missing colon", doc: `{"sessionId" "abc"}`, bad: true},
		{name: "missing comma", doc: `{"sessionId":"abc" "messages":[1]}`, bad: true},
		{name: "bare control byte in string", doc: "{\"sessionId\":\"a\nb\"}", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, nonEmpty, err := scanClineHistoryDoc(t, tc.doc)
			if tc.bad {
				if err == nil {
					t.Fatalf("malformed document accepted: id=%q nonEmpty=%v", id, nonEmpty)
				}
				return
			}
			if err != nil || id != tc.id || nonEmpty != tc.nonEmpty {
				t.Fatalf("scan = %q, %v, %v; want %q, %v", id, nonEmpty, err, tc.id, tc.nonEmpty)
			}
		})
	}
}

// A single huge string (a large tool output) must be skipped without being
// materialized, so memory use stays far below the string's size.
func TestScanImportedClineHistorySkipsHugeStringWithoutBuffering(t *testing.T) {
	const huge = 64 << 20
	doc := `{"sessionId":"abc","messages":[{"role":"user","content":"hi"},{"role":"tool","content":"` +
		strings.Repeat("x", huge) + `"}]}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "h.messages.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	id, nonEmpty, err := scanImportedClineHistory(context.Background(), root, "h.messages.json")
	runtime.ReadMemStats(&after)
	if err != nil || id != "abc" || !nonEmpty {
		t.Fatalf("huge-string scan = %q, %v, %v", id, nonEmpty, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
		t.Fatalf("scan allocated %d bytes for a %d-byte string; string contents must not be buffered", allocated, huge)
	}
}

func TestScanImportedClineHistoryHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "h.messages.json"), []byte(`{"sessionId":"abc","messages":[1]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := scanImportedClineHistory(ctx, root, "h.messages.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan err = %v", err)
	}
}
