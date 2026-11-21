package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func assetImportFixture(t *testing.T, sourceMode string, chat bool) (*Plugin, ports.ImportedSessionRelocation, map[string][]byte) {
	t.Helper()
	root, path, cwd := importFixture(t)
	if sourceMode != CLIStorageDir {
		if err := os.Rename(filepath.Join(root, CLIStorageDir), filepath.Join(root, sourceMode)); err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(root, sourceMode, "conversations", importTestID+".db")
	}
	assets := map[string][]byte{
		filepath.Join("conversations", importTestID+".meta"):                            {0, 1, 2, 255},
		filepath.Join("brain", importTestID, "task.md"):                                 []byte("conversation task"),
		filepath.Join("brain", importTestID, ".system_generated", "logs", "turn.jsonl"): []byte("conversation log\n"),
	}
	for rel, body := range assets {
		path := filepath.Join(root, sourceMode, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint, err := ImportFingerprint(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.importVersion = func(context.Context) (string, error) { return supportedImportVersion, nil }
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: t.TempDir(), Chat: chat, SourceFingerprint: fingerprint, DataDir: t.TempDir()}
	return p, r, assets
}

func TestImportSnapshotsOnlySelectedConversationAssets(t *testing.T) {
	for _, sourceMode := range []string{CLIStorageDir, ACPStorageDir} {
		for _, chat := range []bool{false, true} {
			name := sourceMode + "/terminal"
			if chat {
				name = sourceMode + "/chat"
			}
			t.Run(name, func(t *testing.T) {
				p, r, assets := assetImportFixture(t, sourceMode, chat)
				for _, rel := range []string{"auth.json", filepath.Join(sourceMode, "settings.json"), filepath.Join(sourceMode, "brain", "other-conversation", "secret"), filepath.Join(sourceMode, "conversations", "other.meta")} {
					path := filepath.Join(r.ConfigDir, rel)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("profile-secret"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ref, err := p.PrepareImportedSession(context.Background(), r)
				if err != nil {
					t.Fatal(err)
				}
				mode := CLIStorageDir
				if chat {
					mode = ACPStorageDir
				}
				wantFiles := map[string]bool{importProofName: true, filepath.Join(mode, "conversations", importTestID+".db"): true}
				for rel, body := range assets {
					src, dst := filepath.Join(r.ConfigDir, sourceMode, rel), filepath.Join(ref.ConfigDir, mode, rel)
					got, err := os.ReadFile(dst)
					if err != nil || !bytes.Equal(got, body) {
						t.Fatalf("asset %s = %q, %v", rel, got, err)
					}
					si, err := os.Stat(src)
					if err != nil {
						t.Fatal(err)
					}
					di, err := os.Stat(dst)
					if err != nil || os.SameFile(si, di) || di.Mode().Perm() != 0o600 {
						t.Fatalf("asset is not a private independent file: %s, %v", rel, err)
					}
					wantFiles[filepath.Join(mode, rel)] = true
					if err := os.WriteFile(dst, []byte("later native edit"), 0o600); err != nil {
						t.Fatal(err)
					}
					got, err = os.ReadFile(src)
					if err != nil || !bytes.Equal(got, body) {
						t.Fatalf("source asset changed: %s, %v", rel, err)
					}
				}
				if err := filepath.WalkDir(ref.ConfigDir, func(path string, entry fs.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					rel, err := filepath.Rel(ref.ConfigDir, path)
					if err != nil || !wantFiles[rel] {
						t.Errorf("unexpected private file: %s, %v", rel, err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if got, err := ImportFingerprint(context.Background(), r.ConfigDir, r.TranscriptPath); err != nil || got != r.SourceFingerprint {
					t.Fatalf("source DB changed: %v", err)
				}
				if err := os.RemoveAll(r.ConfigDir); err != nil {
					t.Fatal(err)
				}
				// Recovery uses the private assets, including later native edits.
				again, err := p.PrepareImportedSession(context.Background(), r)
				if err != nil || again != ref {
					t.Fatalf("asset recovery consulted source: %+v, %v", again, err)
				}
				if err := os.Remove(filepath.Join(ref.ConfigDir, mode, "conversations", importTestID+".meta")); err != nil {
					t.Fatal(err)
				}
				if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
					t.Fatal("recovered root missing its recorded metadata sidecar")
				}
			})
		}
	}
}

func TestImportRejectsAssetLinksAndOversizeWithoutPublication(t *testing.T) {
	for _, kind := range []string{"meta link", "brain link", "nested link", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			p, r, _ := assetImportFixture(t, CLIStorageDir, false)
			meta := strings.TrimSuffix(r.TranscriptPath, ".db") + ".meta"
			brain := filepath.Join(r.ConfigDir, CLIStorageDir, "brain", importTestID)
			switch kind {
			case "meta link", "brain link":
				path := meta
				if kind == "brain link" {
					path = brain
				}
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "nested link":
				if err := os.Symlink(t.TempDir(), filepath.Join(brain, "outside")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "oversize":
				f, err := os.Create(filepath.Join(brain, "large"))
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(65 << 20)
				closeErr := f.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("large fixture: %v, %v", err, closeErr)
				}
			}
			if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
				t.Fatal("unsafe asset snapshot accepted")
			}
			entries, err := os.ReadDir(filepath.Join(r.DataDir, "native-imports", "agy"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed import left private state: %v", entries)
			}
			if got, err := ImportFingerprint(context.Background(), r.ConfigDir, r.TranscriptPath); err != nil || got != r.SourceFingerprint {
				t.Fatalf("rejection changed source DB: %v", err)
			}
		})
	}
}

func TestImportRecoversWhenOnlyOneOptionalAssetExists(t *testing.T) {
	for _, remove := range []string{filepath.Join("conversations", importTestID+".meta"), filepath.Join("brain", importTestID)} {
		t.Run(filepath.Base(remove), func(t *testing.T) {
			p, r, _ := assetImportFixture(t, CLIStorageDir, false)
			if err := os.RemoveAll(filepath.Join(r.ConfigDir, CLIStorageDir, remove)); err != nil {
				t.Fatal(err)
			}
			ref, err := p.PrepareImportedSession(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(ref.ConfigDir, CLIStorageDir, remove)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("import invented an absent optional asset: %v", err)
			}
			if err := os.RemoveAll(r.ConfigDir); err != nil {
				t.Fatal(err)
			}
			p.importVersion = func(context.Context) (string, error) { return "1.5.14", nil }
			if again, err := p.PrepareImportedSession(context.Background(), r); err != nil || again != ref {
				t.Fatalf("partial-asset receipt recovery = %+v, %v", again, err)
			}
		})
	}
}

func TestPreparedImportRecoveryDoesNotCheckInstalledVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"upgrade", nil}, {"unavailable", errors.New("provider unavailable")}} {
		t.Run(tc.name, func(t *testing.T) {
			p, r, _ := assetImportFixture(t, CLIStorageDir, true)
			ref, err := p.PrepareImportedSession(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			p.importVersion = func(context.Context) (string, error) { return "1.5.14", tc.err }
			if err := os.RemoveAll(r.ConfigDir); err != nil {
				t.Fatal(err)
			}
			if again, err := p.PrepareImportedSession(context.Background(), r); err != nil || again != ref {
				t.Fatalf("proven recovery was version-gated: %+v, %v", again, err)
			}
			if err := ValidateImportedACP(context.Background(), ref.ConfigDir, ref.NativeSessionID, r.LaunchCWD, "agy"); err != nil {
				t.Fatalf("proven ACP reopen: %v", err)
			}
			r.DataDir = t.TempDir()
			if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
				t.Fatal("new preparation bypassed version gate")
			}
			if err := os.Remove(ref.TranscriptPath); err != nil {
				t.Fatal(err)
			}
			if _, found, err := p.LocateTranscript(context.Background(), ref); err == nil && found {
				t.Fatal("missing private history accepted after update")
			}
		})
	}
}

type changeAgyAssetAfterStaging struct {
	context.Context
	parent, stagedRel, source string
	changed                   bool
	err                       error
}

func (c *changeAgyAssetAfterStaging) Err() error {
	if !c.changed {
		entries, _ := os.ReadDir(c.parent)
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".agy-") {
				continue
			}
			if _, err := os.Stat(filepath.Join(c.parent, entry.Name(), c.stagedRel)); err == nil {
				c.changed = true
				c.err = os.WriteFile(c.source, []byte("source changed concurrently"), 0o600)
				break
			}
		}
	}
	return c.Context.Err()
}

