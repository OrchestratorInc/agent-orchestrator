package litmus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const DefaultRepo = "orchestratorinc/agent-orchestrator"

var defaultRecipe, _ = recipeFiles.ReadFile("recipes/ao-recipe.yaml")

func TestRecipeParsingAndStoredRecipes(t *testing.T) {
	r, err := ParseRecipe(defaultRecipe)
	if err != nil || r.Kind != "electron" || r.Toolchain.Node != "24" || r.Install.CacheKey[0] != "frontend/package-lock.json" || r.Isolation.Home == nil || r.privateHome() ||
		!strings.Contains(strings.Join(r.Run.Args, " "), "$LITMUS_DEBUG_PORT") || len(r.Seed) != 2 || !strings.Contains(r.Seed[1].Cmd, "--harness claude-code --mode chat") || strings.Contains(strings.ToLower(string(defaultRecipe)), "fake") || !strings.Contains(r.Seed[1].WaitFor, "session ls") || r.Ready.PageTargets != 1 {
		t.Fatalf("default recipe %+v err %v", r, err)
	}
	if r.Run.Cmd != "./node_modules/.bin/electron-forge" || len(r.Run.Args) < 3 || r.Run.Args[0] != "start" || r.Run.Args[1] != "--" {
		t.Fatalf("default recipe must launch Electron Forge directly without npm's predev hook: %+v", r.Run)
	}
	for src, want := range map[string]string{
		"": "empty", "kind: cli\nbogus: 1\nrun: {cmd: x}": "bogus", "kind: gui\nrun: {cmd: x}": "kind", "kind: cli": "run.cmd",
		"kind: cli\nrun: {cmd: x, dir: ../up}": "relative", "kind: cli\nrun: {cmd: x}\ntoolchain: {node: '2*'}": "toolchain.node", "kind: cli\nrun: {cmd: x}\ninstall: {cmd: npm ci}": "cacheKey",
	} {
		if _, err := ParseRecipe([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", src, err, want)
		}
	}

	st := NewStore(t.TempDir())
	is(t, "")(st.LoadRecipe(DefaultRepo)) // embedded default
	is(t, "TESTING_RECIPE_MISSING")(st.LoadRecipe("o/r"))
	is(t, "")(nil, os.MkdirAll(filepath.Dir(st.recipePath("o/r")), 0o750))
	is(t, "")(nil, os.WriteFile(st.recipePath("o/r"), []byte("kind: cli\nrun: {cmd: ./go}\n"), 0o600))
	if got, err := st.LoadRecipe("o/r"); err != nil || got.Run.Cmd != "./go" {
		t.Fatalf("stored recipe %+v err %v", got, err)
	}
	is(t, "")(nil, os.WriteFile(st.recipePath("o/r"), []byte("kind: cli\n"), 0o600))
	is(t, "TESTING_RECIPE_INVALID")(st.LoadRecipe("o/r"))

	for url, want := range map[string]bool{
		"https://github.com/OrchestratorInc/agent-orchestrator.git": true, "git@github.com:orchestratorinc/agent-orchestrator.git": true,
		"https://gitlab.com/orchestratorinc/agent-orchestrator": false, "https://github.com/orchestratorinc/other": false, "/Users/me/checkout": false, "": false,
	} {
		if MatchRepo(url, "orchestratorinc", "agent-orchestrator") != want {
			t.Errorf("MatchRepo(%q) != %v", url, want)
		}
	}
}

func TestRecipeValidateRequest(t *testing.T) {
	empty, blank, path := "", " ", "/a@b/recipe@abcdef0"
	for _, tc := range []struct {
		req  RecipeValidateRequest
		code string
	}{
		{RecipeValidateRequest{}, ""},
		{RecipeValidateRequest{SHA: "abcdef0"}, ""},
		{RecipeValidateRequest{Path: &path, SHA: strings.Repeat("e", 40)}, ""},
		{RecipeValidateRequest{Path: &empty}, "TESTING_RECIPE_INVALID"},
		{RecipeValidateRequest{Path: &blank}, "TESTING_RECIPE_INVALID"},
		{RecipeValidateRequest{SHA: "main"}, "TESTING_RECIPE_INVALID"},
		{RecipeValidateRequest{SHA: "abcdef"}, "TESTING_RECIPE_INVALID"},
		{RecipeValidateRequest{SHA: strings.Repeat("e", 41)}, "TESTING_RECIPE_INVALID"},
	} {
		is(t, tc.code)(nil, tc.req.Validate())
	}
}

func TestRecipeValidateSelectsProjectStoredRecipeOrFile(t *testing.T) {
	st := NewStore(t.TempDir())
	svc := New(st, nil).WithProjectRepo(func(_ context.Context, project string) string {
		if project == "other-project" {
			return "other/app"
		}
		if project == "default-project" {
			return DefaultRepo
		}
		return ""
	})
	for _, project := range []string{"", "unknown", "other-project", "default-project"} {
		_, err := svc.RecipeValidate(ctx, RecipeValidateRequest{ProjectID: project})
		if !isCode(err, "TESTING_RECIPE_MISSING") || !strings.Contains(err.Error(), "no recipe") {
			t.Fatalf("project %q silently used another repository's recipe: %v", project, err)
		}
	}
	writeFile(t, st.recipePath("other/app"), "kind: cli\nrun: {cmd: ./stored}\n")
	r, err := svc.RecipeValidate(ctx, RecipeValidateRequest{ProjectID: "other-project"})
	if err != nil || r.Run.Cmd != "./stored" {
		t.Fatalf("stored recipe = %+v, error = %v", r, err)
	}
	// A SHA-looking suffix belongs to the filename; only SHA can request a launch.
	path := filepath.Join(t.TempDir(), "recipe@abcdef0")
	writeFile(t, path, "kind: cli\nrun: {cmd: ./explicit}\n")
	r, err = svc.RecipeValidate(ctx, RecipeValidateRequest{Path: &path})
	if err != nil || r.Run.Cmd != "./explicit" {
		t.Fatalf("explicit recipe = %+v, error = %v", r, err)
	}
	_, err = svc.RecipeValidate(ctx, RecipeValidateRequest{Path: &path, SHA: "abcdef0"})
	is(t, "TESTING_RECIPE_INVALID")(nil, err)
}

func TestRecipeValidateStoredReadErrorPreservesPath(t *testing.T) {
	st := NewStore(t.TempDir())
	path := st.recipePath(DefaultRepo)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	_, err := New(st, nil).WithProjectRepo(func(context.Context, string) string { return DefaultRepo }).RecipeValidate(ctx, RecipeValidateRequest{ProjectID: "project"})
	var ae *apierr.Error
	var pe *os.PathError
	if !errors.As(err, &ae) || ae.Kind != apierr.KindInternal || ae.Code != "TESTING_RECIPE_UNREADABLE" || !strings.Contains(ae.Message, path) || !errors.As(err, &pe) || pe.Path != path {
		t.Fatalf("stored recipe read lost its API classification or cause: API=%#v path=%#v error=%v", ae, pe, err)
	}
}

func TestRecipeValidateExplicitReadErrorsAreInvalid(t *testing.T) {
	for _, failure := range []string{"missing", "directory"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recipe.yaml")
			if failure == "directory" {
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			_, err := New(NewStore(t.TempDir()), nil).RecipeValidate(ctx, RecipeValidateRequest{Path: &path})
			var ae *apierr.Error
			var pe *os.PathError
			if !errors.As(err, &ae) || ae.Kind != apierr.KindInvalid || ae.Code != "TESTING_RECIPE_UNREADABLE" || !strings.Contains(ae.Message, path) || !errors.As(err, &pe) || pe.Path != path {
				t.Fatalf("explicit recipe read lost its client classification or cause: API=%#v path=%#v error=%v", ae, pe, err)
			}
		})
	}
}

