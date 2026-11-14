package dictation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGGUFCatalogIsPinnedAndPathsAreIsolated(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	seen := map[string]bool{}
	count := 0
	for _, m := range models {
		if seen[m.id] {
			t.Fatalf("duplicate model %s", m.id)
		}
		seen[m.id] = true
		if !m.gguf() {
			continue
		}
		count++
		if len(m.revision) != 40 || len(m.checksum) != 64 || m.bytes <= 0 || m.license == "" {
			t.Fatalf("unpinned model: %s", m.id)
		}
		if filepath.Base(m.filename) != m.filename {
			t.Fatalf("unsafe filename %s", m.filename)
		}
		rel, err := filepath.Rel(s.root, s.modelPath(m))
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatal("model escapes root")
		}
		if filepath.Dir(s.marker(m)) != s.root {
			t.Fatal("marker escapes root")
		}
	}
	if count != 4 {
		t.Fatalf("catalog has %d models, expected 4", count)
	}
}

func TestVerifiedDownloadRejectsCorruptionAndPreservesExistingFile(t *testing.T) {
	payload := "verified model"
	hash := sha256.Sum256([]byte(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
	defer server.Close()
	for _, tc := range []struct {
		name, checksum string
		size           int64
		valid          bool
	}{
		{"valid", hex.EncodeToString(hash[:]), int64(len(payload)), true},
		{"checksum", strings.Repeat("0", 64), int64(len(payload)), false},
		{"truncated", hex.EncodeToString(hash[:]), int64(len(payload) + 1), false},
		{"oversize", hex.EncodeToString(hash[:]), int64(len(payload) - 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "model.gguf")
			if err := os.WriteFile(dest, []byte("old model"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := downloadVerified(context.Background(), server.URL, dest, tc.size, tc.checksum, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("download error: %v", err)
			}
			data, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			expected := "old model"
			if tc.valid {
				expected = payload
			}
			if string(data) != expected {
				t.Fatalf("content %q", data)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 {
				t.Fatal("temporary download leaked")
			}
		})
	}
}

func TestCancelledModelDownloadLeavesNoInstallation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(t.TempDir(), "model.gguf")
	if err := downloadVerified(ctx, "http://127.0.0.1:1", dest, 10, strings.Repeat("0", 64), nil); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("cancelled model installed")
	}
}

func TestInterruptedModelDownloadCleansPartialFile(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- downloadVerified(ctx, server.URL, filepath.Join(dir, "model.gguf"), 100, strings.Repeat("0", 64), nil)
	}()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("interrupted download succeeded")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("partial model was retained")
	}
}

func TestGGUFDeletePreservesOtherModelsAndRuntime(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	first, _ := findModel("handy-computer/whisper-small-gguf")
	second, _ := findModel("handy-computer/parakeet-unified-en-0.6b-gguf")
	for _, path := range []string{s.ggufPython(), filepath.Join(filepath.Dir(filepath.Dir(s.ggufPython())), "ao-ready"), s.vadPath(), s.modelPath(first), s.marker(first), s.modelPath(second), s.marker(second)} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !s.installed(first) || !s.installed(second) {
		t.Fatal("models not recognized as installed")
	}
	s.selected = first.id
	if err := s.Delete(context.Background(), first.id); err == nil {
		t.Fatal("deleted selected model")
	}
	if err := s.Delete(context.Background(), second.id); err != nil {
		t.Fatal(err)
	}
	if !s.installed(first) || s.installed(second) {
		t.Fatal("delete affected another model or retained deleted model")
	}
	if err := s.Select(context.Background(), second.id); err == nil {
		t.Fatal("selected deleted model")
	}
}

func TestDownloadVerifiedReportsByteProgress(t *testing.T) {
	payload := strings.Repeat("speech", 4096)
	hash := sha256.Sum256([]byte(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
	defer server.Close()
	var last int64
	calls := 0
	err := downloadVerified(context.Background(), server.URL, filepath.Join(t.TempDir(), "model.gguf"), int64(len(payload)), hex.EncodeToString(hash[:]), func(done int64) {
		if done < last {
			t.Fatalf("progress went backwards: %d after %d", done, last)
		}
		last = done
		calls++
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || last != int64(len(payload)) {
		t.Fatalf("progress ended at %d of %d after %d reports", last, len(payload), calls)
	}
}

func TestWatchBlobsReportsGrowthCappedAtTotal(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	dir := filepath.Join(t.TempDir(), "blobs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s.planInstall(1)("Downloading")
	stop := s.watchBlobs(dir, 100)
	if err := os.WriteFile(filepath.Join(dir, "abc.incomplete"), make([]byte, 250), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if status, err := s.Status(context.Background()); err == nil && status.ProgressTotal == progressScale && status.ProgressDone == progressScale {
			stop()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	t.Fatal("watcher never reported the capped progress")
}

func TestInstallProgressIsOneRangeThatNeverGoesBackwards(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	step := s.planInstall(1, 3)
	read := func() int64 {
		status, err := s.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return status.ProgressDone
	}
	step("first")
	s.stepProgress(1)
	if got := read(); got != progressScale/4 {
		t.Fatalf("end of the first step = %d, want %d", got, progressScale/4)
	}
	step("second")
	if got := read(); got != progressScale/4 {
		t.Fatalf("starting the second step moved progress to %d", got)
	}
	s.stepProgress(0.5)
	half := read()
	s.stepProgress(0.1)
	if got := read(); got != half {
		t.Fatalf("progress went backwards from %d to %d", half, got)
	}
	s.stepProgress(1)
	if got := read(); got != progressScale {
		t.Fatalf("end of the last step = %d, want %d", got, progressScale)
	}
}
