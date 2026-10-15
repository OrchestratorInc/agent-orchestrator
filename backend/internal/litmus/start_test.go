package litmus

import (
	"context"
	"errors"
	"testing"
)

func TestStartSpawnsOneClaudeCodeWorkerForThePR(t *testing.T) {
	ctx := context.Background()
	var got []SpawnRequest
	svc := New(NewStore(t.TempDir()), nil).WithSpawner(func(_ context.Context, r SpawnRequest) (string, error) {
		got = append(got, r)
		return "ses-9", nil
	})

	st, err := svc.Start(ctx, StartRequest{ProjectID: " p1 ", PRURL: "acme/web#42"}) // short ref, no harness
	if err != nil || st.SessionID != "ses-9" || st.Name != "litmus: PR #42" {
		t.Fatalf("Start = %+v, %v", st, err)
	}
	want := SpawnRequest{ProjectID: "p1", Harness: "claude-code", Name: "litmus: PR #42",
		Prompt: "Test PR https://github.com/acme/web/pull/42. Start with `ao testing intake https://github.com/acme/web/pull/42`. " + ShellRule + " " + ExploreRule}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("spawn request = %+v, want %+v", got, want)
	}

	is(t, "TESTING_HARNESS_UNSUPPORTED")(svc.Start(ctx, StartRequest{PRURL: "acme/web#42", Harness: "aider"}))
	is(t, "TESTING_PR_URL_INVALID")(svc.Start(ctx, StartRequest{PRURL: "https://gitlab.com/a/b/-/merge_requests/1"}))
	if len(got) != 1 {
		t.Fatalf("a refused start spawned: %+v", got)
	}

	boom := errors.New("spawn failed")
	failing := New(NewStore(t.TempDir()), nil).WithSpawner(func(context.Context, SpawnRequest) (string, error) { return "", boom })
	if _, err := failing.Start(ctx, StartRequest{PRURL: "acme/web#42"}); !errors.Is(err, boom) {
		t.Fatalf("spawn error not passed through: %v", err)
	}
	is(t, "TESTING_SPAWN_UNAVAILABLE")(New(NewStore(t.TempDir()), nil).Start(ctx, StartRequest{PRURL: "acme/web#42"}))
}

func TestStartPicksTheHarnessFromRequestSettingWorkerThenDefault(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, request, setting, worker, want string
	}{
		{name: "nothing configured", want: "claude-code"},
		{name: "request", request: "codex", want: "codex"},
		{name: "request beats setting", request: "claude-code", setting: "codex", want: "claude-code"},
		{name: "setting", setting: "codex", want: "codex"},
		{name: "setting beats worker", setting: "claude-code", worker: "codex", want: "claude-code"},
		{name: "worker", worker: "codex", want: "codex"},
		{name: "unsupported worker is skipped", worker: "aider", want: "claude-code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []SpawnRequest
			svc := New(NewStore(t.TempDir()), nil).WithSpawner(func(_ context.Context, r SpawnRequest) (string, error) {
				got = append(got, r)
				return "ses-9", nil
			}).WithProjectHarness(func(context.Context, string) string { return tc.worker })
			if tc.setting != "" {
				if _, err := svc.SetProjectSettings(ctx, "p1", ProjectSettings{Harness: tc.setting}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.Start(ctx, StartRequest{ProjectID: "p1", PRURL: "acme/web#42", Harness: tc.request}); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Harness != tc.want {
				t.Fatalf("spawned %+v, want harness %q", got, tc.want)
			}
		})
	}
}

func TestProjectSettingsHarnessRoundTripsAndRefusesOthers(t *testing.T) {
	ctx := context.Background()
	svc := New(NewStore(t.TempDir()), nil)
	saved, err := svc.SetProjectSettings(ctx, "p1", ProjectSettings{Harness: " codex ", Model: "gpt-6.1-sol", Effort: "high"})
	if err != nil || saved.Harness != "codex" {
		t.Fatalf("saved %+v, %v", saved, err)
	}
	got, err := svc.ProjectSettings(ctx, "p1")
	if err != nil || got != (ProjectSettings{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high"}) {
		t.Fatalf("read back %+v, %v", got, err)
	}
	is(t, "TESTING_HARNESS_UNSUPPORTED")(svc.SetProjectSettings(ctx, "p1", ProjectSettings{Harness: "aider"}))
	if got, _ := svc.ProjectSettings(ctx, "p1"); got.Harness != "codex" {
		t.Fatalf("a refused save changed the settings: %+v", got)
	}
}
