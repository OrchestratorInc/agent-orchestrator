package sessionmanager

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type agyChatImportFixture struct {
	manager     *Manager
	store       *transitionStore
	runtime     *transitionRuntime
	chat        *transitionChat
	agent       *agy.Plugin
	prepared    ports.NativeSessionRef
	source      string
	sourceBytes []byte
	events      *[]string
}

func newAgyImportFixture(t *testing.T, sourceStore string, mode domain.SessionMode) agyChatImportFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("synthetic POSIX Agy version responder")
	}
	ctx := context.Background()
	bin := t.TempDir()
	// Only the version probe executes. Runtime/Chat controllers are the manager
	// fakes; native preparation, validation and storage bridging use real Agy.
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf '1.5.13\\n'; exit 0; fi\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root, cwd, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	var err error
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_HOME", root)
	id := "00000000-0000-4000-8000-000000000519"
	source := filepath.Join(root, sourceStore, "conversations", id+".db")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		"PRAGMA user_version=1",
		"CREATE TABLE trajectory_meta(trajectory_id TEXT PRIMARY KEY,cascade_id TEXT,trajectory_type INTEGER,source INTEGER)",
		"CREATE TABLE trajectory_metadata_blob(id TEXT PRIMARY KEY,data BLOB)",
		"CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,has_subtrajectory NUMERIC,metadata BLOB,error_details BLOB,permissions BLOB,task_details BLOB,render_info BLOB,step_payload BLOB,step_format INTEGER)",
		"CREATE TABLE gen_metadata(idx INTEGER PRIMARY KEY,data BLOB,size INTEGER)",
		"CREATE TABLE executor_metadata(idx INTEGER PRIMARY KEY,data BLOB)",
		"CREATE TABLE parent_references(idx INTEGER PRIMARY KEY,data BLOB)",
		"CREATE TABLE battle_mode_infos(idx INTEGER PRIMARY KEY,data BLOB)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	field := func(n protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), value)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(cwd)}).String()
	meta := append(field(1, field(1, []byte(uri))), field(7, []byte(uri))...)
	if _, err := db.Exec("INSERT INTO trajectory_meta VALUES('trajectory',?,4,17)", id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO trajectory_metadata_blob VALUES('main',?)", meta); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		kind  int
		field protowire.Number
		text  string
	}{{14, 19, "remember the handoff marker"}, {15, 20, "remembered"}} {
		payload := field(item.field, field(1, []byte(item.text)))
		if _, err := db.Exec("INSERT INTO steps(idx,step_type,status,has_subtrajectory,step_payload,step_format) VALUES(?,?,3,0,?,0)", i, item.kind, payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := agy.ImportFingerprint(ctx, root, source)
	if err != nil {
		t.Fatal(err)
	}
	manager, store, rt, chat, events := newTransitionManager(t, mode)
	useFastInterfaceTransitionTimings(manager)
	manager.dataDir = t.TempDir()
	manager.runFilePath = filepath.Join(manager.dataDir, "running.json")
	plugin := agy.New()
	prepared, err := plugin.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: id, ConfigDir: root, TranscriptPath: source, SourceCWD: cwd, LaunchCWD: workspace, DataDir: manager.dataDir, SourceFingerprint: fingerprint, Chat: mode == domain.SessionModeChat})
	if err != nil {
		t.Fatal(err)
	}
	manager.agents = singleAgent{agent: plugin}
	store.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: cwd, Config: domain.ProjectConfig{Env: map[string]string{"GEMINI_HOME": root}}}
	rec := store.sessions["session-1"]
	rec.Harness = domain.HarnessAgy
	rec.Metadata.WorkspacePath = workspace
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = id, id
	rec.Metadata.NativeTranscriptPath = prepared.TranscriptPath
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: id, ConfigDir: root, CWD: cwd, Fingerprint: fingerprint, Transferred: true, Prepared: true, Adopted: true, ResumeNativeID: id, ResumeConfigDir: prepared.ConfigDir, ResumeCwd: workspace, ResumeTranscriptPath: prepared.TranscriptPath}
	store.sessions[rec.ID] = rec
	return agyChatImportFixture{manager: manager, store: store, runtime: rt, chat: chat, agent: plugin, prepared: prepared, source: source, sourceBytes: before, events: events}
}

