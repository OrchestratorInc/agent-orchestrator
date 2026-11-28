package droid

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Start each failure at a new publication, while retaining the independently
// observed target identity for the assertions and context boundary below.
func unpublishedDroidFixture(t *testing.T) (*Plugin, ports.ImportedSessionRelocation, ports.NativeSessionRef, []byte) {
	t.Helper()
	p, r, body := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ref.TranscriptPath, droidReceiptPath(r.DataDir, ref.NativeSessionID), droidReceiptPath(r.DataDir, ref.NativeSessionID) + ".verified"} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	return p, r, ref, body
}

func TestDroidSourceChangeRemovesOnlyNewUnverifiedCopyAndRetryHasOneIdentity(t *testing.T) {
	p, r, ref, body := unpublishedDroidFixture(t)
	changed := append(append([]byte(nil), body...), '\n')
	ctx := &changeDroidSourceAfterPublication{Context: context.Background(), target: ref.TranscriptPath, source: r.TranscriptPath, body: changed}
	if _, err := p.PrepareImportedSession(ctx, r); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("source-change rejection = %v", err)
	}
	if !ctx.changed || ctx.err != nil {
		t.Fatalf("did not mutate at native publication: %v, %v", ctx.changed, ctx.err)
	}
	if _, err := os.Lstat(ref.TranscriptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preparation left a native duplicate: %v", err)
	}
	if _, err := os.Lstat(droidReceiptPath(r.DataDir, ref.NativeSessionID) + ".verified"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preparation was marked verified: %v", err)
	}
	r.SourceFingerprint = NativeHistoryFingerprint(changed)
	retry, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil || retry.NativeSessionID == ref.NativeSessionID {
		t.Fatalf("new selection retry = %+v, %v", retry, err)
	}
	var nativeFiles []string
	if err := filepath.WalkDir(filepath.Join(r.ConfigDir, "sessions"), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".jsonl") {
			nativeFiles = append(nativeFiles, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(nativeFiles) != 2 { // the source and exactly one prepared conversation
		t.Fatalf("retry left duplicate native identities: %v", nativeFiles)
	}
	got, err := os.ReadFile(r.TranscriptPath)
	if err != nil || !bytes.Equal(got, changed) {
		t.Fatalf("preparation modified source history: %v", err)
	}
}

type afterDroidPublication struct {
	context.Context
	target string
	action func() error
	called bool
	err    error
}

func (c *afterDroidPublication) Err() error {
	if !c.called {
		if _, err := os.Lstat(c.target); err == nil {
			c.called = true
			c.err = c.action()
			return context.Canceled
		}
	}
	if c.called {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestDroidFailedPublicationPreservesForeignModifiedAndVerifiedArtifacts(t *testing.T) {
	for _, kind := range []string{"replacement inode", "later turns", "verified", "changed receipt"} {
		t.Run(kind, func(t *testing.T) {
			p, r, ref, _ := unpublishedDroidFixture(t)
			var want []byte
			ctx := &afterDroidPublication{Context: context.Background(), target: ref.TranscriptPath}
			ctx.action = func() error {
				body, err := os.ReadFile(ref.TranscriptPath)
				if err != nil {
					return err
				}
				want = body
				switch kind {
				case "replacement inode":
					// Even byte-identical foreign replacements must survive.
					if err := os.Rename(ref.TranscriptPath, ref.TranscriptPath+".saved"); err != nil {
						return err
					}
					return os.WriteFile(ref.TranscriptPath, body, 0o600)
				case "later turns":
					want = append(body, []byte("{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"later\"}}\n")...)
					return os.WriteFile(ref.TranscriptPath, want, 0o600)
				case "verified":
					return os.WriteFile(droidReceiptPath(r.DataDir, ref.NativeSessionID)+".verified", []byte(r.SourceFingerprint), 0o600)
				case "changed receipt":
					return os.WriteFile(droidReceiptPath(r.DataDir, ref.NativeSessionID), []byte("foreign receipt"), 0o600)
				}
				return nil
			}
			if _, err := p.PrepareImportedSession(ctx, r); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected post-publication failure: %v", err)
			}
			if !ctx.called || ctx.err != nil {
				t.Fatalf("publication boundary: %v, %v", ctx.called, ctx.err)
			}
			got, err := os.ReadFile(ref.TranscriptPath)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("failure deleted or rewrote %s: %v", kind, err)
			}
		})
	}
}

func TestDroidFailureDoesNotDeleteForeignTargetWithoutReceipt(t *testing.T) {
	p, r, _ := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{droidReceiptPath(r.DataDir, ref.NativeSessionID), droidReceiptPath(r.DataDir, ref.NativeSessionID) + ".verified"} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
		t.Fatal("foreign native target was adopted")
	}
	got, err := os.ReadFile(ref.TranscriptPath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("failure deleted foreign target: %v", err)
	}
}

func TestDroidFailureDoesNotDeletePreexistingUnverifiedCopy(t *testing.T) {
	p, r, _ := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(droidReceiptPath(r.DataDir, ref.NativeSessionID) + ".verified"); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
		t.Fatal("incomplete copy recovered without source verification")
	}
	got, err := os.ReadFile(ref.TranscriptPath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("failure deleted preexisting copy: %v", err)
	}
}

func TestDroidConcurrentPreparerCannotAdoptCopyBeingRolledBack(t *testing.T) {
	p, r, ref, _ := unpublishedDroidFixture(t)
	var competingErr error
	ctx := &afterDroidPublication{Context: context.Background(), target: ref.TranscriptPath}
	ctx.action = func() error {
		_, competingErr = p.PrepareImportedSession(context.Background(), r)
		return nil
	}
	if _, err := p.PrepareImportedSession(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected interrupted owner: %v", err)
	}
	if !ctx.called || ctx.err != nil || competingErr == nil {
		t.Fatalf("competing caller adopted incomplete copy: %v, %v, %v", ctx.called, ctx.err, competingErr)
	}
	if _, err := os.Lstat(ref.TranscriptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted owner left a duplicate: %v", err)
	}
	if again, err := p.PrepareImportedSession(context.Background(), r); err != nil || again != ref {
		t.Fatalf("retry after rollback = %+v, %v", again, err)
	}
}
