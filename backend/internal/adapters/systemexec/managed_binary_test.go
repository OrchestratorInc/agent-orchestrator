package systemexec

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func cloudflaredName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The daemon resolves the connector at mobilebridge.ManagedCloudflaredPath; a
// download that lands anywhere else would never be found.
func TestInstallManagedBinaryLandsAnExecutableAtTheManagedPath(t *testing.T) {
	t.Parallel()
	binary := []byte("#!/bin/sh\necho cloudflared version 2026.10.3\n")
	for _, tc := range []struct {
		name, member string
		asset        []byte
	}{
		{name: "plain binary", asset: binary},
		{name: "tgz archive", member: "cloudflared", asset: tarGz(t, "./cloudflared", binary)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(httpHandler(string(tc.asset)))
			defer server.Close()
			dataDir := t.TempDir()

			got, err := newAdapter(dataDir, server.Client()).InstallManagedBinary(t.Context(), ports.ManagedBinaryDownload{
				URL: server.URL + "/asset", SHA256: sha256Hex(tc.asset), ArchiveMember: tc.member, Name: cloudflaredName(),
			})
			if err != nil {
				t.Fatalf("InstallManagedBinary() error = %v", err)
			}
			if want := mobilebridge.ManagedCloudflaredPath(dataDir); got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
			body, err := os.ReadFile(got)
			if err != nil || !bytes.Equal(body, binary) {
				t.Fatalf("installed bytes = %q, %v; want the release binary", body, err)
			}
			if info, _ := os.Stat(got); runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
				t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
			}
			assertOnlyManagedBinary(t, filepath.Dir(got), filepath.Base(got))
		})
	}
}

// A tampered or corrupted download must never become runnable: no file at the
// managed path, and no executable temp file left beside it.
func TestInstallManagedBinaryRefusesAChecksumMismatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(httpHandler("#!/bin/sh\necho tampered\n"))
	defer server.Close()
	dataDir := t.TempDir()

	_, err := newAdapter(dataDir, server.Client()).InstallManagedBinary(t.Context(), ports.ManagedBinaryDownload{
		URL: server.URL + "/asset", SHA256: sha256Hex([]byte("the pinned release")), Name: cloudflaredName(),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(mobilebridge.ManagedCloudflaredPath(dataDir)); !os.IsNotExist(statErr) {
		t.Fatalf("managed path exists after a mismatch: %v", statErr)
	}
	assertOnlyManagedBinary(t, filepath.Join(dataDir, "bin"), "")
}

// A transfer that stops sending fails within the stall timeout, so the caller
// can try again inside its install time.
func TestInstallManagedBinaryGivesUpAStalledTransfer(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	adapter := newAdapter(t.TempDir(), server.Client())
	adapter.downloadStall = 200 * time.Millisecond

	started := time.Now()
	_, err := adapter.InstallManagedBinary(t.Context(), ports.ManagedBinaryDownload{
		URL: server.URL + "/asset", SHA256: sha256Hex([]byte("the pinned release")), Name: cloudflaredName(),
	})
	if !errors.Is(err, errDownloadStalled) || time.Since(started) > 5*time.Second {
		t.Fatalf("error = %v after %s, want a stall", err, time.Since(started))
	}
}

func TestInstallManagedBinaryRefusesPlainHTTP(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(httpHandler("x"))
	defer server.Close()
	_, err := newAdapter(t.TempDir(), server.Client()).InstallManagedBinary(t.Context(), ports.ManagedBinaryDownload{
		URL: server.URL, SHA256: sha256Hex([]byte("x")), Name: "cloudflared",
	})
	if err == nil || !strings.Contains(err.Error(), "absolute HTTPS") {
		t.Fatalf("error = %v, want HTTPS refusal", err)
	}
}

func assertOnlyManagedBinary(t *testing.T, dir, want string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.Name() != want {
			t.Fatalf("leftover %q in %s", entry.Name(), dir)
		}
	}
}
