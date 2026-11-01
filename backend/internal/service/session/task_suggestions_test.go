package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type listingTracker struct {
	fakeTracker
	issues []domain.Issue
	err    error
	repos  []domain.TrackerRepo
}

func (f *listingTracker) List(_ context.Context, repo domain.TrackerRepo, _ domain.ListFilter) ([]domain.Issue, error) {
	f.repos = append(f.repos, repo)
	return f.issues, f.err
}

func githubIssue(n string, title string) domain.Issue {
	return domain.Issue{
		ID:    domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "acme/repo#" + n},
		Title: title,
		State: domain.IssueOpen,
		URL:   "https://github.com/acme/repo/issues/" + n,
	}
}

func TestTaskSuggestionsOffersOpenIssuesNotAlreadyStarted(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", RepoOriginURL: "https://github.com/acme/repo.git"}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker, IssueID: "github:acme/repo#2"}
	st.sessions["other-1"] = domain.SessionRecord{ID: "other-1", ProjectID: "other", Kind: domain.KindWorker, IssueID: "#3"}
	tracker := &listingTracker{issues: []domain.Issue{
		githubIssue("1", "Fix login"),
		githubIssue("2", "Already being worked on"),
		githubIssue("3", "Same number, other project"),
		githubIssue("4", "  "),
		githubIssue("5", "Add dark mode"),
		githubIssue("6", "One too many"),
	}}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st, Tracker: tracker})

	got, err := svc.TaskSuggestions(context.Background(), "ao")
	if err != nil {
		t.Fatalf("TaskSuggestions: %v", err)
	}
	want := []TaskSuggestion{
		{Source: TaskSuggestionSourceIssue, IssueID: "github:acme/repo#1", IssueNumber: 1, Title: "Fix login", URL: "https://github.com/acme/repo/issues/1"},
		{Source: TaskSuggestionSourceIssue, IssueID: "github:acme/repo#3", IssueNumber: 3, Title: "Same number, other project", URL: "https://github.com/acme/repo/issues/3"},
		{Source: TaskSuggestionSourceIssue, IssueID: "github:acme/repo#5", IssueNumber: 5, Title: "Add dark mode", URL: "https://github.com/acme/repo/issues/5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %#v, want %#v", got, want)
	}
	if len(tracker.repos) != 1 || tracker.repos[0].Native != "acme/repo" || tracker.repos[0].Provider != domain.TrackerProviderGitHub {
		t.Fatalf("listed repos = %#v, want acme/repo on github", tracker.repos)
	}
}

func TestTaskSuggestionsSkipAnIssueStartedFromASuggestion(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", RepoOriginURL: "https://github.com/acme/repo.git"}
	cmd := &fakeCommander{}
	cmd.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{ID: "ao-2", ProjectID: cfg.ProjectID, Kind: cfg.Kind, IssueID: cfg.IssueID}
		st.sessions[rec.ID] = rec
		return rec
	}
	tracker := &listingTracker{issues: []domain.Issue{githubIssue("12", "Fix login"), githubIssue("13", "Add dark mode")}}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st, Tracker: tracker})

	before, err := svc.TaskSuggestions(context.Background(), "ao")
	if err != nil || len(before) != 2 {
		t.Fatalf("suggestions before = %#v, %v", before, err)
	}
	// The desktop starts the first suggestion with its issue id and its own brief.
	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Work on issue #12", IssueID: before[0].IssueID}); err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if cmd.spawnedCfg.IssueID != "github:acme/repo#12" || cmd.spawnedCfg.Prompt != "Work on issue #12" {
		t.Fatalf("spawned config = %#v, want the issue recorded and the brief unchanged", cmd.spawnedCfg)
	}
	after, err := svc.TaskSuggestions(context.Background(), "ao")
	if err != nil || len(after) != 1 || after[0].IssueNumber != 13 {
		t.Fatalf("suggestions after = %#v, %v; want only #13", after, err)
	}
}

func TestTaskSuggestionsFindsFixmesBeforeTodos(t *testing.T) {
	dir := newWorkspaceRepo(t)
	writeWorkspaceFile(t, dir, "src/a.go", "package main\n// TODO: handle the retry budget\nfunc a() {} // TODO(sam): log the slow path\n")
	writeWorkspaceFile(t, dir, "src/b.ts", "/* FIXME: cache invalidation misses deletes */\nconst todoList = [] // a TODO list, not a note\n")
	writeWorkspaceFile(t, dir, "vendor/lib.go", "// FIXME: vendored code is not ours\n")
	writeWorkspaceFile(t, dir, "src/short.go", "// TODO: x\n")
	writeWorkspaceFile(t, dir, "untracked.go", "// FIXME: never committed\n")
	runGit(t, dir, "add", "src", "vendor")
	runGit(t, dir, "commit", "-m", "notes")
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Path: dir}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	got, err := svc.TaskSuggestions(context.Background(), "ao")
	if err != nil {
		t.Fatalf("TaskSuggestions: %v", err)
	}
	want := []TaskSuggestion{
		{Source: TaskSuggestionSourceTodo, Path: "src/b.ts", Line: 1, Text: "cache invalidation misses deletes"},
		{Source: TaskSuggestionSourceTodo, Path: "src/a.go", Line: 2, Text: "handle the retry budget"},
		{Source: TaskSuggestionSourceTodo, Path: "src/a.go", Line: 3, Text: "log the slow path"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestions = %#v, want %#v", got, want)
	}
}

func TestTaskSuggestionsDegradeQuietly(t *testing.T) {
	st := newFakeStore()
	// No git repo at the path and a tracker that fails: the call still succeeds.
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Path: t.TempDir(), RepoOriginURL: "https://github.com/acme/repo.git"}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st, Tracker: &listingTracker{err: errors.New("offline")}})

	got, err := svc.TaskSuggestions(context.Background(), "ao")
	if err != nil || len(got) != 0 {
		t.Fatalf("TaskSuggestions = %#v, %v; want none and no error", got, err)
	}
	if _, err := svc.TaskSuggestions(context.Background(), "missing"); err == nil {
		t.Fatal("unknown project should fail")
	}
}

func TestIssueNumber(t *testing.T) {
	for in, want := range map[string]int{
		"acme/repo#12":                          12,
		"github:acme/repo#12":                   12,
		"#7":                                    7,
		"42":                                    42,
		"https://github.com/acme/repo/issues/9": 9,
		"gitlab:group/proj#5@gitlab.example":    5,
		"":                                      0,
		"no-number":                             0,
	} {
		if got := issueNumber(in); got != want {
			t.Errorf("issueNumber(%q) = %d, want %d", in, got, want)
		}
	}
}
