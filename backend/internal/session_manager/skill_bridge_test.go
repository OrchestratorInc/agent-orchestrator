package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillbridge"
)

type skillBridgeAgent struct {
	fakeAgent
	native []string
}

func (a skillBridgeAgent) NativeSkillRoots(context.Context, string, map[string]string) []string {
	return a.native
}

func seedSkill(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bridgedSkills(t *testing.T, m *Manager, id domain.SessionID) []string {
	t.Helper()
	dir := skillbridge.SessionDir(m.dataDir, string(domain.HarnessAgy), string(id))
	entries, err := os.ReadDir(filepath.Join(dir, ".agents", "skills"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func newSkillBridgeManager(t *testing.T, agent ports.Agent) (*Manager, *fakeStore, *recordingLauncher, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	launcher := &recordingLauncher{}
	m, store, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	m.agents = singleAgent{agent: agent}
	return m, store, launcher, home
}

func agySession(store *fakeStore, mode domain.SessionMode) domain.SessionRecord {
	seedChatResumeSession(store, domain.ActivityExited)
	rec := store.sessions["mer-1"]
	rec.Harness = domain.HarnessAgy
	rec.Mode = mode
	store.sessions[rec.ID] = rec
	return rec
}

func TestSkillBridgeFollowsTheSwitch(t *testing.T) {
	native := t.TempDir()
	m, store, _, home := newSkillBridgeManager(t, skillBridgeAgent{native: []string{native}})
	rec := agySession(store, domain.SessionModeChat)
	workspace := t.TempDir()
	root := skillbridge.UserRoots(home)[1]
	for _, name := range []string{"mine", "built-in", "project", "child"} {
		seedSkill(t, root, name)
	}
	seedSkill(t, native, "built-in")
	seedSkill(t, filepath.Join(workspace, ".agents", "skills"), "project")
	seedSkill(t, filepath.Join(workspace, "api", ".agents", "skills"), "child")
	store.workspaceRepo = map[string][]domain.WorkspaceRepoRecord{
		string(rec.ProjectID): {{ProjectID: rec.ProjectID, Name: "api", RelativePath: "api"}},
	}
	sync := func() []string {
		t.Helper()
		agent, _ := m.agents.Agent(rec.Harness)
		m.syncSkillBridge(context.Background(), agent, rec.ID, workspace, nil)
		return bridgedSkills(t, m, rec.ID)
	}

	if got := sync(); got != nil {
		t.Fatalf("no switch wired: skills = %v", got)
	}
	enabled, known := false, false
	m.SetSkillBridge(func() (bool, bool) { return enabled, known })
	if got := sync(); got != nil {
		t.Fatalf("a session without skills gained %v before Developer mode was known", got)
	}
	enabled, known = true, true
	if got := sync(); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("on: skills = %v, want only those the session lacks", got)
	}
	enabled, known = false, false // the daemon restarted; the desktop has not connected
	if got := sync(); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("after a restart: skills = %v, want the session to keep them", got)
	}
	known = true
	if got := sync(); got != nil {
		t.Fatalf("off: skills = %v, want none", got)
	}
}

func TestSkillBridgeOnlyForAgentsThatSupportIt(t *testing.T) {
	m, store, _, home := newSkillBridgeManager(t, fakeAgent{})
	rec := agySession(store, domain.SessionModeChat)
	seedSkill(t, skillbridge.UserRoots(home)[0], "mine")
	m.SetSkillBridge(func() (bool, bool) { return true, true })
	m.syncSkillBridge(context.Background(), fakeAgent{}, rec.ID, t.TempDir(), nil)
	if got := bridgedSkills(t, m, rec.ID); got != nil {
		t.Fatalf("skills = %v for an agent without a skill bridge", got)
	}
}

func TestSkillBridgePreparedBeforeLaunchAndRemovedWithTheSession(t *testing.T) {
	m, store, launcher, home := newSkillBridgeManager(t, skillBridgeAgent{})
	seedSkill(t, skillbridge.UserRoots(home)[0], "mine")
	m.SetSkillBridge(func() (bool, bool) { return true, true })
	var atStart []string
	launcher.beforeStart = func(cfg ChatStart) { atStart = bridgedSkills(t, m, cfg.SessionID) }
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessAgy, RequestedMode: domain.SessionModeChat, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !reflect.DeepEqual(atStart, []string{"mine"}) {
		t.Fatalf("skills when the chat started = %v", atStart)
	}
	if err := m.recordTermination(context.Background(), rec.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := bridgedSkills(t, m, rec.ID); got != nil {
		t.Fatalf("skills outlived the session: %v", got)
	}

	// A resumed chat gets its skills again before its controller starts.
	resumed := agySession(store, domain.SessionModeChat)
	atStart = nil
	if _, err := m.ResumeAgentWithMode(context.Background(), resumed.ID); err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	if !reflect.DeepEqual(atStart, []string{"mine"}) {
		t.Fatalf("skills when the chat resumed = %v", atStart)
	}
}
