package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/skillbridge"
)

type skillAgent struct {
	fakeAgent
	native   []string
	layout   string
	requests *[]ports.AgentSkillRequest
}

func (a skillAgent) SkillSupport(_ context.Context, request ports.AgentSkillRequest) ports.AgentSkillSupport {
	if a.requests != nil {
		*a.requests = append(*a.requests, request)
	}
	return ports.AgentSkillSupport{Native: a.native, Layout: a.layout}
}

func indexedSkills(m *Manager, id domain.SessionID) []string {
	var names []string
	for _, skill := range skillbridge.ReadIndex(m.dataDir, string(id)) {
		names = append(names, skill.Name)
	}
	return names
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

func TestSkillsFollowTheSetting(t *testing.T) {
	native := t.TempDir()
	var requests []ports.AgentSkillRequest
	m, store, _, home := newSkillBridgeManager(t, skillAgent{native: []string{native}, layout: skillbridge.AgentsLayout, requests: &requests})
	rec := agySession(store, domain.SessionModeChat)
	workspace := t.TempDir()
	launch := filepath.Join(workspace, "api", "cmd")
	if err := os.MkdirAll(filepath.Join(workspace, "api", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Metadata.WorkspacePath = workspace
	store.sessions[rec.ID] = rec
	root := skillbridge.UserRoots(home)[1]
	for _, name := range []string{"mine", "built-in", "project", "child", "sibling"} {
		seedSkill(t, root, name)
	}
	seedSkill(t, native, "built-in")
	seedSkill(t, filepath.Join(workspace, ".agents", "skills"), "project")
	seedSkill(t, filepath.Join(workspace, "api", ".agents", "skills"), "child")
	seedSkill(t, filepath.Join(workspace, "web", ".claude", "skills"), "sibling")
	store.workspaceRepo = map[string][]domain.WorkspaceRepoRecord{string(rec.ProjectID): {
		{ProjectID: rec.ProjectID, Name: "api", RelativePath: "api"},
		{ProjectID: rec.ProjectID, Name: "web", RelativePath: "web"},
	}}
	sync := func() []string {
		t.Helper()
		agent, _ := m.agents.Agent(rec.Harness)
		m.syncSkills(context.Background(), agent, rec.ID, launch, nil)
		return bridgedSkills(t, m, rec.ID)
	}

	if got := sync(); got != nil {
		t.Fatalf("no setting wired: skills = %v", got)
	}
	enabled := true
	m.SetSkillSharingEnabled(func(context.Context) bool { return enabled })
	if got := sync(); !reflect.DeepEqual(got, []string{"mine"}) || !reflect.DeepEqual(indexedSkills(m, rec.ID), []string{"mine"}) {
		t.Fatalf("on: skills = %v, index %v, want only those the session lacks", got, indexedSkills(m, rec.ID))
	}
	if len(requests) == 0 || !requests[0].Chat || requests[0].WorkspacePath != launch {
		t.Fatalf("requests = %+v", requests)
	}
	// The child repositories can no longer be listed, so a user's skill that one
	// of them has could be copied over it: what was prepared stays as it was.
	seedSkill(t, root, "later")
	store.workspaceRepoErr = errors.New("database unavailable")
	if got := sync(); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("repositories unknown: skills = %v", got)
	}
	store.workspaceRepoErr = nil
	if got := sync(); !reflect.DeepEqual(got, []string{"later", "mine"}) {
		t.Fatalf("repositories known again: skills = %v", got)
	}
	enabled = false
	if got := sync(); got != nil || indexedSkills(m, rec.ID) != nil {
		t.Fatalf("off: skills = %v, index %v", got, indexedSkills(m, rec.ID))
	}
}

func TestSkillsForAnAgentThatTakesNoFolder(t *testing.T) {
	m, store, _, home := newSkillBridgeManager(t, fakeAgent{})
	rec := agySession(store, domain.SessionModeTUI)
	seedSkill(t, skillbridge.UserRoots(home)[0], "from-agents")
	seedSkill(t, skillbridge.UserRoots(home)[1], "from-claude")
	m.SetSkillSharingEnabled(func(context.Context) bool { return true })
	prompt := func(harness domain.AgentHarness) string {
		t.Helper()
		rec.Harness = harness
		store.sessions[rec.ID] = rec
		text, err := m.buildSystemPrompt(context.Background(), domain.KindWorker, rec.ProjectID, rec.ID, "", false)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	for harness, want := range map[domain.AgentHarness][]string{
		domain.HarnessDroid:  {"from-agents", "from-claude"},
		domain.HarnessGemini: {"from-claude"},
		domain.HarnessCursor: nil, // its docs say it reads every folder
	} {
		text := prompt(harness)
		m.syncSkills(context.Background(), fakeAgent{}, rec.ID, t.TempDir(), nil)
		if got := bridgedSkills(t, m, rec.ID); got != nil {
			t.Fatalf("%s: copies = %v", harness, got)
		}
		if got := indexedSkills(m, rec.ID); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: index = %v, want %v", harness, got, want)
		}
		pointer := filepath.ToSlash(skillbridge.IndexDocument(m.dataDir, string(rec.ID)))
		if strings.Contains(text, pointer) != (want != nil) {
			t.Fatalf("%s: prompt points at the list = %v, want %v", harness, want == nil, want != nil)
		}
	}
	m.SetSkillSharingEnabled(func(context.Context) bool { return false })
	if strings.Contains(prompt(domain.HarnessDroid), "## User skills") {
		t.Fatal("the prompt mentions skills while the setting is off")
	}
	m.SetSkillSharingEnabled(func(context.Context) bool { return true })
	m.agents = singleAgent{agent: skillAgent{layout: skillbridge.AgentsLayout}}
	if strings.Contains(prompt(domain.HarnessAgy), "## User skills") {
		t.Fatal("an agent that takes a folder was pointed at the list too")
	}
}

func TestSkillsPreparedBeforeLaunchAndRemovedWithTheSession(t *testing.T) {
	m, store, launcher, home := newSkillBridgeManager(t, skillAgent{layout: skillbridge.AgentsLayout})
	seedSkill(t, skillbridge.UserRoots(home)[0], "mine")
	m.SetSkillSharingEnabled(func(context.Context) bool { return true })
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
	if got := bridgedSkills(t, m, rec.ID); got != nil || indexedSkills(m, rec.ID) != nil {
		t.Fatalf("skills outlived the session: %v, %v", got, indexedSkills(m, rec.ID))
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
