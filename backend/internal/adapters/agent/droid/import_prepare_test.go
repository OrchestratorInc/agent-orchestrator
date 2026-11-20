package droid

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	for _, kind := range []string{"workspace", "profile", "missing", "tampered"} {
		t.Run(kind, func(t *testing.T) {
			copyCfg := cfg
			copyCfg.Env = map[string]string{"HOME": r.Env["HOME"], factoryHomeEnv: ""}
			switch kind {
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
