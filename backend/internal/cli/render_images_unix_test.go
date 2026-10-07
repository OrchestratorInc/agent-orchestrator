//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestInlineLocalImagesReportsAFIFOAsMissingWithoutBlocking(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe.png")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	opens := 0
	stubOpenRenderImage(t, func(name string) (*os.File, error) {
		opens++
		return os.Open(name)
	})

	type result struct {
		missing []string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, missing, err := inlineLocalImages(`<img src="` + pipe + `">`)
		done <- result{missing, err}
	}()
	select {
	case r := <-done:
		// Opening a FIFO blocks until a writer appears, so it must never be opened.
		if r.err != nil || !slices.Equal(r.missing, []string{pipe}) || opens != 0 {
			t.Fatalf("err=%v missing=%q opens=%d", r.err, r.missing, opens)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("inlineLocalImages blocked on a FIFO")
	}
}
