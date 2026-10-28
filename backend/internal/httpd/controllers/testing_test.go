package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
)

var _ controllers.TestingService = (*litmus.Service)(nil)

type testingProbe struct {
	controllers.TestingService
	session, path, action, refused string
	args                           []string
	start                          litmus.StartRequest
	recipe                         litmus.RecipeValidateRequest
	settings                       litmus.ProjectSettings
	keepRuns, keepCheckouts        int
	err                            error
}

func (p *testingProbe) Submit(_ context.Context, session, path string) (litmus.Verdict, error) {
	p.session, p.path = session, path
	return litmus.Verdict{Result: "blocked", BlockedOn: "sample login"}, p.err
}
func (p *testingProbe) Start(_ context.Context, in litmus.StartRequest) (litmus.Started, error) {
	p.start = in
	return litmus.Started{SessionID: "w1", Name: "Test PR"}, p.err
}
func (p *testingProbe) Status(_ context.Context, session string) (litmus.RunStatus, error) {
	p.session = session
	return litmus.RunStatus{Run: litmus.Run{ID: "run_w1"}}, p.err
}
func (p *testingProbe) Drive(_ context.Context, session, action string, args []string) (map[string]any, error) {
	p.session, p.action, p.args = session, action, args
	return map[string]any{"text": "receipt head:1"}, p.err
}
func (p *testingProbe) RefuseTester(session, verb string) error {
	p.session, p.refused = session, verb
	if session == "tester" {
		return apierr.Invalid("TESTING_TESTER_REFUSED", "tester may not "+verb, nil)
	}
	return nil
}
func (p *testingProbe) Prune(_ context.Context, runs, checkouts int) (litmus.PruneReport, error) {
	p.keepRuns, p.keepCheckouts = runs, checkouts
	return litmus.PruneReport{}, p.err
}
func (p *testingProbe) PruneDryRun(ctx context.Context, runs, checkouts int) (litmus.PruneReport, error) {
	p.action = "dry-run"
	return p.Prune(ctx, runs, checkouts)
}
func (p *testingProbe) Post(_ context.Context, id string) (litmus.Run, error) {
	p.path = id
	return litmus.Run{ID: id, CommentState: "posted"}, p.err
}
func (p *testingProbe) Discard(_ context.Context, id string) (litmus.Run, error) {
	p.path = id
	return litmus.Run{ID: id, CommentState: "discarded"}, p.err
}
func (p *testingProbe) RecipeValidate(_ context.Context, req litmus.RecipeValidateRequest) (litmus.Recipe, error) {
	p.recipe = req
	if req.Path != nil {
		p.path = *req.Path
	}
	return litmus.Recipe{Kind: "electron", Timings: []string{"prepare 1s"}}, p.err
}
func (p *testingProbe) ProjectSettings(_ context.Context, id string) (litmus.ProjectSettings, error) {
	p.path = id
	return p.settings, p.err
}
func (p *testingProbe) SetProjectSettings(_ context.Context, id string, in litmus.ProjectSettings) (litmus.ProjectSettings, error) {
	p.path, p.settings = id, in
	return in, p.err
}
func (p *testingProbe) Runs(_ context.Context, project string, limit int) (litmus.RunList, error) {
	p.path, p.keepRuns = project, limit
	return litmus.RunList{}, p.err
}

func testingRequest(t *testing.T, p controllers.TestingService, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.TestingController{Svc: p}).Register(r)
	req := httptest.NewRequest(method, "/testing"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	return out
}

func TestTestingAPISubmitForwardsVerdictPath(t *testing.T) {
	p := &testingProbe{}
	out := testingRequest(t, p, "POST", "/submit", `{"sessionId":" w1 ","verdictPath":"/run/scratch/verdict.json"}`)
	if out.Code != 200 || p.session != "w1" || p.path != "/run/scratch/verdict.json" || !strings.Contains(out.Body.String(), `"result":"blocked"`) {
		t.Fatalf("status=%d body=%s probe=%+v", out.Code, out.Body, p)
	}
	for _, body := range []string{`{"sessionId":"w1","skipBase":"later"}`, `{"blocked":true}`, `{"summary":"old"}`, `{"sameFault":true}`, `{"shot":1}`, `{"verdictPath":42}`} {
		p.path = ""
		out = testingRequest(t, p, "POST", "/submit", body)
		if out.Code != 400 || p.path != "" || !strings.Contains(out.Body.String(), "INVALID_JSON") {
			t.Fatalf("body %q: %d %s", body, out.Code, out.Body)
		}
	}
}

