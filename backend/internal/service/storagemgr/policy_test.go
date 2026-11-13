package storagemgr

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var policyNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func quietWorktree(id domain.SessionID) WorktreeUsage {
	return WorktreeUsage{Path: "/data/worktrees/p/" + string(id), SessionID: id, State: WorktreeQuiet, Bytes: 2000, DependencyBytes: 1500}
}

func findAction(actions []PlannedAction, kind domain.StorageCleanKind, target string) (PlannedAction, bool) {
	for _, action := range actions {
		if action.Kind == kind && action.Target == target {
			return action, true
		}
	}
	return PlannedAction{}, false
}

func TestOffloadThresholdFollowsPullRequestState(t *testing.T) {
	settings := domain.DefaultStorageSettings()
	cases := []struct {
		name     string
		session  sessionView
		pressure domain.DiskPressureLevel
		quiet    time.Duration
		auto     bool
	}{
		{"merged after 30m", sessionView{PRCount: 1, PRsDone: true}, domain.DiskPressureNormal, 31 * time.Minute, true},
		{"merged too soon", sessionView{PRCount: 1, PRsDone: true}, domain.DiskPressureNormal, 10 * time.Minute, false},
		{"no PR after 2h", sessionView{}, domain.DiskPressureNormal, 3 * time.Hour, true},
		{"no PR too soon", sessionView{}, domain.DiskPressureNormal, time.Hour, false},
		{"open PR before 48h", sessionView{PRCount: 1}, domain.DiskPressureNormal, 47 * time.Hour, false},
		{"open PR after 48h", sessionView{PRCount: 1}, domain.DiskPressureNormal, 49 * time.Hour, true},
		{"open PR low disk after 12h", sessionView{PRCount: 1}, domain.DiskPressureLow, 13 * time.Hour, true},
		{"open PR critical at once", sessionView{PRCount: 1}, domain.DiskPressureCritical, time.Minute, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sess := tc.session
			sess.ID, sess.Quiet, sess.LastActivity = "p-1", true, policyNow.Add(-tc.quiet)
			snap := &scanSnapshot{Sessions: []sessionView{sess}, Worktrees: []WorktreeUsage{quietWorktree("p-1")}}
			actions := plan(policyInput{Now: policyNow, Settings: settings, Pressure: tc.pressure, Snapshot: snap})
			action, ok := findAction(actions, domain.StorageCleanOffload, "/data/worktrees/p/p-1")
			if !ok {
				t.Fatalf("no offload planned: %+v", actions)
			}
			if action.Automatic != tc.auto || action.Bytes != 1500 {
				t.Fatalf("action = %+v, want automatic=%v bytes=1500", action, tc.auto)
			}
		})
	}
}

func TestOffloadNeverPlannedForProtectedSessions(t *testing.T) {
	settings := domain.DefaultStorageSettings()
	base := sessionView{ID: "p-1", Quiet: true, LastActivity: policyNow.Add(-100 * time.Hour)}
	cases := map[string]func(*sessionView, *WorktreeUsage, *domain.StorageSettings){
		"orchestrator":      func(s *sessionView, _ *WorktreeUsage, _ *domain.StorageSettings) { s.Orchestrator = true },
		"workspace project": func(s *sessionView, _ *WorktreeUsage, _ *domain.StorageSettings) { s.WorkspaceKind = true },
		"project opted out": func(s *sessionView, _ *WorktreeUsage, _ *domain.StorageSettings) { s.OffloadDisabled = true },
		"offload off":       func(_ *sessionView, _ *WorktreeUsage, st *domain.StorageSettings) { st.OffloadEnabled = false },
		"agent may run":     func(_ *sessionView, w *WorktreeUsage, _ *domain.StorageSettings) { w.State = WorktreeLive },
		"nothing to free":   func(_ *sessionView, w *WorktreeUsage, _ *domain.StorageSettings) { w.DependencyBytes = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sess, wt, st := base, quietWorktree("p-1"), settings
			mutate(&sess, &wt, &st)
			actions := plan(policyInput{Now: policyNow, Settings: st, Snapshot: &scanSnapshot{Sessions: []sessionView{sess}, Worktrees: []WorktreeUsage{wt}}})
			if _, ok := findAction(actions, domain.StorageCleanOffload, wt.Path); ok {
				t.Fatalf("offload planned for %s: %+v", name, actions)
			}
		})
	}
}

