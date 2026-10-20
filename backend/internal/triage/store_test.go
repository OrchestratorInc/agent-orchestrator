package triage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunStorePrivateAtomicAndRedacted(t *testing.T) {
	dir := t.TempDir()
	store, err := openRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "private-run", State: "report_saved", Messages: []Message{{Text: `token=ghp_abcdefghijklmnopqrstuvwx`}},
		Evidence: []Evidence{{ID: "probe", Source: "/Users/alice/private/file", Text: `quoted "value"`, Values: map[string]any{
			"password": "private-plain-value", "count": 5, "nested": map[string]any{"access_token": "unusual-credential"},
		}}},
		Request: StartRequest{Origin: Origin{Machine: json.RawMessage(`{"cookie":"private-browser-cookie"}`)}},
	}
	if err := store.save(run); err != nil {
		t.Fatal(err)
	}
	path, _ := RunDir(dir, run.ID)
	raw, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ghp_abcdefghijklmnopqrstuvwx", "private-plain-value", "unusual-credential", "private-browser-cookie", "/Users/alice"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("manifest contains private value %q", secret)
		}
	}
	saved, err := store.get(run.ID)
	if err != nil || saved.Messages[0].Text == run.Messages[0].Text {
		t.Fatalf("redaction or JSON roundtrip failed: %#v, %v", saved, err)
	}
	if runtime.GOOS != "windows" {
		for _, target := range []string{store.root, filepath.Join(store.root, "runs"), path, filepath.Join(path, "manifest.json")} {
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("private permission missing on %s: %v", target, err)
			}
		}
	}
	run.State = "updated"
	if err := store.save(run); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != "manifest.json" {
		t.Fatalf("atomic write left temporary files: %#v, %v", entries, err)
	}
}

func TestRunStoreRejectsEscapesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	store, err := openRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../outside", "/absolute", `a\b`} {
		if _, err := RunDir(dir, id); err == nil {
			t.Fatalf("unsafe run id accepted: %q", id)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(store.root, "runs", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := store.save(Run{ID: "linked"}); err == nil {
		t.Fatal("symlinked run directory accepted")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("store wrote outside its private directory")
	}
}

func TestRunStoreExclusiveOwnerAndRecovery(t *testing.T) {
	store, err := openRunStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, "active.lock")
	release, err := claim(path)
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}
	defer release()
	if _, err := claim(path); !errors.Is(err, errRunOwned) {
		t.Fatalf("second helper accepted: %v", err)
	}
	now := time.Now().UTC()
	if err := store.save(Run{ID: "first", CreatedAt: now, State: "running"}); err != nil {
		t.Fatal(err)
	}
	active, _ := store.get("first")
	if active.State != "running" {
		t.Fatal("second helper changed a live owner's run")
	}
	release()
	release, err = claim(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := store.recoverInterrupted(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	saved, _ := store.get("first")
	if saved.State != "interrupted" {
		t.Fatalf("recovery did not mark the interrupted run: %#v", saved)
	}
}

func TestRunStoreStructuredRedactionKeepsJSONValid(t *testing.T) {
	raw, err := sanitizeJSON(map[string]any{
		"body":    `The error contained {"password":"not for sharing"} and a Windows path C:\Users\alice\private.`,
		"machine": map[string]any{"cookie": "session=private-browser-cookie"},
		"count":   json.Number("9007199254740993"),
	})
	if err != nil || !json.Valid(raw) {
		t.Fatalf("redaction broke JSON syntax: %s, %v", raw, err)
	}
	for _, private := range []string{"not for sharing", "private-browser-cookie", `C:\Users\alice`} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("structured redaction retained private content: %s", raw)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil || decoded["count"] != json.Number("9007199254740993") {
		t.Fatalf("redaction changed an unrelated number: %#v, %v", decoded, err)
	}
}

func TestRunStoreConcurrentDeadOwnerCleanupCannotRemoveReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.lock")
	if err := privateDir(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "2147483647-dead"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	releases := make(chan func(), 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			release, err := claim(path)
			if err == nil {
				releases <- release
			} else if !errors.Is(err, errRunOwned) {
				t.Errorf("claim failed: %v", err)
			}
		}()
	}
	close(start)
	workers.Wait()
	close(releases)
	owners := 0
	for release := range releases {
		owners++
		release()
	}
	if owners > 1 {
		t.Fatalf("dead-owner recovery granted %d concurrent owners", owners)
	}
	releaseOld, err := claim(path)
	if err != nil {
		t.Fatal(err)
	}
	releaseOld()
	releaseNew, err := claim(path)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseNew()
	releaseOld()
	if _, err := claim(path); !errors.Is(err, errRunOwned) {
		t.Fatalf("old release removed replacement ownership: %v", err)
	}
}

func TestRejectedTransitionCannotOverwriteDurableSessionCheckpoint(t *testing.T) {
	store, err := openRunStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := *startCommand("", true).Start
	request.PreparationID = "stale-run"
	run := Run{ID: request.PreparationID, Request: request, Execution: "session", State: "session_launch", Messages: []Message{}}
	if err := store.save(run); err != nil {
		t.Fatal(err)
	}
	launch, err := buildSessionLaunch(context.Background(), run, filepath.Dir(filepath.Dir(store.root)))
	if err != nil {
		t.Fatal(err)
	}
	run.Launch = &launch
	if err := store.save(run); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &host{ctx: ctx, cancel: cancel, store: store, out: io.Discard, deps: Dependencies{Now: time.Now}}
	i := h.newInvestigation(run)
	if _, err := store.sessionCheckpoint(run.ID, "", true, time.Now); err != nil {
		t.Fatal(err)
	}
	if err := i.commitIf(func(*Run) bool { return false }); !errors.Is(err, errTransitionRejected) {
		t.Fatalf("transition was not rejected: %v", err)
	}
	saved, err := store.get(run.ID)
	if err != nil || !saved.SessionLaunchStarted {
		t.Fatal("rejected stale transition overwrote POST intent")
	}
}

func TestStoreOmitsLiveNativeRequestsAndTerminalBytes(t *testing.T) {
	store, err := openRunStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "ephemeral", State: "running", PendingInput: &PendingInput{ID: "native", Kind: "approval", Detail: json.RawMessage(`{"native":"ephemeral-form"}`)}, TerminalReplay: "ephemeral-terminal", TerminalSequence: 100}
	if err := store.save(run); err != nil {
		t.Fatal(err)
	}
	saved, err := store.get(run.ID)
	if err != nil || saved.PendingInput != nil || saved.TerminalReplay != "" || saved.TerminalSequence != 0 {
		t.Fatalf("live state entered saved manifest: %#v %v", saved, err)
	}
}