func TestTestingAPIErrorEnvelope(t *testing.T) {
	p := &testingProbe{err: apierr.Invalid("TESTING_VERDICT_INVALID", "leg head never ran", nil)}
	out := testingRequest(t, p, "POST", "/submit", `{"sessionId":"w1","verdictPath":"scratch/verdict.json"}`)
	if out.Code != 400 || !strings.Contains(out.Body.String(), "TESTING_VERDICT_INVALID") || !strings.Contains(out.Body.String(), "leg head never ran") {
		t.Fatalf("%d %s", out.Code, out.Body)
	}
}

func TestTestingAPIRemovedRoutes(t *testing.T) {
	for _, path := range []string{"/checks", "/checks/c1/start", "/checks/c1/end", "/checks/c1/compare", "/checks/c1/replay", "/rerun"} {
		out := testingRequest(t, &testingProbe{}, "POST", path, `{}`)
		if out.Code != http.StatusNotFound {
			t.Fatalf("POST %s = %d", path, out.Code)
		}
	}
}

func TestTestingAPIStartDriveAndStatus(t *testing.T) {
	p := &testingProbe{}
	out := testingRequest(t, p, "POST", "/start", `{"projectId":"p1","harness":"codex","prUrl":"o/r#12","noComment":true,"noEvidence":true}`)
	if out.Code != 200 || p.start.ProjectID != "p1" || p.start.Harness != "codex" || p.start.PRURL != "o/r#12" || !p.start.NoComment || !p.start.NoEvidence {
		t.Fatalf("start %d %+v", out.Code, p.start)
	}
	out = testingRequest(t, p, "POST", "/drive", `{"sessionId":"w1","action":" SCREENSHOT ","args":["shot.png","--leg","base"]}`)
	if out.Code != 200 || p.action != "screenshot" || p.session != "w1" || strings.Join(p.args, " ") != "shot.png --leg base" {
		t.Fatalf("drive %d %+v", out.Code, p)
	}
	out = testingRequest(t, p, "GET", "/status?sessionId=%20w1%20", "")
	if out.Code != 200 || p.session != "w1" || !strings.Contains(out.Body.String(), "run_w1") || strings.Contains(out.Body.String(), "checks") {
		t.Fatalf("status %d %s", out.Code, out.Body)
	}
}

func TestTestingAPIUserVerbsRefuseTester(t *testing.T) {
	for _, path := range []string{"/start", "/runs/run_w1/post", "/runs/run_w1/discard", "/recipe/validate", "/prune"} {
		out := testingRequest(t, &testingProbe{}, "POST", path, `{"sessionId":"tester"}`)
		if out.Code != 400 || !strings.Contains(out.Body.String(), "TESTING_TESTER_REFUSED") {
			t.Fatalf("POST %s = %d %s", path, out.Code, out.Body)
		}
	}
}

func TestTestingAPIRestoredUserVerbsRefuseRealTester(t *testing.T) {
	store := litmus.NewStore(t.TempDir())
	if err := store.Save(litmus.Run{ID: "run_tester", SessionID: "tester"}); err != nil {
		t.Fatal(err)
	}
	svc := litmus.New(store, nil)
	for _, path := range []string{"/runs/run_tester/discard", "/recipe/validate"} {
		out := testingRequest(t, svc, "POST", path, `{"sessionId":" tester "}`)
		if out.Code != 403 || !strings.Contains(out.Body.String(), "TESTING_TESTER_FORBIDDEN") {
			t.Fatalf("POST %s = %d %s", path, out.Code, out.Body)
		}
	}
}