func TestRecipeContextUsesSelectedSourceAndPreservesNotes(t *testing.T) {
	for _, tc := range []struct{ name, repo, source, notes string }{
		{"embedded", DefaultRepo, string(defaultRecipe), ""},
		{"stored overrides embedded", DefaultRepo, "# Preserve source comments.\nkind: electron\nrun: {cmd: ./test-app}\nnotes: |\n  Use a test fixture.\n  Keep $LITMUS_ROOT literal here.\n", "Use a test fixture.\nKeep $LITMUS_ROOT literal here.\n"},
		{"stored other app", "acme/widget", "kind: web\nrun: {cmd: ./web}\nnotes: |-\n  Line one.\n  Line two.", "Line one.\nLine two."},
		{"no notes", "acme/widget", "kind: electron\nrun: {cmd: ./app}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(t.TempDir())
			if tc.name != "embedded" {
				writeFile(t, store.recipePath(tc.repo), tc.source)
			}
			text, err := store.recipeText(tc.repo)
			if err != nil || text != tc.source {
				t.Fatalf("resolved source differs: error %v", err)
			}
			r, err := store.LoadRecipe(tc.repo)
			is(t, "")(nil, err)
			if tc.name != "embedded" && r.Notes != tc.notes {
				t.Fatalf("notes = %q, want %q", r.Notes, tc.notes)
			}
			run := Run{ID: "run_recipe", Kind: "pr", Repo: tc.repo, PRNumber: 1, HeadSHA: head6394, BaseSHA: base6394}
			is(t, "")(nil, store.Save(run))
			is(t, "")(nil, store.writeRecipeContext(run.ID, tc.repo))
			is(t, "")(nil, store.writeContextPack(run))
			svc := New(store, nil)
			if readRunFile(t, svc, run, "recipe.yaml") != tc.source || readRunFile(t, svc, run, "recipe-notes.md") != r.Notes {
				t.Fatal("run files differ from the recipe used by LoadRecipe")
			}
			assertContextFilesNamed(t, svc, run, []string{"recipe.yaml", "recipe-notes.md", "target.json", "run.json", "context-pack.md"})
		})
	}
}

