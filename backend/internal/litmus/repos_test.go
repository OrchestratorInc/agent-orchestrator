package litmus

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// renamesGH answers `gh repo view <arg> --json nameWithOwner` from a table; an arg not in it fails.
type renamesGH struct {
	names map[string]string
	calls []string
}

func (g *renamesGH) Run(_ context.Context, args ...string) ([]byte, error) {
	g.calls = append(g.calls, strings.Join(args, " "))
	if name, ok := g.names[args[2]]; ok {
		return []byte(`{"nameWithOwner":"` + name + `"}`), nil
	}
	return nil, errors.New("HTTP 404: Not Found")
}

func projectList(ps ...Registered) func(context.Context) ([]Registered, error) {
	return func(context.Context) ([]Registered, error) { return ps, nil }
}

func TestRepoResolverMatchesEitherNameBeforeAskingGH(t *testing.T) {
	gh := &renamesGH{}
	resolve := newRepoResolver(gh, projectList(
		Registered{Path: "/other", URLs: []string{"https://github.com/acme/other.git", ""}},
		Registered{Path: "/ao", URLs: []string{"git@github.com:Untrivial-ai/agent-orchestrator.git", "https://github.com/acme/upstream"}},
	))
	for _, names := range [][]string{
		{"orchestratorinc/agent-orchestrator", "untrivial-ai/agent-orchestrator"}, // the requested name matches the origin
		{"acme/upstream", "x/y"}, // the canonical upstream URL matches
	} {
		if got, err := resolve(ctx, names...); err != nil || got != "/ao" {
			t.Fatalf("resolve(%v) = %q, %v", names, got, err)
		}
	}
	if len(gh.calls) != 0 {
		t.Fatalf("gh called for a plain match: %q", gh.calls)
	}
}

func TestRepoResolverFollowsRenamesAndCaches(t *testing.T) {
	const old = "https://github.com/Untrivial-ai/agent-orchestrator.git"
	gh := &renamesGH{names: map[string]string{old: "OrchestratorInc/agent-orchestrator"}}
	resolve := newRepoResolver(gh, projectList(
		Registered{Path: "/gitlab", URLs: []string{"https://gitlab.com/g/p.git"}},
		Registered{Path: "/down", URLs: []string{"https://github.com/gh/fails"}}, // gh errors for it: skipped
		Registered{Path: "/ao", URLs: []string{old}},
	))
	for range 2 {
		if got, err := resolve(ctx, "orchestratorinc/agent-orchestrator", "aoagents/agent-orchestrator"); err != nil || got != "/ao" {
			t.Fatalf("resolve = %q, %v", got, err)
		}
	}
	// The gitlab URL never reaches gh; the old URL is asked once (cached), while names and URLs gh
	// failed on are asked again on the second call (a gh failure is not cached).
	want := []string{
		"repo view orchestratorinc/agent-orchestrator --json nameWithOwner",
		"repo view aoagents/agent-orchestrator --json nameWithOwner",
		"repo view https://github.com/gh/fails --json nameWithOwner",
		"repo view " + old + " --json nameWithOwner",
		"repo view orchestratorinc/agent-orchestrator --json nameWithOwner",
		"repo view aoagents/agent-orchestrator --json nameWithOwner",
		"repo view https://github.com/gh/fails --json nameWithOwner",
	}
	if !slices.Equal(gh.calls, want) {
		t.Fatalf("gh calls:\n%q\nwant:\n%q", gh.calls, want)
	}
}

func TestRepoResolverFollowsARenameOfTheRequestedName(t *testing.T) {
	// The project is registered under the new name; the user's URL still has the old one.
	gh := &renamesGH{names: map[string]string{"untrivial-ai/agent-orchestrator": "OrchestratorInc/agent-orchestrator"}}
	resolve := newRepoResolver(gh, projectList(Registered{Path: "/ao", URLs: []string{"https://github.com/orchestratorinc/agent-orchestrator"}}))
	if got, err := resolve(ctx, "untrivial-ai/agent-orchestrator"); err != nil || got != "/ao" {
		t.Fatalf("resolve = %q, %v", got, err)
	}
}