func TestTestingAPISettingsRefusesRealTester(t *testing.T) {
	store := litmus.NewStore(t.TempDir())
	if err := store.Save(litmus.Run{ID: "run_tester", SessionID: "tester"}); err != nil {
		t.Fatal(err)
	}
	svc := litmus.New(store, nil)
	want := litmus.ProjectSettings{AskBeforePosting: true, Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
	if _, err := svc.SetProjectSettings(context.Background(), "p1", want); err != nil {
		t.Fatal(err)
	}
	out := testingRequest(t, svc, "PUT", "/projects/p1/settings", `{"sessionId":" tester ","askBeforePosting":false}`)
	if out.Code != http.StatusForbidden || !strings.Contains(out.Body.String(), "TESTING_TESTER_FORBIDDEN") || !strings.Contains(out.Body.String(), "ao testing settings") {
		t.Fatalf("tester settings: %d %s", out.Code, out.Body)
	}
	got, err := svc.ProjectSettings(context.Background(), "p1")
	if err != nil || got != want {
		t.Fatalf("tester changed settings: %+v error %v, want %+v", got, err, want)
	}
	out = testingRequest(t, svc, "PUT", "/projects/p1/settings", `{"askBeforePosting":false}`)
	if out.Code != http.StatusOK {
		t.Fatalf("user settings without a session: %d %s", out.Code, out.Body)
	}
	got, err = svc.ProjectSettings(context.Background(), "p1")
	if err != nil || got.AskBeforePosting {
		t.Fatalf("user settings were not saved: %+v error %v", got, err)
	}
}

func TestTestingAPISettingsReplacesUndecodableFile(t *testing.T) {
	for _, data := range []string{
		`{"askBeforePosting":true,"model":"old",}`,
		`{"askBeforePosting":true,"model":"old","effort":42}`,
	} {
		t.Run(data, func(t *testing.T) {
			dataDir := t.TempDir()
			store := litmus.NewStore(dataDir)
			if err := store.Save(litmus.Run{ID: "run_tester", SessionID: "tester"}); err != nil {
				t.Fatal(err)
			}
			svc := litmus.New(store, nil)
			path := filepath.Join(dataDir, "litmus", "projects", "p1.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, settingsErr := svc.ProjectSettings(context.Background(), "p1")
			var apiErr *apierr.Error
			if !errors.As(settingsErr, &apiErr) {
				t.Fatalf("fixture must return a typed decode error: %v", settingsErr)
			}
			for _, req := range []struct{ method, path, body string }{
				{"GET", "/projects/p1/settings", ""},
				{"POST", "/start", `{"projectId":"p1","prUrl":"acme/web#42"}`},
			} {
				out := testingRequest(t, svc, req.method, req.path, req.body)
				var body struct{ Code, Message string }
				if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if out.Code != http.StatusInternalServerError || body.Code != "TESTING_SETTINGS_UNREADABLE" || body.Message != apiErr.Message || !strings.Contains(body.Message, path) {
					t.Fatalf("%s %s: %d %s", req.method, req.path, out.Code, out.Body)
				}
			}
			out := testingRequest(t, svc, "PUT", "/projects/p1/settings", `{"sessionId":" tester ","askBeforePosting":false}`)
			if out.Code != http.StatusForbidden || !strings.Contains(out.Body.String(), "TESTING_TESTER_FORBIDDEN") {
				t.Fatalf("tester repair: %d %s", out.Code, out.Body)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != data {
				t.Fatalf("GET or refused PUT changed settings: %q, error %v", got, err)
			}
			out = testingRequest(t, svc, "PUT", "/projects/p1/settings", `{"askBeforePosting":false,"effort":"high"}`)
			if out.Code != http.StatusOK {
				t.Fatalf("user repair: %d %s", out.Code, out.Body)
			}
			want := litmus.ProjectSettings{Effort: "high"}
			if got, err := svc.ProjectSettings(context.Background(), "p1"); err != nil || got != want {
				t.Fatalf("repaired settings %+v, error %v, want %+v", got, err, want)
			}
		})
	}
}

func TestTestingAPISettingsKeepsFilesystemErrors(t *testing.T) {
	dataDir := t.TempDir()
	svc := litmus.New(litmus.NewStore(dataDir), nil)
	path := filepath.Join(dataDir, "litmus", "projects", "p1.json")
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT"} {
		out := testingRequest(t, svc, method, "/projects/p1/settings", `{"askBeforePosting":false}`)
		if out.Code != http.StatusInternalServerError || !strings.Contains(out.Body.String(), `"code":"INTERNAL_ERROR"`) {
			t.Fatalf("%s settings directory: %d %s", method, out.Code, out.Body)
		}
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		t.Fatalf("settings directory was replaced: %v, error %v", st, err)
	}
}

func TestTestingAPIPruneRunsAndSettings(t *testing.T) {
	p := &testingProbe{}
	out := testingRequest(t, p, "POST", "/prune", `{"keepRuns":0,"keepCheckouts":0,"dryRun":true}`)
	if out.Code != 200 || p.keepRuns != 0 || p.keepCheckouts != 0 || p.action != "dry-run" {
		t.Fatalf("prune %d %+v", out.Code, p)
	}
	out = testingRequest(t, p, "GET", "/runs?projectId=p1&limit=7", "")
	if out.Code != 200 || p.path != "p1" || p.keepRuns != 7 {
		t.Fatalf("runs %d %+v", out.Code, p)
	}
	out = testingRequest(t, p, "GET", "/runs?limit=0", "")
	if out.Code != 400 || !strings.Contains(out.Body.String(), "TESTING_LIMIT_INVALID") {
		t.Fatalf("runs invalid %d %s", out.Code, out.Body)
	}
	out = testingRequest(t, p, "PUT", "/projects/p1/settings", `{"sessionId":" w1 ","askBeforePosting":true,"harness":"codex","model":"gpt-6.1-sol","effort":"xhigh"}`)
	if out.Code != 200 || p.path != "p1" || p.session != "w1" || p.refused != "settings" || !p.settings.AskBeforePosting || p.settings.Model != "gpt-6.1-sol" || p.settings.Effort != "xhigh" {
		t.Fatalf("settings %d %+v", out.Code, p)
	}
	out = testingRequest(t, p, "PUT", "/projects/p1/settings", `{"review":true}`)
	if out.Code != 400 {
		t.Fatalf("old review setting accepted: %d", out.Code)
	}
}

func (p *testingProbe) Intake(_ context.Context, session, target string) (litmus.Run, error) {
	p.session, p.path = session, target
	return litmus.Run{Kind: "issue", IssueNumber: 7}, p.err
}

func (p *testingProbe) Launch(_ context.Context, session, leg string) (litmus.Run, error) {
	p.session, p.path = session, leg
	return litmus.Run{Kind: "issue", IssueNumber: 7}, p.err
}

func TestTestingAPIIssueStartIntakeAndLaunch(t *testing.T) {
	p := &testingProbe{}
	out := testingRequest(t, p, "POST", "/start", `{"projectId":"p1","prUrl":"https://github.com/acme/widget/issues/7","candidateUrl":"https://github.com/acme/widget/pull/11"}`)
	if out.Code != 200 || p.start.PRURL != "https://github.com/acme/widget/issues/7" || p.start.CandidateURL != "https://github.com/acme/widget/pull/11" {
		t.Fatalf("start %d %+v", out.Code, p.start)
	}
	out = testingRequest(t, p, "POST", "/intake", `{"sessionId":"w1","prUrl":"https://github.com/acme/widget/issues/7"}`)
	if out.Code != 200 || p.session != "w1" || p.path != p.start.PRURL {
		t.Fatalf("intake %d %+v", out.Code, p)
	}
	out = testingRequest(t, p, "POST", "/launch", `{"sessionId":"w1","leg":"main"}`)
	if out.Code != 200 || p.path != "main" {
		t.Fatalf("launch %d %+v", out.Code, p)
	}
}

func TestTestingAPIDiscardAndRecipeValidate(t *testing.T) {
	for _, tc := range []struct{ route, body, path, verb, result string }{
		{"/runs/run_w1/discard", `{"sessionId":" user "}`, "run_w1", "discard", `"commentState":"discarded"`},
		{"/recipe/validate", `{"sessionId":" user ","path":"/tmp/recipe.yaml","sha":"abcdef0"}`, "/tmp/recipe.yaml", "recipe validate", `"timings":["prepare 1s"]`},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			p := &testingProbe{}
			out := testingRequest(t, p, "POST", tc.route, tc.body)
			if out.Code != 200 || p.session != "user" || p.path != tc.path || p.refused != tc.verb || !strings.Contains(out.Body.String(), tc.result) {
				t.Fatalf("status=%d body=%s probe=%+v", out.Code, out.Body, p)
			}
			if tc.verb == "recipe validate" && p.recipe.SHA != "abcdef0" {
				t.Fatalf("recipe validation lost SHA: %+v", p.recipe)
			}
			p = &testingProbe{err: apierr.Invalid("TESTING_SAMPLE_ERROR", "sample failure", nil)}
			out = testingRequest(t, p, "POST", tc.route, tc.body)
			if out.Code != 400 || !strings.Contains(out.Body.String(), "TESTING_SAMPLE_ERROR") {
				t.Fatalf("error envelope: %d %s", out.Code, out.Body)
			}
			p = &testingProbe{}
			out = testingRequest(t, p, "POST", tc.route, `{"path":42}`)
			if out.Code != 400 || p.path != "" || p.refused != "" || !strings.Contains(out.Body.String(), "INVALID_JSON") {
				t.Fatalf("invalid request reached service: %d %s probe=%+v", out.Code, out.Body, p)
			}
		})
	}
}

