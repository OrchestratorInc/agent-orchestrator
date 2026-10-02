//go:build linux

package persistenthost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNamespaceLaunchCancelledAdmissionAfterReopen(t *testing.T) {
	dataDir := t.TempDir()
	identity := strings.Repeat("a", 64)
	before, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", identity)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := readNamespaceLaunch(dataDir, before.Session, identity)
	if err != nil {
		t.Fatal(err)
	}
	after, err := retireNamespaceLaunch(t.Context(), dataDir, before, true)
	if err != nil || after.Phase != "cancelled" || after.Revision != 2 {
		t.Fatal("cancellation did not persist", after, err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := grantNamespaceLaunch(t.Context(), dataDir, stale, writer); err == nil {
		t.Fatal("stale admission survived durable cancellation")
	}
	message, err := io.ReadAll(reader)
	if err != nil || len(message) != 0 {
		t.Fatal("cancelled launch received a permit", err)
	}
	if _, err := prepareNamespaceLaunch(t.Context(), dataDir, before.Session, identity); !errors.Is(err, os.ErrExist) {
		t.Fatal("cancelled identity was reusable", err)
	}
	reopened, err := readNamespaceLaunch(dataDir, before.Session, identity)
	if err != nil || reopened.Phase != after.Phase || reopened.Revision != after.Revision {
		t.Fatal("stale attempt replaced cancellation", reopened, err)
	}
	repeated, err := retireNamespaceLaunch(t.Context(), dataDir, reopened, true)
	if err != nil || repeated.Revision != reopened.Revision {
		t.Fatal("repeated cancellation rotated the journal", repeated, err)
	}
}

func TestNamespaceLaunchStrictEvidence(t *testing.T) {
	identity := strings.Repeat("b", 64)
	owner := namespaceOwner{Boot: "b419c735-b5e6-4358-81ce-a68c5f42f9b0", PID: 1234, Start: 42, Namespace: 123456, Process: 7890}
	valid := namespaceLaunch{Version: 1, Session: "session-a", Identity: identity, Phase: "retired", Revision: 5, Owner: &owner}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
		ok   bool
	}{
		{"complete", bytes.Clone, true},
		{"empty", func([]byte) []byte { return nil }, false},
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }, false},
		{"missing-owner", func(b []byte) []byte { return bytes.Replace(b, []byte(`"owner":{`), []byte(`"unknown":{`), 1) }, false},
		{"null-owner", func([]byte) []byte {
			record := valid
			record.Owner = nil
			result, _ := json.Marshal(record)
			return result
		}, false},
		{"missing-boot", func(b []byte) []byte { return bytes.Replace(b, []byte(owner.Boot), nil, 1) }, false},
		{"missing-start", func(b []byte) []byte { return bytes.Replace(b, []byte(`"start":42`), []byte(`"start":0`), 1) }, false},
		{"missing-pid", func(b []byte) []byte { return bytes.Replace(b, []byte(`"pid":1234`), []byte(`"pid":0`), 1) }, false},
		{"missing-process", func(b []byte) []byte { return bytes.Replace(b, []byte(`"process":7890`), []byte(`"process":0`), 1) }, false},
		{"missing-namespace", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"namespace":123456`), []byte(`"namespace":0`), 1)
		}, false},
		{"wrong-session", func(b []byte) []byte { return bytes.Replace(b, []byte("session-a"), []byte("session-b"), 1) }, false},
		{"wrong-identity", func(b []byte) []byte { return bytes.Replace(b, []byte(identity), []byte(strings.Repeat("c", 64)), 1) }, false},
		{"duplicate-phase", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"phase":"retired"`), []byte(`"phase":"permitted","phase":"retired"`), 1)
		}, false},
		{"duplicate-pid", func(b []byte) []byte { return bytes.Replace(b, []byte(`"pid":1234`), []byte(`"pid":0,"pid":1234`), 1) }, false},
		{"alias", func(b []byte) []byte { return bytes.Replace(b, []byte(`"start"`), []byte(`"\u017Ftart"`), 1) }, false},
		{"interrupted-state", func(b []byte) []byte { return bytes.Replace(b, []byte(`"revision":5`), []byte(`"revision":3`), 1) }, false},
		{"trailing-record", func(b []byte) []byte { return append(b, []byte(`{}`)...) }, false},
		{"exact-limit", func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), 4096-len(b))...) }, true},
		{"one-over-limit", func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), 4097-len(b))...) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if _, err := prepareNamespaceLaunch(t.Context(), dataDir, valid.Session, identity); err != nil {
				t.Fatal(err)
			}
			path, err := namespaceLaunchPath(dataDir, valid.Session, identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.edit(bytes.Clone(raw)), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = readNamespaceLaunch(dataDir, valid.Session, identity)
			if (err == nil) != tc.ok {
				t.Fatalf("evidence admission %v, want valid %v", err, tc.ok)
			}
		})
	}
}

