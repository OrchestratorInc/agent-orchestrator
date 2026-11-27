package deepseekharness

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const tailTestID = "0b6d1c2e-3f4a-4b5c-8d9e-0f1a2b3c4d5e"

func tailHeader(id string) string {
	return fmt.Sprintf(`{"type":"session","version":4,"id":%q,"cwd":"/work","createdAt":1,"isSeeded":false,"delegationDepth":0}`, id)
}

func tailEvent(seq int, typ string) string {
	return fmt.Sprintf(`{"type":%q,"seq":%d,"time":%d,"data":{"turn":1}}`, typ, seq, 1000+seq)
}

// appendFrame appends lines as one zstd frame, the way the harness appends a
// batch.
func appendFrame(t *testing.T, path string, lines ...string) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	frame := encoder.EncodeAll([]byte(strings.Join(lines, "\n")+"\n"), nil)
	appendBytes(t, path, frame)
	return frame
}

func appendBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func eventTypes(events []NativeHistoryEvent) []string {
	out := make([]string, len(events))
	for i, event := range events {
		out[i] = fmt.Sprintf("%d:%s", event.Seq, event.Type)
	}
	return out
}

func TestSessionTailReadsWholeFramesAsTheyLand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.v4.jsonl.zstd")
	appendFrame(t, path, tailHeader(tailTestID), tailEvent(0, "turn/start"))
	tail := NewSessionTail(path)
	ctx := context.Background()

	events, err := tail.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(events); strings.Join(got, ",") != "0:turn/start" {
		t.Fatalf("first read = %v", got)
	}
	if header, ok := tail.Header(); !ok || header.ID != tailTestID {
		t.Fatalf("header = %+v, %v", header, ok)
	}

	// The harness is mid-write: half a frame is on disk.
	encoder, _ := zstd.NewWriter(nil, zstd.WithEncoderCRC(true))
	frame := encoder.EncodeAll([]byte(tailEvent(1, "tool/call")+"\n"+tailEvent(2, "tool/result")+"\n"), nil)
	appendBytes(t, path, frame[:len(frame)/2])
	if events, err := tail.Next(ctx); err != nil || len(events) != 0 {
		t.Fatalf("read of a half-written frame = %v, %v; want nothing yet", eventTypes(events), err)
	}
	appendBytes(t, path, frame[len(frame)/2:])
	appendFrame(t, path, tailEvent(3, "turn/end"))
	events, err = tail.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(eventTypes(events), ","); got != "1:tool/call,2:tool/result,3:turn/end" {
		t.Fatalf("after the frame landed = %s", got)
	}
	if events, err := tail.Next(ctx); err != nil || len(events) != 0 {
		t.Fatalf("read with nothing new = %v, %v", eventTypes(events), err)
	}
}

func TestSessionTailReadsWholeLinesOfAPlainLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.v4.jsonl")
	appendBytes(t, path, []byte(tailHeader(tailTestID)+"\n"+tailEvent(0, "turn/start")+"\n"+`{"type":"turn/e`))
	tail := NewSessionTail(path)
	events, err := tail.Next(context.Background())
	if err != nil || strings.Join(eventTypes(events), ",") != "0:turn/start" {
		t.Fatalf("first read = %v, %v", eventTypes(events), err)
	}
	appendBytes(t, path, []byte(`nd","seq":1,"time":2,"data":{}}`+"\n"))
	events, err = tail.Next(context.Background())
	if err != nil || strings.Join(eventTypes(events), ",") != "1:turn/end" {
		t.Fatalf("after the line completed = %v, %v", eventTypes(events), err)
	}
}

func TestSessionTailStopsOnALogItCannotFollow(t *testing.T) {
	ctx := context.Background()
	t.Run("another generation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.v4.jsonl")
		appendBytes(t, path, []byte(strings.Replace(tailHeader(tailTestID), `"version":4`, `"version":5`, 1)+"\n"))
		if _, err := NewSessionTail(path).Next(ctx); !errors.Is(err, errTailUnsupported) {
			t.Fatalf("err = %v, want unsupported", err)
		}
	})
	t.Run("a gap in the sequence", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.v4.jsonl")
		appendBytes(t, path, []byte(tailHeader(tailTestID)+"\n"+tailEvent(1, "turn/start")+"\n"))
		if _, err := NewSessionTail(path).Next(ctx); !errors.Is(err, errTailUnsupported) {
			t.Fatalf("err = %v, want unsupported", err)
		}
	})
	t.Run("a log that shrank", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.v4.jsonl")
		appendBytes(t, path, []byte(tailHeader(tailTestID)+"\n"+tailEvent(0, "turn/start")+"\n"))
		tail := NewSessionTail(path)
		if _, err := tail.Next(ctx); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(tailHeader(tailTestID)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tail.Next(ctx); !errors.Is(err, errTailUnsupported) {
			t.Fatalf("err = %v, want unsupported", err)
		}
		// It stays stopped.
		if _, err := tail.Next(ctx); !errors.Is(err, errTailUnsupported) {
			t.Fatalf("second err = %v, want unsupported", err)
		}
	})
}

func TestFindSessionLogFindsTheSessionByItsID(t *testing.T) {
	home := t.TempDir()
	if _, err := FindSessionLog(home, tailTestID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("before the harness writes it: err = %v, want not exist", err)
	}
	dir := filepath.Join(home, "sessions", "--private-tmp-work--", tailTestID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := FindSessionLog(home, tailTestID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("folder with no log yet: err = %v, want not exist", err)
	}
	want := filepath.Join(dir, "session.v4.jsonl.zstd")
	appendFrame(t, want, tailHeader(tailTestID))
	got, err := FindSessionLog(home, tailTestID)
	if err != nil || got != want {
		t.Fatalf("FindSessionLog = %q, %v; want %q", got, err, want)
	}
	if _, err := FindSessionLog(home, "../etc"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an id that is not a session id must be refused, got %v", err)
	}
}

func TestHomePrefersTheLaunchEnvironment(t *testing.T) {
	t.Setenv("DSH_HOME", "/daemon/dsh")
	if got, _ := Home(map[string]string{"DSH_HOME": "/session/dsh"}); got != "/session/dsh" {
		t.Fatalf("Home = %q", got)
	}
	if got, _ := Home(nil); got != "/daemon/dsh" {
		t.Fatalf("Home without a session override = %q", got)
	}
}