func TestTestingAPIRecipeValidateWithoutProjectHasNoDefault(t *testing.T) {
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil)
	// No SHA requests parsing only, so no target starts.
	for _, body := range []string{`{}`, `{"path":null}`} {
		out := testingRequest(t, svc, "POST", "/recipe/validate", body)
		if out.Code != 404 || !strings.Contains(out.Body.String(), "no recipe") {
			t.Fatalf("unexpected recipe fallback for %s: %d %s", body, out.Code, out.Body)
		}
	}
}

func TestTestingAPIRecipeValidateForwardsAbsentFileAndSHA(t *testing.T) {
	for _, body := range []string{`{"sha":"abcdef0","projectId":"project"}`, `{"path":null,"sha":"abcdef0","projectId":"project"}`} {
		p := &testingProbe{}
		out := testingRequest(t, p, "POST", "/recipe/validate", body)
		if out.Code != 200 || p.recipe.Path != nil || p.recipe.SHA != "abcdef0" || p.recipe.ProjectID != "project" || p.session != "" || p.refused != "recipe validate" {
			t.Fatalf("request %s: status=%d body=%s probe=%+v", body, out.Code, out.Body, p)
		}
	}
}

func TestTestingAPIRecipeValidateReadAndContentErrors(t *testing.T) {
	for _, source := range []string{"stored", "file"} {
		for _, failure := range []string{"read", "parse", "validate"} {
			t.Run(source+"/"+failure, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "litmus", "recipes", strings.ReplaceAll("other/app", "/", "__")+".yaml")
				body := `{"projectId":"project"}`
				if source == "file" {
					path = filepath.Join(dir, "recipe@abcdef0")
					data, err := json.Marshal(map[string]string{"path": path})
					if err != nil {
						t.Fatal(err)
					}
					body = string(data)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				wantStatus, wantCode := 400, "TESTING_RECIPE_INVALID"
				if failure == "read" {
					// A directory at the recipe path fails ReadFile even when tests run as root.
					if err := os.Mkdir(path, 0o750); err != nil {
						t.Fatal(err)
					}
					wantCode = "TESTING_RECIPE_UNREADABLE"
					if source == "stored" {
						wantStatus = 500
					}
				} else {
					content := "kind: cli\n"
					if failure == "parse" {
						content = "kind: [\n"
					}
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				svc := litmus.New(litmus.NewStore(dir), nil).WithProjectRepo(func(context.Context, string) string { return "other/app" })
				out := testingRequest(t, svc, "POST", "/recipe/validate", body)
				if out.Code != wantStatus || !strings.Contains(out.Body.String(), wantCode) || failure == "read" && !strings.Contains(out.Body.String(), path) {
					t.Fatalf("status=%d body=%s, want %d %s with recipe path on read failure", out.Code, out.Body, wantStatus, wantCode)
				}
			})
		}
	}
}

func TestTestingAPIRecipeValidateRejectsEmptyFileAndInvalidSHA(t *testing.T) {
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil)
	for _, body := range []string{`{"path":""}`, `{"path":" "}`, `{"sha":"main"}`} {
		out := testingRequest(t, svc, "POST", "/recipe/validate", body)
		if out.Code != 400 || !strings.Contains(out.Body.String(), "TESTING_RECIPE_INVALID") {
			t.Fatalf("request %s: %d %s", body, out.Code, out.Body)
		}
	}
}