func TestEndedWorktreesAutomaticAfterRetentionUnlessKeptForChanges(t *testing.T) {
	settings := domain.DefaultStorageSettings()
	ended := func(id domain.SessionID, age time.Duration, cleanup domain.WorkspaceDisposition) (sessionView, WorktreeUsage) {
		return sessionView{ID: id, Terminated: true, EndedAt: policyNow.Add(-age), Cleanup: cleanup},
			WorktreeUsage{Path: "/w/" + string(id), SessionID: id, State: WorktreeEnded, Bytes: 100}
	}
	s1, w1 := ended("e-1", 8*day, "")
	s2, w2 := ended("e-2", 2*day, "")
	s3, w3 := ended("e-3", 30*day, domain.DispositionPreservedDirty)
	snap := &scanSnapshot{Sessions: []sessionView{s1, s2, s3}, Worktrees: []WorktreeUsage{w1, w2, w3}}
	actions := plan(policyInput{Now: policyNow, Settings: settings, Snapshot: snap})
	for target, wantAuto := range map[string]bool{"/w/e-1": true, "/w/e-2": false, "/w/e-3": false} {
		action, ok := findAction(actions, domain.StorageCleanEndedWorktrees, target)
		if !ok || action.Automatic != wantAuto {
			t.Fatalf("%s: action=%+v ok=%v, want automatic=%v", target, action, ok, wantAuto)
		}
	}

	settings.EndedWorktreeDays = 0
	actions = plan(policyInput{Now: policyNow, Settings: settings, Snapshot: snap})
	if action, _ := findAction(actions, domain.StorageCleanEndedWorktrees, "/w/e-1"); action.Automatic {
		t.Fatal("endedWorktreeDays=0 must never remove automatically")
	}
}

func TestOrphansOnlyCleanGitWorktreesAndNeverAutomatic(t *testing.T) {
	snap := &scanSnapshot{Worktrees: []WorktreeUsage{
		{Path: "/w/clean", State: WorktreeOrphan, GitWorktree: true, Clean: true, Bytes: 10},
		{Path: "/w/dirty", State: WorktreeOrphan, GitWorktree: true, Clean: false, Bytes: 10},
		{Path: "/w/notgit", State: WorktreeOrphan, Bytes: 10},
	}}
	actions := plan(policyInput{Now: policyNow, Settings: domain.DefaultStorageSettings(), Snapshot: snap})
	action, ok := findAction(actions, domain.StorageCleanOrphanWorktrees, "/w/clean")
	if !ok || action.Automatic {
		t.Fatalf("clean orphan action=%+v ok=%v, want manual only", action, ok)
	}
	for _, target := range []string{"/w/dirty", "/w/notgit"} {
		if _, ok := findAction(actions, domain.StorageCleanOrphanWorktrees, target); ok {
			t.Fatalf("%s must not be offered", target)
		}
	}
}

