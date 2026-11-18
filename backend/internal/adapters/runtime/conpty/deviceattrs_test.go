package conpty

import (
	"testing"
	"time"
)

func TestDA1ScannerFindsQueries(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"\x1b[c", 1},
		{"\x1b[0c", 1},
		{"\x1b[00c", 1},
		{"prompt> \x1b[c tail", 1},
		{"\x1b[c\x1b[0c", 2},
		{"\x1b[>c", 0},    // DA2
		{"\x1b[=c", 0},    // DA3
		{"\x1b[1c", 0},    // not a DA1 query
		{"\x1b[?1;2c", 0}, // a reply, not a query
		{"\x1bc", 0},      // RIS
		{"[c", 0},
		{"\x1b\x1b[c", 1},
		{"\x1b[?u\x1b[c", 1}, // fish: kitty keyboard query, then DA1
	} {
		var s da1Scanner
		if got := s.scan([]byte(tc.in)); got != tc.want {
			t.Errorf("scan(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestDA1ScannerJoinsSplitReads(t *testing.T) {
	for _, parts := range [][]string{
		{"\x1b", "[c"},
		{"\x1b[", "c"},
		{"\x1b[0", "c"},
		{"\x1b", "[", "0", "c"},
		{"text\x1b", "[", "c more"},
	} {
		var s da1Scanner
		got := 0
		for _, p := range parts {
			got += s.scan([]byte(p))
		}
		if got != 1 {
			t.Errorf("scan(%q) = %d, want 1", parts, got)
		}
	}

	// A split that turns into something else must not match.
	var s da1Scanner
	if got := s.scan([]byte("\x1b[")) + s.scan([]byte(">c")); got != 0 {
		t.Errorf("split DA2 matched %d times", got)
	}
}

func TestDA1RepliesMatchesOnlyReplies(t *testing.T) {
	for _, in := range []string{"\x1b[?1;2c", "\x1b[?62;22c", "\x1b[?1;2c\x1b[?1;2c"} {
		if !da1Replies.MatchString(in) {
			t.Errorf("%q should be a DA1 reply", in)
		}
	}
	for _, in := range []string{"c", "\x1b[c", "\x1b[>0;276;0c", "\x1b[?1;2cx", "x\x1b[?1;2c", "\x1b[12;40R"} {
		if da1Replies.MatchString(in) {
			t.Errorf("%q should not be a DA1 reply", in)
		}
	}
}

// readPTYInput reads what the host wrote to the fake PTY, or fails after 2 s.
func readPTYInput(t *testing.T, f *serveFixture) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := f.pty.ReadInput(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for PTY input")
		return ""
	}
}

// A shell starting with no client attached still gets its DA1 answer, even
// when the query arrives split across two reads.
func TestHostAnswersDA1WithNoClient(t *testing.T) {
	if !answersDeviceAttributes {
		t.Skip("ConPTY answers DA1 itself on Windows")
	}
	f := startServe(t, 300)
	defer f.cancel()

	go func() {
		_, _ = f.pty.WriteOutput([]byte("Welcome to fish\x1b[?u\x1b"))
		_, _ = f.pty.WriteOutput([]byte("[c"))
	}()
	if got := readPTYInput(t, f); got != string(da1Reply) {
		t.Fatalf("PTY input = %q, want %q", got, da1Reply)
	}
}

// A client's own DA1 reply is dropped so the program never reads two answers;
// typed input still reaches the PTY.
func TestHostDropsClientDA1Reply(t *testing.T) {
	if !answersDeviceAttributes {
		t.Skip("ConPTY answers DA1 itself on Windows")
	}
	f := startServe(t, 301)
	defer f.cancel()

	c := newTestClient(t, f.addr)
	defer c.close()
	if err := c.send(MsgTerminalInput, []byte("\x1b[?1;2c")); err != nil {
		t.Fatal(err)
	}
	if err := c.send(MsgTerminalInput, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	if got := readPTYInput(t, f); got != "ls\r" {
		t.Fatalf("PTY input = %q, want only the typed %q", got, "ls\r")
	}
}