func TestImportedAgyFirstChatToTerminalHandoffUsesRealAdapter(t *testing.T) {
	f := newAgyImportFixture(t, agy.ACPStorageDir, domain.SessionModeChat)
	ctx := context.Background()
	rec := f.store.sessions["session-1"]
	terminal := filepath.Join(f.prepared.ConfigDir, agy.CLIStorageDir, "conversations", f.prepared.NativeSessionID+".db")
	restore := ports.RestoreConfig{Session: ports.SessionRef{WorkspacePath: rec.Metadata.WorkspacePath, Metadata: nativeRestoreMetadata(rec)}}
	// Actual launch still demands the terminal copy, even when ACP is valid.
	if _, _, err := f.agent.GetRestoreCommand(ctx, restore); err == nil {
		t.Fatal("actual terminal restore accepted an absent target database")
	}
	if err := f.manager.preflightInterfaceTarget(ctx, rec, domain.SessionInterfaceTransition{TargetMode: domain.SessionModeTUI, NativeConversationID: f.prepared.NativeSessionID}); err != nil {
		t.Fatalf("preflight rejected the existing ACP binding: %v", err)
	}
	if _, err := os.Stat(terminal); !os.IsNotExist(err) {
		t.Fatalf("preflight bridged storage before stopping Chat: %v", err)
	}
	transition, err := f.manager.StartInterfaceTransition(ctx, rec.ID, domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
	if err != nil {
		t.Fatal(err)
	}
	settled := awaitTransition(t, f.store, transition.ID)
	if settled.Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("handoff = %s: %s", settled.Phase, settled.ErrorDetail)
	}
	rec = f.store.sessions[rec.ID]
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != f.prepared.NativeSessionID {
		t.Fatalf("handoff lost native identity: mode=%s id=%s", rec.Mode, rec.Metadata.AgentSessionID)
	}
	if f.runtime.created != 1 || f.runtime.lastCfg.Env["GEMINI_HOME"] != f.prepared.ConfigDir || !slices.Contains(f.runtime.lastCfg.Argv, f.prepared.NativeSessionID) {
		t.Fatalf("terminal did not launch the prepared conversation: %v", f.runtime.lastCfg.Argv)
	}
	stop, start := slices.Index(*f.events, "stop:chat"), slices.Index(*f.events, "start:tui")
	if stop < 0 || start <= stop {
		t.Fatalf("controller order: %v", *f.events)
	}
	if _, err := agy.ReadImportArchive(ctx, f.prepared.ConfigDir, terminal); err != nil {
		t.Fatalf("terminal native copy is invalid: %v", err)
	}
	after, err := os.ReadFile(f.source)
	if err != nil || !bytes.Equal(f.sourceBytes, after) {
		t.Fatalf("original native database changed: %v", err)
	}
	// Preflight's source-mode hint must not leak into later actual launches.
	if err := os.Remove(terminal); err != nil {
		t.Fatal(err)
	}
	restore.Session.Metadata = nativeRestoreMetadata(rec)
	if _, _, err := f.agent.GetRestoreCommand(ctx, restore); err == nil {
		t.Fatal("actual restore fell back to ACP after terminal history disappeared")
	}
}

