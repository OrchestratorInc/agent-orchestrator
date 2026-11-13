package dictation

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func installedModel(t *testing.T, s *Service, id string) {
	t.Helper()
	m, ok := findModel(id)
	if !ok {
		t.Fatal("unknown fixture")
	}
	for _, path := range []string{s.python(), filepath.Join(filepath.Dir(filepath.Dir(s.python())), "ao-ready"), filepath.Join(s.modelPath(m), "config.json"), filepath.Join(s.modelPath(m), "packed_manifest.json"), filepath.Join(s.root, id+".ready")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestModelsSelectionAndDeletion(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	defer s.Close()
	if err := s.Select(context.Background(), "../../other"); err == nil {
		t.Fatal("accepted unknown model")
	}
	if err := s.Select(context.Background(), "phonon-2.5-en"); err == nil {
		t.Fatal("selected absent model")
	}
	installedModel(t, s, "phonon-2.5")
	installedModel(t, s, "phonon-2.5-en")
	if err := s.Select(context.Background(), "phonon-2.5-en"); err != nil {
		t.Fatal(err)
	}
	restored := New(dir)
	defer restored.Close()
	if statusForTest(t, restored).Selected != "phonon-2.5-en" {
		t.Fatal("selection was not persisted")
	}
	if err := s.Delete(context.Background(), "phonon-2.5-en"); err == nil {
		t.Fatal("deleted selected model")
	}
	if err := s.Delete(context.Background(), "phonon-2.5"); err != nil {
		t.Fatal(err)
	}
	if s.installed(models[0]) {
		t.Fatal("deleted model still ready")
	}
	s.recording = true
	if err := s.Select(context.Background(), "phonon-2.5-en"); err == nil {
		t.Fatal("switched during recording")
	}
	if err := s.Install(context.Background(), "phonon-2.5"); err == nil {
		t.Fatal("installed during recording")
	}
	if _, err := s.Open(context.Background()); err == nil {
		t.Fatal("opened second recording")
	}
}
func TestPartialInstallationIsNotReady(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	installedModel(t, s, "phonon-2.5")
	if err := os.Remove(filepath.Join(s.root, "phonon-2.5.ready")); err != nil {
		t.Fatal(err)
	}
	if s.installed(models[0]) {
		t.Fatal("interrupted install is ready")
	}
	if err := s.Select(context.Background(), "phonon-2.5"); err == nil {
		t.Fatal("selected interrupted install")
	}
}
func TestSelectionRejectsNativeOnOtherPlatforms(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("native macOS is available")
	}
	s := New(t.TempDir())
	defer s.Close()
	if err := s.Select(context.Background(), "native-macos"); err == nil {
		t.Fatal("accepted native on another platform")
	}
}

type bufferCloser struct{ strings.Builder }

func (*bufferCloser) Close() error { return nil }
func TestAudioAndFinishProtocol(t *testing.T) {
	input := &bufferCloser{}
	session := &Session{worker: &worker{input: input}}
	if err := session.Audio(make([]byte, 3200)); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := session.Finish(); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(input.String()))
	var audio, stop map[string]string
	if err := decoder.Decode(&audio); err != nil {
		t.Fatal(err)
	}
	if audio["type"] != "audio" {
		t.Fatal(audio)
	}
	if err := decoder.Decode(&stop); err != nil {
		t.Fatal(err)
	}
	if stop["type"] != "stop" {
		t.Fatal(stop)
	}
	if err := decoder.Decode(&stop); err != io.EOF {
		t.Fatal("duplicate finish", err)
	}
	if err := session.Audio(make([]byte, 4)); err == nil {
		t.Fatal("accepted audio after stop")
	}
}
func TestAudioLimits(t *testing.T) {
	for _, size := range []int{0, 3, 64004} {
		t.Run(string(rune(size+65)), func(t *testing.T) {
			session := &Session{worker: &worker{input: &bufferCloser{}}}
			if session.Audio(make([]byte, size)) == nil {
				t.Fatal("accepted invalid audio")
			}
		})
	}
	session := &Session{worker: &worker{input: &bufferCloser{}}, bytes: 16000 * 4 * 300}
	if session.Audio(make([]byte, 4)) == nil {
		t.Fatal("accepted overlong audio")
	}
}
func TestCancelAndCloseStopOwnedProcesses(t *testing.T) {
	s := New(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelInstall = cancel
	s.CancelInstall()
	if ctx.Err() == nil {
		t.Fatal("installer not cancelled")
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	s.worker = &worker{cancel: workerCancel}
	s.Close()
	if workerCtx.Err() == nil {
		t.Fatal("worker not cancelled")
	}
	if err := s.Install(context.Background(), "phonon-2.5"); err == nil {
		t.Fatal("installed after shutdown")
	}
}

func statusForTest(t *testing.T, s *Service) Status {
	t.Helper()
	status, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestSelectedModelPreloadsAndStaysWarmBetweenRecordings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX worker fixture")
	}
	dir := t.TempDir()
	s := New(dir)
	installedModel(t, s, "phonon-2.5-en")
	script := `#!/bin/sh
echo '{"type":"ready"}'
while IFS= read -r command; do
  case "$command" in
    *start*) echo '{"type":"listening"}' ;;
    *cancel*) echo '{"type":"text","text":"discard me"}'; echo '{"type":"cancelled"}' ;;
    *stop*) echo '{"type":"end","text":""}' ;;
  esac
done
`
	if err := os.WriteFile(s.python(), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.python(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(context.Background(), "phonon-2.5-en"); err != nil {
		t.Fatal(err)
	}
	awaitEvent := func(w *worker, kind string) {
		t.Helper()
		select {
		case event := <-w.events:
			if event.Type != kind {
				t.Fatalf("got %q, want %q", event.Type, kind)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("waiting for %s", kind)
		}
	}
	s.mu.Lock()
	selected := s.worker
	s.mu.Unlock()
	if selected == nil {
		t.Fatal("select did not prepare model")
	}
	awaitEvent(selected, "ready")
	s.Close()
	s = New(dir)
	defer s.Close()
	s.mu.Lock()
	prepared := s.worker
	s.mu.Unlock()
	if prepared == nil {
		t.Fatal("startup did not prepare saved model")
	}
	awaitEvent(prepared, "ready")
	for attempt := range 4 {
		session, err := s.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if session.worker != prepared {
			t.Fatal("reloaded prepared model")
		}
		awaitEvent(prepared, "listening")
		if attempt < 2 {
			session.Close(false)
			continue
		}
		if err := session.Finish(); err != nil {
			t.Fatal(err)
		}
		awaitEvent(prepared, "end")
		session.Close(true)
	}
}

func TestOpenCanCancelWhileWorkerResets(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	s.worker = &worker{resetting: make(chan struct{}), cancel: func() {}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Open(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Open returned %v", err)
	}
}

func TestCancelledWorkerTimeoutReleasesWaiters(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	reader, writer := io.Pipe()
	events := make(chan Event)
	var once sync.Once
	w := &worker{input: writer, events: events, cancel: func() {
		once.Do(func() { _ = reader.Close(); close(events) })
	}}
	s.worker = w
	s.recording = true
	session := &Session{svc: s, worker: w}
	session.Close(false)
	s.mu.Lock()
	done := w.resetting
	s.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stuck reset blocked future recordings")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.worker != nil {
		t.Fatal("retained unresponsive worker")
	}
}

func TestModelIsNotInstalledUntilItsRuntimeIsFullyBuilt(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	installedModel(t, s, "phonon-2.5")
	m, _ := findModel("phonon-2.5")
	if !s.installed(m) {
		t.Fatal("fixture should be installed")
	}
	// A rebuild in progress leaves the interpreter and old model files but no runtime marker.
	if err := os.Remove(filepath.Join(filepath.Dir(filepath.Dir(s.python())), "ao-ready")); err != nil {
		t.Fatal(err)
	}
	if s.installed(m) {
		t.Fatal("half-built runtime reported as installed")
	}
}