func TestRecipeContextNamesMissingSetupAndReturnsReadOrParseErrors(t *testing.T) {
	store := NewStore(t.TempDir())
	run := Run{ID: "run_recipe", Kind: "pr", Repo: "acme/widget", PRNumber: 1, HeadSHA: head6394, BaseSHA: base6394}
	is(t, "")(nil, store.Save(run))
	is(t, "")(nil, store.writeRecipeContext(run.ID, run.Repo))
	is(t, "")(nil, store.writeContextPack(run))
	pack := readRunFile(t, New(store, nil), run, "context-pack.md")
	if !strings.Contains(pack, "Recipe: unavailable") || strings.Contains(pack, "- recipe.yaml:") || strings.Contains(pack, "- recipe-notes.md:") {
		t.Fatalf("missing recipe is not explicit in the pack: %s", pack)
	}
	path := store.recipePath(run.Repo)
	is(t, "")(nil, os.MkdirAll(path, 0o750))
	if err := store.writeRecipeContext(run.ID, run.Repo); !isCode(err, "TESTING_RECIPE_UNREADABLE") {
		t.Fatalf("recipe context hid read error: %v", err)
	}
	is(t, "")(nil, os.Remove(path))
	writeFile(t, path, "kind: electron\n")
	if err := store.writeRecipeContext(run.ID, run.Repo); !isCode(err, "TESTING_RECIPE_INVALID") {
		t.Fatalf("recipe context hid parse/validation error: %v", err)
	}
}

func TestIsolationEnvAcceptsApplicationSettings(t *testing.T) {
	r, err := ParseRecipe([]byte("kind: cli\nrun: {cmd: fixture}\nisolation:\n  env:\n    INSTANCE: '$LITMUS_RUN_ID'\n    PARENT: '$LITMUS_LEG_DIR/..'\n    MODE: shared\n"))
	is(t, "")(nil, err)
	if r.Isolation.Env["INSTANCE"] != "$LITMUS_RUN_ID" || r.Isolation.Env["PARENT"] != "$LITMUS_LEG_DIR/.." || r.Isolation.Env["MODE"] != "shared" {
		t.Fatalf("application settings changed: %v", r.Isolation.Env)
	}
}

func TestAORecipeContainsAppSpecificGuidance(t *testing.T) {
	recipe, err := ParseRecipe(defaultRecipe)
	is(t, "")(nil, err)
	for _, want := range []string{"project and session lists", "Sample project", "real PR fixture", "use any working model, cheapest first; if refused try another", "Agents in a leg use the owner's real logins; keep agent turns short.", "On account or login screens, never click Login, Logout, Sign in, Sign out, Use, Switch, Remove, Renew or Refresh login.", "Any change that touches UI: check it in both themes.", "Settings > General > Appearance", "the receipt of the switch and one after-shot per theme are the proof."} {
		if !strings.Contains(recipe.Notes, want) {
			t.Errorf("AO recipe notes omit %q", want)
		}
	}
	for _, old := range []string{"Device", "Active", "logged out", "isolation failed", "would touch the owner's login"} {
		if strings.Contains(recipe.Notes, old) {
			t.Errorf("recipe retains shared-HOME guidance %q", old)
		}
	}
}
