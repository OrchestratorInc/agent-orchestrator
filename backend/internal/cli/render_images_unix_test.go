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

// inlineWithin fails the test if inlineLocalImages has not returned in time.
func inlineWithin(t *testing.T, html string) (missing []string, err error) {
	t.Helper()
	type result struct {
		missing []string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, missing, err := inlineLocalImages(html)
		done <- result{missing, err}
	}()
	select {
	case r := <-done:
		return r.missing, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("inlineLocalImages is stuck")
		return nil, nil
	}
}

func TestInlineLocalImagesNeverOpensAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "stream.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	opened := false
	wrapOpenRenderImage(t, func(name string, _ func(string) (*os.File, error)) (*os.File, error) {
		opened = true
		return os.Open(name)
	})

	missing, err := inlineWithin(t, `<img src="`+fifo+`">`)
	if err != nil || opened || !slices.Equal(missing, []string{fifo}) {
		t.Fatalf("err=%v opened=%v missing=%q", err, opened, missing)
	}
}

func TestInlineLocalImagesRefusesAFIFOThatReplacesAnImage(t *testing.T) {
	chart := writeImage(t, t.TempDir(), "chart.png", pngSignature)
	wrapOpenRenderImage(t, func(name string, open func(string) (*os.File, error)) (*os.File, error) {
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(name, 0o600); err != nil {
			t.Fatal(err)
		}
		return open(name)
	})

	missing, err := inlineWithin(t, `<img src="`+chart+`">`)
	if err != nil || !slices.Equal(missing, []string{chart}) {
		t.Fatalf("err=%v missing=%q", err, missing)
	}
}
