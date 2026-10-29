package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	vt "github.com/unixshells/vt-go"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

// terminalScreen is the screen a captured terminal command left behind: its
// rows as plain text with trailing spaces trimmed, and the width they wrapped at.
type terminalScreen struct {
	Rows []string
	Cols int
}

// runCapturedTerminalCommand runs name on a private PTY sized like the caller's
// terminal, forwarding keystrokes and mirroring output, and returns the final
// screen. Login CLIs such as `claude setup-token` print their result only to a
// terminal, so rendering the screen is how AO reads that result back.
func runCapturedTerminalCommand(ctx context.Context, name string, args []string, stdin io.Reader, stdout io.Writer) (terminalScreen, error) {
	cols, rows := 80, 24
	if file, ok := stdout.(*os.File); ok && term.IsTerminal(file.Fd()) {
		if w, h, err := term.GetSize(file.Fd()); err == nil && w > 0 && h > 0 {
			cols, rows = w, h
		}
	}
	if file, ok := stdin.(*os.File); ok && term.IsTerminal(file.Fd()) {
		// Raw mode hands every key, Ctrl+C included, to the child's own tty.
		state, err := term.MakeRaw(file.Fd())
		if err != nil {
			return terminalScreen{}, fmt.Errorf("prepare terminal: %w", err)
		}
		defer func() { _ = term.Restore(file.Fd(), state) }()
	}
	stream, err := ptyexec.Spawn(ctx, append([]string{name}, args...), agentlaunch.ProcessEnvForLaunchBinary(ctx, name), uint16(rows), uint16(cols)) //nolint:gosec // terminal sizes fit uint16
	if err != nil {
		return terminalScreen{}, err
	}
	defer func() { _ = stream.Close() }()

	screen := vt.NewSafeEmulator(cols, rows)
	defer func() { _ = screen.Close() }()
	// The caller's real terminal already answers the child's capability
	// queries; drop the emulator's duplicate replies so its Write never blocks.
	go func() { _, _ = io.Copy(io.Discard, screen) }()
	go func() { _, _ = io.Copy(stream, stdin) }()
	// The PTY reports EIO or EOF once the child exits; either ends the capture.
	_, _ = io.Copy(io.MultiWriter(stdout, screen), stream)
	return terminalScreen{Rows: strings.Split(screen.String(), "\n"), Cols: screen.Width()}, nil
}

// secretCutoffWriter mirrors output until marker appears, then drops the rest
// so a secret the child prints never reaches the user's terminal or scrollback.
type secretCutoffWriter struct {
	out    io.Writer
	marker []byte
	// held is the end of an earlier write that could start the marker.
	held []byte
	cut  bool
}

func newSecretCutoffWriter(out io.Writer, marker string) *secretCutoffWriter {
	return &secretCutoffWriter{out: out, marker: []byte(marker)}
}

func (w *secretCutoffWriter) Write(p []byte) (int, error) {
	if w.cut {
		return len(p), nil
	}
	pending := append(w.held, p...)
	w.held = nil
	if i := bytes.Index(pending, w.marker); i >= 0 {
		w.cut = true
		if _, err := w.out.Write(pending[:i]); err != nil {
			return 0, err
		}
		// The dropped bytes may have reset colors or restored the cursor.
		_, err := io.WriteString(w.out, "\x1b[0m\x1b[?25h\r\n")
		return len(p), err
	}
	// Hold back only an ending that could begin a marker split across writes,
	// so ordinary prompts still appear immediately.
	keep := 0
	for n := min(len(w.marker)-1, len(pending)); n > 0; n-- {
		if bytes.HasSuffix(pending, w.marker[:n]) {
			keep = n
			break
		}
	}
	if _, err := w.out.Write(pending[:len(pending)-keep]); err != nil {
		return 0, err
	}
	w.held = append([]byte(nil), pending[len(pending)-keep:]...)
	return len(p), nil
}