func TestImportRejectsAssetsChangingAfterSnapshot(t *testing.T) {
	p, r, _ := assetImportFixture(t, CLIStorageDir, true)
	rel := filepath.Join("brain", importTestID, "task.md")
	ctx := &changeAgyAssetAfterStaging{
		Context: context.Background(), parent: filepath.Join(r.DataDir, "native-imports", "agy"),
		stagedRel: filepath.Join(ACPStorageDir, rel), source: filepath.Join(r.ConfigDir, CLIStorageDir, rel),
	}
	if _, err := p.PrepareImportedSession(ctx, r); !errors.Is(err, errImportSelectionChanged) {
		t.Fatalf("changed asset accepted: %v", err)
	}
	if !ctx.changed || ctx.err != nil {
		t.Fatalf("asset mutation did not run at snapshot boundary: %v, %v", ctx.changed, ctx.err)
	}
	entries, err := os.ReadDir(ctx.parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected snapshot was published: %v, %v", entries, err)
	}
	got, err := os.ReadFile(ctx.source)
	if err != nil || string(got) != "source changed concurrently" {
		t.Fatalf("import rewrote concurrent source edit: %q, %v", got, err)
	}
}

func TestPreparedRecoveryAfterUpgradeStillValidatesReceiptAndPrivateAssets(t *testing.T) {
	for _, kind := range []string{"receipt", "brain missing", "private asset link", "shared source inode"} {
		t.Run(kind, func(t *testing.T) {
			p, r, _ := assetImportFixture(t, CLIStorageDir, true)
			ref, err := p.PrepareImportedSession(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			p.importVersion = func(context.Context) (string, error) {
				t.Fatal("proven root recovery consulted installed version")
				return "1.5.14", nil
			}
			switch kind {
			case "receipt":
				proof, err := readImportProof(ref.ConfigDir)
				if err != nil {
					t.Fatal(err)
				}
				proof.Fingerprint = "foreign-selection"
				body, err := json.Marshal(proof)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(ref.ConfigDir, importProofName), body, 0o600); err != nil {
					t.Fatal(err)
				}
			case "brain missing":
				if err := os.RemoveAll(filepath.Join(ref.ConfigDir, ACPStorageDir, "brain", importTestID)); err != nil {
					t.Fatal(err)
				}
			case "private asset link", "shared source inode":
				dst := filepath.Join(ref.ConfigDir, ACPStorageDir, "conversations", importTestID+".meta")
				if err := os.Remove(dst); err != nil {
					t.Fatal(err)
				}
				src := strings.TrimSuffix(r.TranscriptPath, ".db") + ".meta"
				link := os.Symlink
				if kind == "shared source inode" {
					link = os.Link
				}
				if err := link(src, dst); err != nil {
					t.Skipf("links unavailable: %v", err)
				}
			}
			if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
				t.Fatal("invalid private preparation recovered")
			}
			if _, found, err := p.LocateTranscript(context.Background(), ref); err == nil && found {
				t.Fatal("invalid private preparation located")
			}
		})
	}
}
