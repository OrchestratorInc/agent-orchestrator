package persistenthost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRekeyMovesLiveHostOntoFreeSessionKey(t *testing.T) {
	dataDir := t.TempDir()
	standby := StandbyKey("worker-1")
	cfg := Config{SessionID: standby, DataDir: dataDir, Workdir: t.TempDir(),
		Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}}
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = Shutdown(context.Background(), dataDir, "worker-1")
		_ = Shutdown(context.Background(), dataDir, standby)
	})
	pid := requestProviderPID(t, transport, 1, "pid")
	before := awaitDescriptor(t, dataDir, standby)

	if err := Rekey(context.Background(), dataDir, standby, "worker-1", "fingerprint-2"); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if _, err := readDescriptor(dataDir, standby); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("standby descriptor after rekey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "chat-hosts", standby, "host.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("standby lock after rekey: %v", err)
	}
	after, err := readDescriptor(dataDir, "worker-1")
	if err != nil {
		t.Fatalf("session descriptor after rekey: %v", err)
	}
	if after.Token != before.Token || after.Address != before.Address || after.OwnershipFingerprint != "fingerprint-2" {
		t.Fatalf("descriptor after rekey = %+v, want same host with new fingerprint", after)
	}
	// The attachment made under the standby key keeps working.
	if got := requestProviderPID(t, transport, 2, "pid"); got != pid {
		t.Fatalf("provider pid after rekey = %d, want %d", got, pid)
	}
	_ = transport.Stdin.Close()

	// Shutdown by the session key retires the host and its launch directory.
	if err := Shutdown(context.Background(), dataDir, "worker-1"); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dataDir, "chat-hosts", standby)); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("standby directory survived host exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStandbyKeysAreUniquePerStandby(t *testing.T) {
	first, second := StandbyKey("worker-1"), StandbyKey("worker-1")
	if first == second {
		t.Fatalf("two standbys for one session share key %q", first)
	}
	if _, err := hostDir(t.TempDir(), first); err != nil {
		t.Fatalf("standby key %q is not a valid host key: %v", first, err)
	}
}

func TestRekeyRefusesHeldSessionKey(t *testing.T) {
	dataDir := t.TempDir()
	workdir := t.TempDir()
	start := func(key string) {
		cfg := Config{SessionID: key, DataDir: dataDir, Workdir: workdir,
			Env:  append(os.Environ(), "AO_CHAT_HOST_PROVIDER_HELPER=1"),
			Argv: []string{os.Args[0], "-test.run=TestProviderHelper"}}
		transport, err := ConnectOrStart(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		_ = transport.Stdin.Close()
		t.Cleanup(func() { _ = Shutdown(context.Background(), dataDir, key) })
	}
	start("worker-1")
	standby := StandbyKey("worker-1")
	start(standby)

	err := Rekey(context.Background(), dataDir, standby, "worker-1", "fp")
	if !errors.Is(err, ErrHostExists) {
		t.Fatalf("rekey onto a live session host = %v, want ErrHostExists", err)
	}
	if _, err := readDescriptor(dataDir, standby); err != nil {
		t.Fatalf("refused rekey must leave the standby in place: %v", err)
	}
}
