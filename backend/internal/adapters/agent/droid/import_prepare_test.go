package droid

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const importTestID = "7f0c09e2-ce0e-43f6-ad51-a2925a075e70"

func importFixture(t *testing.T) (*Plugin, ports.ImportedSessionRelocation, []byte) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake version executable")
	}
	home := t.TempDir()
	t.Setenv(factoryHomeEnv, "")
	binary := filepath.Join(t.TempDir(), "droid")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '0.251.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := ports.ImportedSessionRelocation{NativeSessionID: importTestID, ConfigDir: filepath.Join(home, ".factory"), SourceCWD: t.TempDir(), LaunchCWD: t.TempDir(), DataDir: t.TempDir(), Env: map[string]string{"HOME": home, factoryHomeEnv: ""}}
	r.TranscriptPath = filepath.Join(r.ConfigDir, "sessions", "-source", importTestID+".jsonl")
	header, _ := json.Marshal(map[string]any{"type": "session_start", "id": importTestID, "cwd": r.SourceCWD, "version": 2})
	body := append(header, []byte("\n{\"type\":\"message\",\"id\":\"u1\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"remember marker\"}]}}\n{\"type\":\"message\",\"id\":\"a1\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"tool_use\",\"id\":\"call1\",\"name\":\"Execute\",\"input\":{\"command\":\"pwd\"}}]}}\n")...)
	if err := os.MkdirAll(filepath.Dir(r.TranscriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.TranscriptPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	r.SourceFingerprint = NativeHistoryFingerprint(body)
	return &Plugin{resolvedBinary: binary}, r, body
}

func TestPrepareImportedSessionPreservesSourceAndReusesPublishedHistory(t *testing.T) {
	p, r, body := importFixture(t)
	// Neither of these inputs belongs to a conversation copy.
	for _, path := range []string{strings.TrimSuffix(r.TranscriptPath, ".jsonl") + ".settings.json", filepath.Join(r.ConfigDir, "auth.json")} {
		if err := os.WriteFile(path, []byte("credential-sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if ref.NativeSessionID == r.NativeSessionID || ref.ConfigDir != r.ConfigDir {
		t.Fatalf("wrong copy identity: %+v", ref)
	}
	copyBody, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(copyBody), r.LaunchCWD) || !strings.Contains(string(copyBody), `"name":"Execute"`) || strings.Contains(string(copyBody), "credential-sentinel") {
		t.Fatal("copy lost history/binding or copied settings")
	}
	got, _ := os.ReadFile(r.TranscriptPath)
	if string(got) != string(body) {
		t.Fatal("original modified")
	}
	if _, err := os.Stat(strings.TrimSuffix(ref.TranscriptPath, ".jsonl") + ".settings.json"); !os.IsNotExist(err) {
		t.Fatal("source settings copied")
	}
	// Publication succeeded but AO's S2 checkpoint was lost. The original may
	// now be absent, and the prepared copy may already hold later turns.
	if err := os.WriteFile(ref.TranscriptPath, append(copyBody, []byte("{\"type\":\"message\",\"id\":\"a2\",\"message\":{\"role\":\"assistant\",\"content\":\"later turn\"}}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	again, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil || again != ref {
		t.Fatalf("retry: %+v %v", again, err)
	}
	got, _ = os.ReadFile(ref.TranscriptPath)
	if !strings.Contains(string(got), "later turn") {
		t.Fatal("retry replaced later turns")
	}
	info, _ := os.Stat(ref.TranscriptPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("copy is not private")
	}
	// Native title updates must not strand a valid conversation on reopen.
	lines := strings.SplitN(string(got), "\n", 2)
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	header["title"] = "Renamed imported conversation"
	updated, _ := json.Marshal(header)
	if err := os.WriteFile(ref.TranscriptPath, append(append(updated, '\n'), []byte(lines[1])...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(context.Background(), r); err != nil {
		t.Fatalf("title update prevented recovery: %v", err)
	}
}

func TestDroidPreparationRefusesUnprovenTargetAndSourceLinks(t *testing.T) {
	t.Run("target without receipt", func(t *testing.T) {
		p, r, _ := importFixture(t)
		ref, err := p.PrepareImportedSession(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(droidReceiptPath(r.DataDir, ref.NativeSessionID)); err != nil {
			t.Fatal(err)
		}
		if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
			t.Fatal("unproven collision reused")
		}
	})
	t.Run("source symlink", func(t *testing.T) {
		p, r, body := importFixture(t)
		outside := filepath.Join(t.TempDir(), importTestID+".jsonl")
		if err := os.WriteFile(outside, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(r.TranscriptPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, r.TranscriptPath); err != nil {
			t.Fatal(err)
		}
		if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
			t.Fatal("source symlink accepted")
		}
	})
}

func TestDroidRecoversReservedPublicationBeforeNativeCopy(t *testing.T) {
	p, r, _ := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	// This is the durable state after the receipt was reserved but before the
	// native link was published, e.g. cancellation interrupted WriteOnce.
	if err := os.Remove(ref.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(droidReceiptPath(r.DataDir, ref.NativeSessionID) + ".verified"); err != nil {
		t.Fatal(err)
	}
	again, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil || again != ref {
		t.Fatalf("reserved publication did not recover: %+v %v", again, err)
	}
}

func TestDroidCancellationCannotOrphanNativeTranscript(t *testing.T) {
	p, r, _ := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ref.TranscriptPath, droidReceiptPath(r.DataDir, ref.NativeSessionID), droidReceiptPath(r.DataDir, ref.NativeSessionID) + ".verified"} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupted := &cancelAfterDroidPublication{Context: ctx, cancel: cancel, path: ref.TranscriptPath}
	if _, err := p.PrepareImportedSession(interrupted, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation after native publication, got %v", err)
	}
	if _, err := os.Stat(droidReceiptPath(r.DataDir, ref.NativeSessionID)); err != nil {
		t.Fatalf("publication lost ownership proof: %v", err)
	}
	again, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil || again != ref {
		t.Fatalf("interrupted publication did not recover: %+v %v", again, err)
	}
	if err := os.Remove(r.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(context.Background(), r); err != nil {
		t.Fatalf("verified checkpoint recovery needs original: %v", err)
	}
}

func TestDroidSourceChangeRejectionSurvivesRetry(t *testing.T) {
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
	mutating := &changeDroidSourceAfterPublication{Context: context.Background(), target: ref.TranscriptPath, source: r.TranscriptPath, body: append(body, []byte("\n ")...)}
	if _, err := p.PrepareImportedSession(mutating, r); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("concurrent change accepted: %v", err)
	}
	if mutating.err != nil {
		t.Fatal(mutating.err)
	}
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
		t.Fatal("retry bypassed detected source change")
	}
	if err := os.Remove(r.TranscriptPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
		t.Fatal("missing source promoted rejected publication")
	}
}

type changeDroidSourceAfterPublication struct {
	context.Context
	target, source string
	body           []byte
	changed        bool
	err            error
}

func (c *changeDroidSourceAfterPublication) Err() error {
	if !c.changed {
		if _, err := os.Stat(c.target); err == nil {
			c.changed = true
			c.err = os.WriteFile(c.source, c.body, 0o600)
		}
	}
	return c.Context.Err()
}

// Cancel at the first context check after the native file becomes visible.
// This exercises the real WriteOnce boundary without a production test hook.
type cancelAfterDroidPublication struct {
	context.Context
	cancel context.CancelFunc
	path   string
}

func (c *cancelAfterDroidPublication) Err() error {
	if _, err := os.Stat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestDroidRejectsManyBlankRecords(t *testing.T) {
	if err := validateDroidHistory([]byte(strings.Repeat("\n", 1<<20)), importTestID, ""); err == nil {
		t.Fatal("blank records accepted as history")
	}
}

func TestPrepareImportedSessionRefusesChangedSourceAndUnknownFormats(t *testing.T) {
	for _, kind := range []string{"changed", "schema", "empty", "identity", "truncated", "version"} {
		t.Run(kind, func(t *testing.T) {
			p, r, body := importFixture(t)
			switch kind {
			case "changed":
				body = append(body, ' ')
			case "schema":
				body = []byte(strings.Replace(string(body), `"version":2`, `"version":3`, 1))
				r.SourceFingerprint = NativeHistoryFingerprint(body)
			case "empty":
				body = body[:strings.IndexByte(string(body), '\n')+1]
				r.SourceFingerprint = NativeHistoryFingerprint(body)
			case "identity":
				body = []byte(strings.Replace(string(body), importTestID, "00000000-0000-0000-0000-000000000000", 1))
				r.SourceFingerprint = NativeHistoryFingerprint(body)
			case "truncated":
				body = append(body, []byte("{broken")...)
				r.SourceFingerprint = NativeHistoryFingerprint(body)
			case "version":
				if err := os.WriteFile(p.resolvedBinary, []byte("#!/bin/sh\nprintf '0.252.0\\n'\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(r.TranscriptPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
				t.Fatal("unsafe import accepted")
			}
		})
	}
}

func TestImportedDroidRestoreRefusesMissingHistoryWrongWorkspaceAndProfile(t *testing.T) {
	p, r, _ := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.RestoreConfig{DataDir: r.DataDir, Env: r.Env, Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: ref.NativeSessionID, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}}
	if _, ok, err := p.GetRestoreCommand(context.Background(), cfg); err != nil || !ok {
		t.Fatalf("restore: %v %v", ok, err)
	}
	exists, err := p.NativeConversationExists(context.Background(), ports.SessionRef{DataDir: r.DataDir, WorkspacePath: r.LaunchCWD, Metadata: cfg.Session.Metadata}, ref.NativeSessionID, r.Env)
	if err != nil || !exists {
		t.Fatalf("strict preflight: %v %v", exists, err)
	}
	for _, kind := range []string{"workspace", "profile", "missing", "tampered", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			copyCfg := cfg
			copyCfg.Env = map[string]string{"HOME": r.Env["HOME"], factoryHomeEnv: ""}
			switch kind {
			case "duplicate":
				data, _ := os.ReadFile(ref.TranscriptPath)
				other := filepath.Join(ref.ConfigDir, "sessions", ref.NativeSessionID+".jsonl")
				if err := os.WriteFile(other, data, 0o600); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Remove(other) }()
			case "workspace":
				copyCfg.Session.WorkspacePath = t.TempDir()
			case "profile":
				copyCfg.Env[factoryHomeEnv] = t.TempDir()
			case "missing":
				if err := os.Rename(ref.TranscriptPath, ref.TranscriptPath+".saved"); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Rename(ref.TranscriptPath+".saved", ref.TranscriptPath) }()
			case "tampered":
				data, _ := os.ReadFile(ref.TranscriptPath)
				if err := os.WriteFile(ref.TranscriptPath, []byte(strings.Replace(string(data), "remember marker", "changed content", 1)), 0o600); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.WriteFile(ref.TranscriptPath, data, 0o600) }()
			}
			if _, _, err := p.GetRestoreCommand(context.Background(), copyCfg); err == nil {
				t.Fatal("unsafe reopen accepted")
			}
		})
	}
}

func TestImportedDroidRestoreAfterBinaryUpdate(t *testing.T) {
	p, r, _ := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.resolvedBinary, []byte("#!/bin/sh\nprintf '0.252.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Only new preparations require the version that was verified for import.
	if _, err := p.PrepareImportedSession(context.Background(), r); err == nil {
		t.Fatal("unverified binary version accepted for preparation")
	}
	session := ports.SessionRef{DataDir: r.DataDir, WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: ref.NativeSessionID, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}
	cmd, ok, err := p.GetRestoreCommand(context.Background(), ports.RestoreConfig{DataDir: r.DataDir, Env: r.Env, Session: session})
	if err != nil || !ok || len(cmd) < 2 || cmd[len(cmd)-2] != "-r" || cmd[len(cmd)-1] != ref.NativeSessionID {
		t.Fatalf("updated binary could not restore same conversation: %v %v %v", cmd, ok, err)
	}
	if err := p.PrepareRuntimeLaunch(context.Background(), ports.WorkspaceHookConfig{DataDir: r.DataDir, Env: r.Env, WorkspacePath: r.LaunchCWD, NativeSessionID: ref.NativeSessionID, ImportConfigDir: ref.ConfigDir}); err != nil {
		t.Fatalf("updated binary failed runtime preparation: %v", err)
	}
	if exists, err := p.NativeConversationExists(context.Background(), session, ref.NativeSessionID, r.Env); err != nil || !exists {
		t.Fatalf("updated binary failed history preflight: %v %v", exists, err)
	}
}

func TestImportedDroidRestoreAllowsHistoryToGrowBeyondSourceLimit(t *testing.T) {
	p, r, source := importFixture(t)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(ref.TranscriptPath, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriter(f)
	record := []byte(`{"type":"message","id":"later","message":{"role":"assistant","content":"` + strings.Repeat("x", 16<<10) + "\"}}\n")
	for size := len(original); size <= maxDroidHistoryBytes; size += len(record) {
		if _, err := writer.Write(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ref.TranscriptPath)
	if err != nil || info.Size() <= maxDroidHistoryBytes {
		t.Fatalf("fixture did not cross source limit: %v %v", info, err)
	}
	if _, err := ReadNativeHistory(context.Background(), ref.ConfigDir, ref.TranscriptPath); err == nil {
		t.Fatal("source import size cap was removed")
	}
	cfg := ports.RestoreConfig{DataDir: r.DataDir, Env: r.Env, Session: ports.SessionRef{WorkspacePath: r.LaunchCWD, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: ref.NativeSessionID, ports.MetadataKeyImportConfigDir: ref.ConfigDir}}}
	if _, ok, err := p.GetRestoreCommand(context.Background(), cfg); err != nil || !ok {
		t.Fatalf("grown history could not reopen: %v %v", ok, err)
	}
	exists, err := p.NativeConversationExists(context.Background(), ports.SessionRef{DataDir: r.DataDir, WorkspacePath: r.LaunchCWD, Metadata: cfg.Session.Metadata}, ref.NativeSessionID, r.Env)
	if err != nil || !exists {
		t.Fatalf("grown history failed preflight: %v %v", exists, err)
	}
	for _, kind := range []string{"header", "prefix", "appended record"} {
		t.Run(kind, func(t *testing.T) {
			f, err := os.OpenFile(ref.TranscriptPath, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			defer func() {
				if _, err := f.WriteAt(original, 0); err != nil {
					t.Error(err)
				}
				if err := f.Truncate(info.Size()); err != nil {
					t.Error(err)
				}
			}()
			var offset int64
			change := []byte("!")
			switch kind {
			case "header":
				offset = int64(bytes.Index(original, []byte(`"version":2`)) + len(`"version":`))
				change = []byte("3")
			case "prefix":
				offset = int64(bytes.Index(original, []byte("remember marker")))
			case "appended record":
				offset = info.Size()
				change = []byte("{broken\n")
			}
			if _, err := f.WriteAt(change, offset); err != nil {
				t.Fatal(err)
			}
			if _, _, err := p.GetRestoreCommand(context.Background(), cfg); err == nil {
				t.Fatal("invalid grown history accepted")
			}
		})
	}
	got, err := os.ReadFile(r.TranscriptPath)
	if err != nil || !bytes.Equal(got, source) {
		t.Fatalf("source changed: %v", err)
	}
}

// Droid resumes in lastCwd ?? cwd. A /cwd or --cwd in the original chat leaves
// lastCwd at the user's checkout, so the copy must drop it to run in AO's
// workspace, while the original keeps it.
func TestPrepareImportedSessionDropsLastCwd(t *testing.T) {
	p, r, _ := importFixture(t)
	header, _ := json.Marshal(map[string]any{"type": "session_start", "id": importTestID, "cwd": r.SourceCWD, "lastCwd": r.SourceCWD, "version": 2})
	original, err := os.ReadFile(r.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	body := append(header, original[bytes.IndexByte(original, '\n'):]...)
	if err := os.WriteFile(r.TranscriptPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	r.SourceFingerprint = NativeHistoryFingerprint(body)
	ref, err := p.PrepareImportedSession(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	copyBody, err := os.ReadFile(ref.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	var copied map[string]any
	if err := json.Unmarshal(copyBody[:bytes.IndexByte(copyBody, '\n')], &copied); err != nil {
		t.Fatal(err)
	}
	if _, ok := copied["lastCwd"]; ok || copied["cwd"] != r.LaunchCWD {
		t.Fatalf("copy header = %v, want cwd %q and no lastCwd", copied, r.LaunchCWD)
	}
	if got, _ := os.ReadFile(r.TranscriptPath); !bytes.Equal(got, body) {
		t.Fatal("original modified")
	}
}