func TestNamespaceLaunchMissingAndUnsafeEvidence(t *testing.T) {
	for _, mode := range []string{"missing", "symlink", "fifo", "inaccessible"} {
		t.Run(mode, func(t *testing.T) {
			dataDir := t.TempDir()
			record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("d", 64))
			if err != nil {
				t.Fatal(err)
			}
			path, err := namespaceLaunchPath(dataDir, record.Session, record.Identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, path+".fixture"); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "symlink":
				err = os.Symlink(path+".fixture", path)
			case "fifo":
				err = unix.Mkfifo(path, 0o600)
			case "inaccessible":
				err = os.Mkdir(path, 0o000)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readNamespaceLaunch(dataDir, record.Session, record.Identity); err == nil {
				t.Fatal("unsafe evidence admitted")
			}
			if _, err := retireNamespaceLaunch(t.Context(), dataDir, record, false); err == nil {
				t.Fatal("unsafe evidence allowed retirement acknowledgement")
			}
		})
	}
}

func TestNamespaceLaunchLockAndCancelledContext(t *testing.T) {
	dataDir := t.TempDir()
	record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	path, err := namespaceLaunchPath(dataDir, record.Session, record.Identity)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	err = withNamespaceLaunchLock(t.Context(), path, func() error {
		done := make(chan error, 1)
		go func() { _, err := retireNamespaceLaunch(ctx, dataDir, record, true); done <- err }()
		cancel()
		return <-done
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled waiter mutated admission", err)
	}
	reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
	if err != nil || reopened.Phase != "prepared" {
		t.Fatal("cancelled admission changed durable state", reopened, err)
	}
	if err := os.Rename(path+".lock", path+".old-lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(path)+".old-lock", path+".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := retireNamespaceLaunch(t.Context(), dataDir, record, true); err == nil {
		t.Fatal("symlinked lock admitted a mutation")
	}
}

func TestNamespaceLaunchPublicationRequiresLiveInit(t *testing.T) {
	dataDir := t.TempDir()
	record, err := prepareNamespaceLaunch(t.Context(), dataDir, "session-a", strings.Repeat("f", 64))
	if err != nil {
		t.Fatal(err)
	}
	process, err := readProviderProc(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	boot, err := providerBootID()
	if err != nil {
		t.Fatal(err)
	}
	inode, err := namespaceInode("self")
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	processID, err := namespaceProcessID(fd)
	if err != nil {
		processID = 2
	}
	native := namespaceOwner{Boot: boot, PID: os.Getpid(), Start: process.Start, Namespace: inode, Process: processID}
	if _, err := publishNamespaceLaunch(t.Context(), dataDir, record, native); err == nil {
		t.Fatal("uncontained native process accepted for namespace publication")
	}
	reopened, err := readNamespaceLaunch(dataDir, record.Session, record.Identity)
	if err != nil || reopened.Phase != "prepared" || reopened.Owner != nil {
		t.Fatal("rejected identity advanced launch state", reopened, err)
	}
}

func TestNamespaceProcessIdentityRejectsNonProcessDescriptor(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-process")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := namespaceProcessID(int(file.Fd())); err == nil {
		t.Fatal("ordinary filesystem inode accepted as a kernel process identity")
	}
}
