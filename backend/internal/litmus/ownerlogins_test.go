package litmus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerLoginHashBeforeAndAfterClose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	t.Setenv("PATH", bin) // never query the real Keychain
	security := filepath.Join(bin, "security")
	is(t, "")(nil, os.WriteFile(security, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	path := filepath.Join(home, ".codex", "auth.json")
	is(t, "")(nil, os.MkdirAll(filepath.Dir(path), 0o700))
	is(t, "")(nil, os.WriteFile(path, []byte("fake login"), 0o600))
	svc, _ := newSvc(t)
	run, err := svc.claim("run_s1", "s1", "o/r", 1, CommentOff, EvidenceOff)
	is(t, "")(nil, err)
	var before ownerLogins
	is(t, "")(nil, svc.store.read(run.ID, ownerLoginsFile, &before))
	if len(before.SHA256) != 64 || before.MTime.IsZero() || !before.Claude {
		t.Fatalf("incomplete baseline: %+v", before)
	}
	status, err := svc.Status(ctx, "s1")
	if err != nil || status.OwnerLogins != "owner logins: unchanged" {
		t.Fatalf("unchanged status: %+v, %v", status, err)
	}
	later := before.MTime.Add(time.Minute)
	is(t, "")(nil, os.Chtimes(path, later, later))
	if got := svc.ownerLoginStatus(ctx, run); got != "CHANGED: ~/.codex/auth.json" {
		t.Fatalf("mtime change: %s", got)
	}
	is(t, "")(nil, os.WriteFile(path, []byte("different fake login"), 0o600))
	is(t, "")(nil, os.Chtimes(path, before.MTime, before.MTime))
	// Intake retries must preserve the original hash, even if mtime is unchanged.
	is(t, "")(svc.claim("run_s1", "s1", "o/r", 1, CommentOff, EvidenceOff))
	if got := svc.ownerLoginStatus(ctx, run); got != "CHANGED: ~/.codex/auth.json" {
		t.Fatalf("hash change lost on retry: %s", got)
	}
	is(t, "")(nil, os.WriteFile(security, []byte("#!/bin/sh\nexit 1\n"), 0o700))
	is(t, "")(svc.Cleanup(ctx, "s1"))
	log, err := os.ReadFile(filepath.Join(svc.store.root, run.ID, "run.log"))
	if err != nil || !strings.Contains(string(log), "CHANGED: ~/.codex/auth.json, Claude Code-credentials\n") || strings.Contains(string(log), "fake login") {
		t.Fatalf("close log: %s, %v", log, err)
	}
}
