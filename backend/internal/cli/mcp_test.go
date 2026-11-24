package cli

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// `ao mcp` is the fallback for an install without the ao-mcp executable.
func TestMCPCommandServesTheTools(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var errOut bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- executeWithDeps(Deps{In: inR, Out: outW, Err: &errOut, Sleep: func(time.Duration) {}}, []string{"mcp"})
		_ = outW.Close()
	}()
	replies := bufio.NewScanner(outR)
	replies.Buffer(make([]byte, 0, 1<<20), 16<<20)
	send := func(line string) string {
		t.Helper()
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if !replies.Scan() {
			t.Fatalf("no reply to %s: %v", line, replies.Err())
		}
		return replies.Text()
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test"}}}`)
	if _, err := io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	stdout := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	_ = inW.Close()
	go func() { _, _ = io.Copy(io.Discard, outR) }()
	if err := <-done; err != nil || errOut.Len() != 0 || !strings.Contains(stdout, `"html_render"`) {
		t.Fatalf("ao mcp: err=%v stderr=%s stdout=%s", err, errOut.String(), stdout)
	}
}
