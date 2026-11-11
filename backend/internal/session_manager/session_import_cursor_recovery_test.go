package sessionmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

const cursorRecoveryID = "22222222-2222-4222-8222-222222222222"

type cursorRecoveryAgent struct {
	terminalImportAgent
	plugin *cursor.Plugin
}

func (a *cursorRecoveryAgent) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	return a.plugin.PrepareImportedSession(ctx, r)
}

func (a *cursorRecoveryAgent) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	return a.plugin.ProbeNativeSession(ctx, ref)
}

func (a *cursorRecoveryAgent) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	return a.plugin.NativeSessionConfigDir(ctx, env)
}

func (a *cursorRecoveryAgent) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return a.plugin.PrepareRuntimeLaunch(ctx, cfg)
}

type cursorRecoveryStore struct {
	*fakeStore
	failed bool
}

func (s *cursorRecoveryStore) SetSessionImportWorkspace(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) (bool, error) {
	if !s.failed && next.ResumeNativeID != "" {
		s.failed = true
		return false, errors.New("synthetic checkpoint failure after publication")
	}
	return s.fakeStore.SetSessionImportWorkspace(ctx, id, expected, next, workspace, now)
}

func (s *cursorRecoveryStore) SetPreparedImportIdentity(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) (bool, error) {
	return s.SetSessionImportWorkspace(ctx, id, expected, next, workspace, now)
}

func (*cursorRecoveryStore) ReplaceImportedMessages(context.Context, domain.SessionID, []sessionimport.Message) error {
	return nil
}

func (s *cursorRecoveryStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	projects := make([]domain.ProjectRecord, 0, len(s.projects))
	for _, project := range s.projects {
		projects = append(projects, project)
	}
	return projects, nil
}

func (*cursorRecoveryStore) CreateImportedSession(context.Context, domain.SessionRecord, []sessionimport.Message) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{}, false, errors.New("unexpected import creation in resume regression")
}

func cursorRecoveryFixture(t *testing.T, root, cwd string) string {
	t.Helper()
	path, err := cursorstore.SessionPath(root, cwd, cursorRecoveryID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE blobs (id TEXT PRIMARY KEY,data BLOB); CREATE TABLE meta (key TEXT PRIMARY KEY,value TEXT)"); err != nil {
		t.Fatal(err)
	}
	field := func(number uint64, value []byte) []byte {
		body := binary.AppendUvarint(nil, number<<3|2)
		body = binary.AppendUvarint(body, uint64(len(value)))
		return append(body, value...)
	}
	put := func(body []byte) []byte {
		sum := sha256.Sum256(body)
		if _, err := db.Exec("INSERT INTO blobs VALUES (?,?)", hex.EncodeToString(sum[:]), body); err != nil {
			t.Fatal(err)
		}
		return sum[:]
	}
	user := put(field(1, []byte("synthetic recovery prompt")))
	turn := put(field(1, field(1, user)))
	graph := put(field(8, turn))
	meta, err := json.Marshal(map[string]any{"agentId": cursorRecoveryID, "latestRootBlobId": hex.EncodeToString(graph), "name": "Recovery fixture", "createdAt": 1793600000000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta VALUES ('0',?)", hex.EncodeToString(meta)); err != nil {
		t.Fatal(err)
	}
	sidecar, err := json.Marshal(map[string]any{"schemaVersion": 1, "cwd": cwd, "title": "Recovery fixture", "updatedAtMs": 1793600000000})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "meta.json"), sidecar, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCursorResumeRecoversPublishedCopyWithoutCheckpoint(t *testing.T) {
	for _, scenario := range []string{"removed source", "changed source", "unproven target", "bad provenance"} {
		t.Run(scenario, func(t *testing.T) {
			root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
			path := cursorRecoveryFixture(t, root, cwd)
			fingerprint, err := cursorstore.Fingerprint(context.Background(), root, path)
			if err != nil {
				t.Fatal(err)
			}
			agent := &cursorRecoveryAgent{terminalImportAgent: terminalImportAgent{native: true}, plugin: cursor.New()}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, agent)
			store := &cursorRecoveryStore{fakeStore: st}
			m.store = store
			project := st.projects["mer"]
			project.Path = cwd
			st.projects[project.ID] = project
			m.SetImportArchiveRefresher(sessionimport.New(store, t.TempDir(), func(domain.AgentHarness) bool { return true }))
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessCursor, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{
				WorkspacePath: launch, Branch: "ao/import", AgentSessionID: cursorRecoveryID, ProviderConversationID: cursorRecoveryID,
				NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{
					NativeID: cursorRecoveryID, ConfigDir: root, CWD: cwd, Fingerprint: fingerprint, Prepared: true, Transferred: true,
				},
			}
			st.sessions[rec.ID] = rec
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil || !store.failed || rt.created != 0 {
				t.Fatalf("checkpoint failure not isolated: err=%v failed=%v launches=%d", err, store.failed, rt.created)
			}
			pending := st.sessions[rec.ID].Metadata.ImportSource
			if pending.ResumeNativeID != "" || pending.ResumeCwd != "" {
				t.Fatalf("failed checkpoint was persisted: %#v", pending)
			}
			target, err := cursorstore.SessionPath(root, launch, cursorRecoveryID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "changed source" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "meta.json"), []byte(`{"cwd":"/changed"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.RemoveAll(filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			if scenario == "unproven target" {
				if err := os.Remove(filepath.Join(filepath.Dir(target), ".ao-import.json")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "bad provenance" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(target), ".ao-import.json"), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Recovery must rely on the durable record, not adapter memory.
			m.agents = singleAgent{agent: &cursorRecoveryAgent{terminalImportAgent: terminalImportAgent{native: true}, plugin: cursor.New()}}
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if scenario == "unproven target" || scenario == "bad provenance" {
				if err == nil || rt.created != 0 || !st.sessions[rec.ID].NeedsImportResume() {
					t.Fatalf("unverified target resumed: result=%#v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || result.Session.Metadata.AgentSessionID != cursorRecoveryID || result.Session.Metadata.NativeTranscriptPath != target {
				t.Fatalf("normal retry failed: result=%#v err=%v", result, err)
			}
			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("recovered native snapshot was replaced: %v", err)
			}
		})
	}
}
