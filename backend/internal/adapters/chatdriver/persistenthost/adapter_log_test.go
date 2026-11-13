package persistenthost

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAdapterLogKeepsStderrAndRotatesOnce(t *testing.T) {
	dir := t.TempDir()
	log := openAdapterLog(dir, 10)
	t.Cleanup(func() { _ = log.Close() })

	for _, line := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n", "eeee\n", "ffff\n"} {
		n, err := log.Write([]byte(line))
		if err != nil || n != len(line) {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", line, n, err, len(line))
		}
	}

	current, err := os.ReadFile(filepath.Join(dir, adapterLogName))
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(filepath.Join(dir, adapterLogName+".1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(previous) + string(current); !strings.HasSuffix(got, "ffff\n") || strings.Contains(got, "aaaa") {
		t.Fatalf("rotation kept %q; want the newest lines and no more than two files", got)
	}
	if _, err := os.Stat(filepath.Join(dir, adapterLogName+".2")); err == nil {
		t.Fatal("rotation must keep a single previous file")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, adapterLogName))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("log mode = %v, %v; want owner-only 0600", info, err)
		}
	}
}

func TestAdapterLogAppendsAcrossHosts(t *testing.T) {
	dir := t.TempDir()
	first := openAdapterLog(dir, adapterLogMaxBytes)
	_, _ = first.Write([]byte("before restart\n"))
	_ = first.Close()

	second := openAdapterLog(dir, adapterLogMaxBytes)
	_, _ = second.Write([]byte("after restart\n"))
	_ = second.Close()

	got, err := os.ReadFile(filepath.Join(dir, adapterLogName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before restart\nafter restart\n" {
		t.Fatalf("log = %q; a restarted host must append to the earlier run", got)
	}
}

func TestAdapterLogNeverFailsTheProvider(t *testing.T) {
	// A directory that does not exist disables the log; writes still succeed so
	// the provider's stderr copier cannot fail the process wait.
	log := openAdapterLog(filepath.Join(t.TempDir(), "missing"), adapterLogMaxBytes)
	if n, err := log.Write([]byte("lost\n")); err != nil || n != 5 {
		t.Fatalf("Write on a disabled log = %d, %v; want 5, nil", n, err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close on a disabled log = %v; want nil", err)
	}
}

func TestAdapterLogTrimsWritesLargerThanTheLimit(t *testing.T) {
	dir := t.TempDir()
	log := openAdapterLog(dir, 10)
	t.Cleanup(func() { _ = log.Close() })

	big := strings.Repeat("x", 15) + "0123456789"
	for range 2 {
		if n, err := log.Write([]byte(big)); err != nil || n != len(big) {
			t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(big))
		}
	}

	for _, name := range []string{adapterLogName, adapterLogName + ".1"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "0123456789" {
			t.Fatalf("%s = %q; want only the newest 10 bytes of an oversized write", name, got)
		}
	}
}