func TestRepoResolverNamesWhatItLookedFor(t *testing.T) {
	gh := &renamesGH{names: map[string]string{"https://github.com/acme/web": "acme/web-app"}}
	resolve := newRepoResolver(gh, projectList(Registered{Path: "/web", URLs: []string{"https://github.com/acme/web"}}))
	_, err := resolve(ctx, "orchestratorinc/agent-orchestrator", "Untrivial-ai/agent-orchestrator", "orchestratorinc/agent-orchestrator")
	want := "no registered AO project matches orchestratorinc/agent-orchestrator or untrivial-ai/agent-orchestrator (registered: acme/web (now acme/web-app))"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want prefix %q", err, want)
	}
	_, err = newRepoResolver(gh, projectList())(ctx, "a/b")
	if err == nil || !strings.Contains(err.Error(), "(registered: none)") {
		t.Fatalf("empty registry err = %v", err)
	}
	boom := errors.New("db down")
	if _, err = newRepoResolver(gh, func(context.Context) ([]Registered, error) { return nil, boom })(ctx, "a/b"); !errors.Is(err, boom) {
		t.Fatalf("list error not wrapped: %v", err)
	}
}

func TestIntakeRecordsRequestedRepoAndBlocksWhenUnresolved(t *testing.T) {
	svc, _ := newSvc(t)
	var asked []string
	svc.WithRepos(func(_ context.Context, names ...string) (string, error) {
		asked = names
		return "", errors.New("no registered AO project matches " + strings.Join(names, " or "))
	})
	run, err := svc.Intake(ctx, "s1", prURL)
	if err != nil || run.HeadSHA == "" || run.State != RunIntake { // still pinned: the worker can read the pack and submit --blocked
		t.Fatalf("run %+v err %v", run, err)
	}
	if run.RequestedRepo != "aoagents/agent-orchestrator" || run.Repo != "orchestratorinc/agent-orchestrator" ||
		!slices.Equal(asked, []string{run.Repo, run.RequestedRepo}) {
		t.Fatalf("run %+v asked %v", run, asked)
	}
	if f := svc.runner.(*fakeRunner); len(f.calls) != 0 || len(run.Gaps) != 1 || !strings.HasPrefix(run.Gaps[0], "I did not read the PR's code with git") { // no clone of a repo nobody registered
		t.Fatalf("git calls %q gaps %q", f.calls, run.Gaps)
	}
	const msg = "blocked at prepare: no registered AO project matches orchestratorinc/agent-orchestrator or aoagents/agent-orchestrator"
	if st, err := svc.Status(ctx, "s1"); err != nil || st.Run.Error != msg {
		t.Fatalf("status %+v err %v", st.Run, err)
	}
	is(t, "TESTING_REPO_UNRESOLVED")(svc.Prepare(ctx, "s1")) // the same lookup, the same message

	ok, _ := newSvc(t)
	ok.WithRepos(func(context.Context, ...string) (string, error) { return "/local", nil })
	if run, err := ok.Intake(ctx, "s1", prURL); err != nil || run.Error != "" {
		t.Fatalf("resolvable run %+v err %v", run, err)
	}
}

func TestStartRefusesAnUnresolvableRepoBeforeSpawning(t *testing.T) {
	spawned := 0
	svc := New(NewStore(t.TempDir()), nil).WithSpawner(func(context.Context, SpawnRequest) (string, error) { spawned++; return "ao-1", nil })
	svc.WithRepos(func(_ context.Context, names ...string) (string, error) {
		if names[0] == "acme/web" {
			return "/web", nil
		}
		return "", errors.New("no registered AO project matches " + names[0])
	})
	_, err := svc.Start(ctx, StartRequest{PRURL: "acme/other#1"})
	is(t, "TESTING_REPO_UNRESOLVED")(nil, err)
	if spawned != 0 || err.Error() != "no registered AO project matches acme/other" {
		t.Fatalf("spawned %d err %v", spawned, err)
	}
	if _, err := svc.Start(ctx, StartRequest{PRURL: "acme/web#1"}); err != nil || spawned != 1 {
		t.Fatalf("spawned %d err %v", spawned, err)
	}
}