func TestFilePoliciesKeepNewestBackupAndRespectAges(t *testing.T) {
	settings := domain.DefaultStorageSettings()
	snap := &scanSnapshot{
		Backups: []fileEntry{
			{Path: "/d/ao.db.backup-new", ModTime: policyNow.Add(-40 * day)},
			{Path: "/d/ao.db.backup-old", ModTime: policyNow.Add(-60 * day)},
			{Path: "/d/ao.db.bak-young", ModTime: policyNow.Add(-2 * day)},
		},
		Logs: []fileEntry{
			{Path: "/s/logs/old.jsonl", ModTime: policyNow.Add(-20 * day)},
			{Path: "/s/logs/active.jsonl", ModTime: policyNow.Add(-time.Hour)},
		},
		DevProfiles: []devProfile{
			{fileEntry: fileEntry{Path: "/s/dev/stale", ModTime: policyNow.Add(-30 * day)}},
			{fileEntry: fileEntry{Path: "/s/dev/running", ModTime: policyNow.Add(-30 * day)}, Running: true},
		},
		Templates: []templateEntry{
			{fileEntry: fileEntry{Path: "/d/dep-templates/a"}, LastUsed: policyNow.Add(-40 * day)},
			{fileEntry: fileEntry{Path: "/d/dep-templates/b"}, LastUsed: policyNow.Add(-2 * day)},
		},
		Database: DatabaseUsage{FreeBytes: 64 << 20},
		Trash:    []fileEntry{{Path: "/d/storage-trash/x"}},
	}
	actions := plan(policyInput{Now: policyNow, Settings: settings, Snapshot: snap})
	checks := []struct {
		kind    domain.StorageCleanKind
		target  string
		present bool
		auto    bool
	}{
		{domain.StorageCleanBackups, "/d/ao.db.backup-new", false, false},
		{domain.StorageCleanBackups, "/d/ao.db.backup-old", true, true},
		{domain.StorageCleanBackups, "/d/ao.db.bak-young", true, false},
		{domain.StorageCleanLogs, "/s/logs/old.jsonl", true, true},
		{domain.StorageCleanLogs, "/s/logs/active.jsonl", false, false},
		{domain.StorageCleanDevProfiles, "/s/dev/stale", true, false},
		{domain.StorageCleanDevProfiles, "/s/dev/running", false, false},
		{domain.StorageCleanTemplates, "/d/dep-templates/a", true, true},
		{domain.StorageCleanTemplates, "/d/dep-templates/b", true, false},
		{domain.StorageCleanDatabase, "ao.db", true, true},
		{domain.StorageCleanTrash, "/d/storage-trash/x", true, true},
	}
	for _, check := range checks {
		action, ok := findAction(actions, check.kind, check.target)
		if ok != check.present || ok && action.Automatic != check.auto {
			t.Fatalf("%s %s: action=%+v ok=%v, want present=%v auto=%v", check.kind, check.target, action, ok, check.present, check.auto)
		}
	}

	settings.CleanDevProfiles = true
	actions = plan(policyInput{Now: policyNow, Settings: settings, Snapshot: snap})
	if action, _ := findAction(actions, domain.StorageCleanDevProfiles, "/s/dev/stale"); !action.Automatic {
		t.Fatal("opted-in dev profile cleanup should be automatic")
	}
}

func TestAutoCleanOffKeepsEverythingManualExceptTrash(t *testing.T) {
	settings := domain.DefaultStorageSettings()
	settings.AutoClean = false
	sess := sessionView{ID: "p-1", Quiet: true, LastActivity: policyNow.Add(-100 * time.Hour)}
	snap := &scanSnapshot{
		Sessions:  []sessionView{sess},
		Worktrees: []WorktreeUsage{quietWorktree("p-1")},
		Logs:      []fileEntry{{Path: "/s/logs/old", ModTime: policyNow.Add(-30 * day)}},
		Trash:     []fileEntry{{Path: "/t/x"}},
	}
	for _, action := range plan(policyInput{Now: policyNow, Settings: settings, Snapshot: snap}) {
		if action.Automatic != (action.Kind == domain.StorageCleanTrash) {
			t.Fatalf("action %+v: only finishing the trash may stay automatic", action)
		}
	}
}

func TestSelectActionsFiltersKindsSessionsAndDevProfiles(t *testing.T) {
	planned := []PlannedAction{
		{Kind: domain.StorageCleanOffload, SessionID: "a", Automatic: true},
		{Kind: domain.StorageCleanOffload, SessionID: "b"},
		{Kind: domain.StorageCleanDevProfiles, Target: "/dev/x"},
		{Kind: domain.StorageCleanLogs, Target: "/l", Automatic: true},
	}
	if got := selectActions(planned, CleanRequest{}); len(got) != 3 {
		t.Fatalf("default request = %+v, want everything but the manual dev profile", got)
	}
	if got := selectActions(planned, CleanRequest{IncludeDev: true}); len(got) != 4 {
		t.Fatalf("include dev = %+v", got)
	}
	if got := selectActions(planned, CleanRequest{Kinds: []domain.StorageCleanKind{domain.StorageCleanDevProfiles}}); len(got) != 1 {
		t.Fatalf("explicit dev kind = %+v", got)
	}
	if got := selectActions(planned, CleanRequest{SessionIDs: []domain.SessionID{"b"}}); len(got) != 1 || got[0].SessionID != "b" {
		t.Fatalf("session filter = %+v", got)
	}
	if got := selectActions(planned, CleanRequest{AutomaticOnly: true}); len(got) != 2 {
		t.Fatalf("automatic only = %+v", got)
	}
}