func TestImportedAgyHandoffReadinessUsesPreparedHome(t *testing.T) {
	for _, tc := range []struct {
		name, sourceStore string
		mode              domain.SessionMode
	}{
		{"CLI-only original opened in Chat", agy.CLIStorageDir, domain.SessionModeChat},
		{"ACP-only original opened in Terminal", agy.ACPStorageDir, domain.SessionModeTUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, removeOriginal := range []bool{false, true} {
				t.Run(map[bool]string{false: "original retained", true: "original removed"}[removeOriginal], func(t *testing.T) {
					f := newAgyImportFixture(t, tc.sourceStore, tc.mode)
					ctx := context.Background()
					rec := f.store.sessions["session-1"]
					currentStore, targetStore := agy.ACPStorageDir, agy.CLIStorageDir
					if tc.mode == domain.SessionModeTUI {
						currentStore, targetStore = targetStore, currentStore
					}
					originalCurrent := filepath.Join(rec.Metadata.ImportSource.ConfigDir, currentStore, "conversations", f.prepared.NativeSessionID+".db")
					if _, err := os.Stat(originalCurrent); !os.IsNotExist(err) {
						t.Fatalf("fixture must lack current-interface history in original home: %v", err)
					}
					if removeOriginal {
						if err := os.Remove(f.source); err != nil {
							t.Fatal(err)
						}
					}
					id, err := f.manager.handoffNativeConversationID(ctx, rec)
					if err != nil || id != f.prepared.NativeSessionID {
						t.Fatalf("prepared readiness id=%q err=%v", id, err)
					}
					status, err := f.manager.InterfaceTransitionStatus(ctx, rec.ID)
					if err != nil || !status.Supported {
						t.Fatalf("prepared handoff status=%+v err=%v", status, err)
					}
					target := oppositeSessionMode(tc.mode)
					transition, err := f.manager.StartInterfaceTransition(ctx, rec.ID, target, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
					if err != nil {
						t.Fatal(err)
					}
					settled := awaitTransition(t, f.store, transition.ID)
					rec = f.store.sessions[rec.ID]
					if settled.Phase != domain.SessionInterfaceTransitionCompleted || rec.Mode != target || rec.Metadata.AgentSessionID != id || rec.Metadata.ProviderConversationID != id {
						t.Fatalf("handoff phase=%s error=%s mode=%s native=%s provider=%s", settled.Phase, settled.ErrorDetail, rec.Mode, rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID)
					}
					launchEnv := f.runtime.lastCfg.Env
					if target == domain.SessionModeChat {
						launchEnv = f.chat.start.Env
					}
					if launchEnv["GEMINI_HOME"] != f.prepared.ConfigDir {
						t.Fatalf("target home=%q, want prepared home %q", launchEnv["GEMINI_HOME"], f.prepared.ConfigDir)
					}
					bridged := filepath.Join(f.prepared.ConfigDir, targetStore, "conversations", id+".db")
					if _, err := agy.ReadImportArchive(ctx, f.prepared.ConfigDir, bridged); err != nil {
						t.Fatalf("private target history invalid: %v", err)
					}
					after, err := os.ReadFile(f.source)
					if removeOriginal {
						if !os.IsNotExist(err) {
							t.Fatalf("handoff recreated original history: %v", err)
						}
					} else if err != nil || !bytes.Equal(f.sourceBytes, after) {
						t.Fatalf("original history changed: %v", err)
					}
				})
			}
		})
	}
}

func TestImportedAgyHandoffRefusesInvalidSourceBeforeStoppingChat(t *testing.T) {
	for _, kind := range []string{"missing history", "corrupt history", "invalid proof"} {
		t.Run(kind, func(t *testing.T) {
			f := newAgyImportFixture(t, agy.ACPStorageDir, domain.SessionModeChat)
			switch kind {
			case "missing history":
				if err := os.Remove(f.prepared.TranscriptPath); err != nil {
					t.Fatal(err)
				}
			case "corrupt history":
				if err := os.WriteFile(f.prepared.TranscriptPath, []byte("not a native database"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "invalid proof":
				if err := os.WriteFile(filepath.Join(f.prepared.ConfigDir, ".ao-import.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			transition, err := f.manager.StartInterfaceTransition(context.Background(), "session-1", domain.SessionModeTUI, domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionHistoryStrict)
			if kind == "invalid proof" {
				if err != nil {
					t.Fatal(err)
				}
				settled := awaitTransition(t, f.store, transition.ID)
				if settled.Phase == domain.SessionInterfaceTransitionCompleted || settled.ErrorDetail == "" {
					t.Fatalf("invalid source handoff = %+v", settled)
				}
			} else if err == nil || transition.ID != "" {
				t.Fatalf("invalid history passed readiness: transition=%+v err=%v", transition, err)
			} else if kind == "missing history" && !errors.Is(err, ErrNativeConversationMissing) {
				t.Fatalf("missing private history error = %v", err)
			}
			if f.store.sessions["session-1"].Mode != domain.SessionModeChat || f.runtime.created != 0 || slices.Contains(*f.events, "stop:chat") {
				t.Fatalf("source stopped despite failed preflight: %v", *f.events)
			}
			terminal := filepath.Join(f.prepared.ConfigDir, agy.CLIStorageDir, "conversations", f.prepared.NativeSessionID+".db")
			if _, err := os.Stat(terminal); !os.IsNotExist(err) {
				t.Fatalf("failed preflight created target storage: %v", err)
			}
		})
	}
}
