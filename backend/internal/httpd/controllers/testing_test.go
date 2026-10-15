package controllers_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
)

func testingServer(t *testing.T, deps httpd.APIDeps) func(method, path, body string, status int, want ...string) {
	t.Helper()
	if svc, ok := deps.Testing.(*litmus.Service); ok {
		deps.Testing = svc.WithSpawnMarkerWait(time.Millisecond)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return func(method, path, body string, status int, want ...string) {
		t.Helper()
		got, code, _ := doRequest(t, srv, method, "/api/v1/testing"+path, body)
		if code != status {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, code, status, got)
		}
		for _, w := range want {
			if !strings.Contains(string(got), w) {
				t.Fatalf("%s %s body %s lacks %q", method, path, got, w)
			}
		}
	}
}

func savedTestPNG(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTestingAPIStatusAndSubmit(t *testing.T) {
	store := litmus.NewStore(t.TempDir())
	do := testingServer(t, httpd.APIDeps{Testing: litmus.New(store, nil).WithGH(recordedGH{t: t})})
	do("GET", "/status?sessionId=ao-1", "", http.StatusNotFound, "TESTING_RUN_NOT_FOUND")
	do("POST", "/intake", `{"sessionId":"ao-1","prUrl":"https://github.com/aoagents/agent-orchestrator/pull/6394"}`, http.StatusOK, `"state":"intake"`, `"prNumber":6394`, `"comment":"off"`)
	do("GET", "/status?sessionId=ao-1", "", http.StatusOK, `"checks":[]`)
	do("POST", "/checks", `{"sessionId":"ao-1","name":"model menu","expect":"last model reachable"}`, http.StatusOK, `"id":"c1"`)
	do("POST", "/checks/c1/start", `{"sessionId":"ao-1"}`, http.StatusOK, `"state":"running"`)
	do("POST", "/checks/c1/end", `{"sessionId":"ao-1","result":"maybe"}`, http.StatusBadRequest, "TESTING_CHECK_RESULT_INVALID")
	do("POST", "/checks/c1/end", `{"sessionId":"ao-1","result":"pass"}`, http.StatusConflict, "TESTING_CHECK_NO_RECEIPT") // nothing was driven
	do("POST", "/submit", `{"sessionId":"ao-1"}`, http.StatusBadRequest, "TESTING_CHECKS_INCOMPLETE", "c1 model menu")
	checks, err := store.Checks("run_ao-1") // receipts normally arrive from the drive stage
	if err != nil {
		t.Fatal(err)
	}
	shot := savedTestPNG(t)
	checks[0].Receipts = []litmus.Receipt{{Seq: 1, Action: "click", AfterPath: shot}}
	if err := store.SaveChecks("run_ao-1", checks); err != nil {
		t.Fatal(err)
	}
	do("POST", "/checks/c1/end", `{"sessionId":"ao-1","result":"pass"}`, http.StatusOK, `"state":"pass"`)
	do("POST", "/drive", `{"sessionId":"ao-1","action":"click","args":["e12"]}`, http.StatusConflict, "TESTING_RUN_NOT_RUNNING")
	do("POST", "/submit", `{"sessionId":"ao-1"}`, http.StatusOK, `"overall":"partial"`, "base-vs-head difference was not shown")
	do("POST", "/drive", `{"sessionId":"ao-1","action":"click","args":["e12"]}`, http.StatusConflict, "TESTING_RUN_CLOSED")
	do("POST", "/intake", `{"sessionId":"ao-1","unknown":true}`, http.StatusBadRequest, "INVALID_JSON")
}

func TestTestingAPISubmitBlocked(t *testing.T) {
	do := testingServer(t, httpd.APIDeps{Testing: litmus.New(litmus.NewStore(t.TempDir()), nil).WithGH(recordedGH{t: t})})
	do("POST", "/intake", `{"sessionId":"ao-1","prUrl":"https://github.com/aoagents/agent-orchestrator/pull/6394"}`, http.StatusOK)
	do("POST", "/submit", `{"sessionId":"ao-1","blocked":true,"reason":"  "}`, http.StatusBadRequest, "TESTING_REASON_REQUIRED")
	do("POST", "/submit", `{"sessionId":"ao-1","blocked":true,"reason":"prepare refused"}`, http.StatusOK, `"overall":"blocked"`, `"reasons":["prepare refused"]`)
	do("POST", "/submit", `{"sessionId":"ao-1","blocked":true,"reason":"again"}`, http.StatusConflict, "TESTING_RUN_CLOSED")
}

func TestTestingAPICheckLegAndCompareRoute(t *testing.T) {
	do := testingServer(t, httpd.APIDeps{Testing: litmus.New(litmus.NewStore(t.TempDir()), nil).WithGH(recordedGH{t: t})})
	do("POST", "/intake", `{"sessionId":"ao-1","prUrl":"https://github.com/aoagents/agent-orchestrator/pull/6394"}`, http.StatusOK)
	do("POST", "/checks", `{"sessionId":"ao-1","name":"menu","expect":"opens","leg":"base"}`, http.StatusOK, `"id":"c1"`, `"leg":"base"`, `"name":"menu"`)
	do("POST", "/checks", `{"sessionId":"ao-1","name":"menu","expect":"opens","leg":"main"}`, http.StatusBadRequest, "TESTING_LEG_INVALID")
	do("POST", "/checks/c9/compare", `{"sessionId":"ao-1"}`, http.StatusNotFound, "TESTING_CHECK_NOT_FOUND")
	do("POST", "/checks/c1/compare", `{"sessionId":"ao-1"}`, http.StatusConflict, "TESTING_COMPARE_INCOMPLETE") // no after-shots
}

func TestTestingAPIReplayValidationAndTesterAccess(t *testing.T) {
	store := litmus.NewStore(t.TempDir())
	run := litmus.Run{ID: "run_ao-1", SessionID: "ao-1", State: litmus.RunRunning, Deadline: time.Now().Add(time.Minute)}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Testing: litmus.New(store, nil)}, httpd.ControlDeps{})
	for _, tc := range []struct {
		leg  string
		code int
		want string
	}{
		{"head", http.StatusBadRequest, "TESTING_REPLAY_LEG"},
		{"base", http.StatusNotFound, "TESTING_CHECK_NOT_FOUND"},
	} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/testing/checks/c1/replay", strings.NewReader(`{"sessionId":"ao-1","leg":"`+tc.leg+`"}`)))
		if res.Code != tc.code || !strings.Contains(res.Body.String(), tc.want) {
			t.Fatalf("leg %s: %d %s", tc.leg, res.Code, res.Body.String())
		}
	}
}

func TestTestingAPICheckEndSameFaultAndDuplicate(t *testing.T) {
	store := litmus.NewStore(t.TempDir())
	svc := litmus.New(store, nil)
	run := litmus.Run{ID: "run_ao-1", SessionID: "ao-1", State: litmus.RunRunning, Deadline: time.Now().Add(time.Minute)}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	shot := savedTestPNG(t)
	checks := []litmus.Check{
		{ID: "c1", Name: "menu", State: litmus.CheckRunning, Receipts: []litmus.Receipt{{Action: "click", AfterPath: shot}}},
		{ID: "c2", Name: "menu", State: litmus.CheckRunning, Leg: "base", Receipts: []litmus.Receipt{{Action: "click", Leg: "base", AfterPath: shot}}},
	}
	if err := store.SaveChecks(run.ID, checks); err != nil {
		t.Fatal(err)
	}
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Testing: svc}, httpd.ControlDeps{})
	for _, row := range []struct {
		path, body, want string
		code             int
	}{
		{"/checks/c1/end", `{"sessionId":"ao-1","result":"fail","sameFault":true}`, "TESTING_CHECK_SAME_FAULT", http.StatusBadRequest},
		{"/checks/c2/end", `{"sessionId":"ao-1","result":"fail","sameFault":true}`, `"sameFault":true`, http.StatusOK},
		{"/checks", `{"sessionId":"ao-1","name":"MENU","expect":"shown","leg":"head"}`, "TESTING_CHECK_DUPLICATE", http.StatusConflict},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/testing"+row.path, strings.NewReader(row.body)))
		if w.Code != row.code || !strings.Contains(w.Body.String(), row.want) {
			t.Fatalf("%s = %d %s", row.path, w.Code, w.Body.String())
		}
	}
}

func TestTestingAPICheckEndShot(t *testing.T) {
	for _, tc := range []struct {
		name string
		shot int
		leg  string
		code int
		want string
	}{
		{"unknown", 99, "head", http.StatusBadRequest, "TESTING_ARGS_INVALID"},
		{"base", 11, "base", http.StatusBadRequest, "TESTING_ARGS_INVALID"},
		{"valid", 11, "head", http.StatusOK, `"shot":11`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := litmus.NewStore(t.TempDir())
			run := litmus.Run{ID: "run_ao-1", SessionID: "ao-1", State: litmus.RunRunning, Deadline: time.Now().Add(time.Minute)}
			if err := store.Save(run); err != nil {
				t.Fatal(err)
			}
			shot := savedTestPNG(t)
			checks := []litmus.Check{
				{ID: "c2", Leg: tc.leg, State: litmus.CheckPass, Receipts: []litmus.Receipt{{Seq: 11, Leg: tc.leg, AfterPath: shot}}},
				{ID: "c9", State: litmus.CheckRunning, Receipts: []litmus.Receipt{{Seq: 42, AfterPath: shot}}},
			}
			if err := store.SaveChecks(run.ID, checks); err != nil {
				t.Fatal(err)
			}
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
				httpd.APIDeps{Testing: litmus.New(store, nil)}, httpd.ControlDeps{})
			w := httptest.NewRecorder()
			body := fmt.Sprintf(`{"sessionId":"ao-1","result":"fail","shot":%d}`, tc.shot)
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/testing/checks/c9/end", strings.NewReader(body)))
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("shot=%d leg=%s: %d %s", tc.shot, tc.leg, w.Code, w.Body.String())
			}
		})
	}
}

func TestTestingAPITestVerbRemoved(t *testing.T) {
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Testing: litmus.New(litmus.NewStore(t.TempDir()), nil)}, httpd.ControlDeps{})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/testing/test", strings.NewReader(`{}`)))
	if res.Code != http.StatusNotFound {
		t.Fatalf("removed test route = %d: %s", res.Code, res.Body.String())
	}
}

func TestTestingAPIStartSpawnsWorker(t *testing.T) {
	var spawned []litmus.SpawnRequest
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil).WithSpawner(func(_ context.Context, r litmus.SpawnRequest) (string, error) {
		spawned = append(spawned, r)
		return "ao-7", nil
	})
	do := testingServer(t, httpd.APIDeps{Testing: svc})
	do("POST", "/start", `{"projectId":"p1","prUrl":"aoagents/agent-orchestrator#6394"}`, http.StatusOK, `"sessionId":"ao-7"`, `"name":"litmus: PR #6394"`)
	do("POST", "/start", `{"prUrl":"aoagents/agent-orchestrator#6394","harness":"aider"}`, http.StatusBadRequest, "TESTING_HARNESS_UNSUPPORTED")
	do("POST", "/start", `{"prUrl":"nope"}`, http.StatusBadRequest, "TESTING_PR_URL_INVALID")
	do("POST", "/start", `{"projectId":"p2","prUrl":"aoagents/agent-orchestrator#6394","harness":"codex"}`, http.StatusOK, `"sessionId":"ao-7"`)
	if len(spawned) != 2 || spawned[0].ProjectID != "p1" || spawned[0].Harness != "claude-code" || spawned[1].Harness != "codex" {
		t.Fatalf("spawned = %+v", spawned)
	}
}

func TestTestingAPIStartNoCommentReachesTheRun(t *testing.T) {
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil).WithGH(recordedGH{t: t}).WithSpawner(func(context.Context, litmus.SpawnRequest) (string, error) { return "ao-7", nil })
	do := testingServer(t, httpd.APIDeps{Testing: svc})
	do("POST", "/start", `{"prUrl":"aoagents/agent-orchestrator#6394","noComment":true}`, http.StatusOK, `"sessionId":"ao-7"`)
	do("POST", "/intake", `{"sessionId":"ao-7","prUrl":"aoagents/agent-orchestrator#6394"}`, http.StatusOK, `"comment":"off"`)
	do("POST", "/submit", `{"sessionId":"ao-7","blocked":true,"reason":"x"}`, http.StatusOK, `"commentState":"off"`)
}

func TestTestingAPIStartNoEvidenceReachesTheRun(t *testing.T) {
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil).WithGH(recordedGH{t: t}).WithSpawner(func(context.Context, litmus.SpawnRequest) (string, error) { return "ao-7", nil })
	do := testingServer(t, httpd.APIDeps{Testing: svc})
	do("POST", "/start", `{"prUrl":"aoagents/agent-orchestrator#6394","noEvidence":true}`, http.StatusOK, `"sessionId":"ao-7"`)
	do("POST", "/intake", `{"sessionId":"ao-7","prUrl":"aoagents/agent-orchestrator#6394"}`, http.StatusOK, `"evidence":"off"`, `"comment":"post"`)
	do("POST", "/submit", `{"sessionId":"ao-7","blocked":true,"reason":"x"}`, http.StatusOK, `"evidenceState":"off"`)
}

func TestTestingAPIUnmountedWithoutService(t *testing.T) {
	testingServer(t, httpd.APIDeps{})("GET", "/status?sessionId=ao-1", "", http.StatusNotFound)
}

// recordedGH replays the gh output recorded for PR 6394 under litmus/testdata,
// so the controller test never reaches the network.
type recordedGH struct{ t *testing.T }

func (g recordedGH) Run(_ context.Context, args ...string) ([]byte, error) {
	g.t.Helper()
	var name string
	switch {
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		name = "pr6394.json"
	case len(args) >= 2 && args[0] == "pr" && args[1] == "diff":
		name = "pr6394.diff"
	default:
		return []byte("{}"), nil
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "litmus", "testdata", name))
	if err != nil {
		g.t.Fatalf("read recorded gh output: %v", err)
	}
	return b, nil
}

func TestTestingAPISubmitSkipBase(t *testing.T) {
	store := litmus.NewStore(t.TempDir())
	do := testingServer(t, httpd.APIDeps{Testing: litmus.New(store, nil).WithGH(recordedGH{t: t})})
	do("POST", "/intake", `{"sessionId":"ao-1","prUrl":"https://github.com/aoagents/agent-orchestrator/pull/6394"}`, http.StatusOK)
	do("POST", "/submit", `{"sessionId":"ao-1","skipBase":"  "}`, http.StatusBadRequest, "TESTING_REASON_REQUIRED")
	do("POST", "/submit", `{"sessionId":"ao-1","skipBase":"base build is broken"}`, http.StatusBadRequest, "TESTING_NO_CHECKS") // the field reaches the service
}

func TestTestingAPIPrune(t *testing.T) {
	dataDir := t.TempDir()
	checkout := filepath.Join(dataDir, "litmus", "repos", "o__r", strings.Repeat("a", 40))
	if err := os.MkdirAll(checkout, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "f"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour) // checkouts used in the last 10 minutes are kept
	if err := os.Chtimes(checkout, old, old); err != nil {
		t.Fatal(err)
	}
	do := testingServer(t, httpd.APIDeps{Testing: litmus.New(litmus.NewStore(dataDir), nil)})
	do("POST", "/prune", `{"dryRun":true}`, http.StatusOK, `"dryRun":true`, "would remove checkout repos/o__r/", "would prune: 1 checkouts, 0 runs")
	if _, err := os.Stat(checkout); err != nil {
		t.Fatalf("dry run deleted the checkout: %v", err)
	}
	do("POST", "/prune", `{"keepRuns":-1}`, http.StatusBadRequest, "TESTING_PRUNE_INVALID")
	do("POST", "/prune", `{}`, http.StatusOK, `"dryRun":false`, "pruned: 1 checkouts, 0 runs")
	if _, err := os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatalf("checkout survived: %v", err)
	}
}

// postingGH answers the comment endpoints a post needs and replays the recorded PR otherwise.
type postingGH struct {
	recordedGH
	posted *[]string
}

func (g postingGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	line := strings.Join(args, " ")
	switch {
	case line == "api user --jq .login":
		return []byte("litmus-bot\n"), nil
	case strings.HasSuffix(line, "/comments --paginate"):
		return []byte("[]"), nil
	case strings.HasPrefix(line, "api --method POST ") && strings.Contains(line, "/comments --input "):
		*g.posted = append(*g.posted, line)
		return []byte(`{"html_url":"https://github.com/o/r/pull/6394#issuecomment-1"}`), nil
	}
	return g.recordedGH.Run(ctx, args...)
}

func TestTestingAPIRunsAskBeforePostingAndRerun(t *testing.T) {
	const pr = "aoagents/agent-orchestrator#6394"
	store := litmus.NewStore(t.TempDir())
	var posted []string
	ids := []string{"ao-7", "ao-8", "ao-9"}
	svc := litmus.New(store, nil).WithGH(postingGH{recordedGH{t: t}, &posted}).WithSpawner(func(context.Context, litmus.SpawnRequest) (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	})
	do := testingServer(t, httpd.APIDeps{Testing: svc})

	do("GET", "/projects/p1/settings", "", http.StatusOK, `"askBeforePosting":false`)
	do("PUT", "/projects/p1/settings", `{"askBeforePosting":true}`, http.StatusOK, `"askBeforePosting":true`)
	do("PUT", "/projects/p1/settings", `{"ask":true}`, http.StatusBadRequest, "INVALID_JSON")
	do("GET", "/projects/p1/settings", "", http.StatusOK, `"askBeforePosting":true`)
	do("GET", "/projects/p2/settings", "", http.StatusOK, `"askBeforePosting":false`)

	finish := func(session string) {
		t.Helper()
		do("POST", "/start", `{"projectId":"p1","prUrl":"`+pr+`","noEvidence":true}`, http.StatusOK, `"sessionId":"`+session+`"`)
		do("POST", "/intake", `{"sessionId":"`+session+`","prUrl":"`+pr+`"}`, http.StatusOK, `"projectId":"p1"`)
		do("POST", "/checks", `{"sessionId":"`+session+`","name":"model menu","expect":"last model reachable"}`, http.StatusOK)
		do("POST", "/checks/c1/start", `{"sessionId":"`+session+`"}`, http.StatusOK)
		id := "run_" + session
		checks, err := store.Checks(id)
		if err != nil {
			t.Fatal(err)
		}
		checks[0].Receipts = []litmus.Receipt{{Seq: 1, Action: "click", AfterPath: savedTestPNG(t)}}
		if err := store.SaveChecks(id, checks); err != nil {
			t.Fatal(err)
		}
		do("POST", "/checks/c1/end", `{"sessionId":"`+session+`","result":"pass"}`, http.StatusOK)
		do("POST", "/submit", `{"sessionId":"`+session+`"}`, http.StatusOK, `"commentState":"awaiting-approval"`)
	}
	finish("ao-7")
	finish("ao-8")
	if len(posted) != 0 {
		t.Fatalf("submit posted %q although the project asks first", posted)
	}

	do("GET", "/runs?projectId=p1", "", http.StatusOK, `"id":"run_ao-8"`, `"id":"run_ao-7"`, `"state":"awaiting-approval"`, `"verdict":"partial"`, `"number":6394`, `"durationSeconds":`)
	do("GET", "/runs?projectId=p2", "", http.StatusOK, `"runs":[]`)
	do("GET", "/runs?limit=1", "", http.StatusOK, `"id":"run_ao-8"`)
	do("GET", "/runs?limit=0", "", http.StatusBadRequest, "TESTING_LIMIT_INVALID")
	do("GET", "/runs?limit=x", "", http.StatusBadRequest, "TESTING_LIMIT_INVALID")
	do("GET", "/runs/run_ao-7", "", http.StatusOK, `"verdictMd":"# Verdict: partial`, `"commentMd":"Litmus tried`, `"checks":[{`, `"run":{`)
	do("GET", "/runs/run_nope", "", http.StatusNotFound, "TESTING_RUN_NOT_FOUND")

	do("POST", "/runs/run_ao-7/post", `{}`, http.StatusOK, `"state":"submitted"`, `"commentState":"posted"`, `issuecomment-1`)
	if len(posted) != 1 {
		t.Fatalf("posted %q", posted)
	}
	do("POST", "/runs/run_ao-7/post", `{}`, http.StatusConflict, "TESTING_NOT_AWAITING") // a second post is refused
	do("POST", "/runs/run_ao-7/discard", `{}`, http.StatusConflict, "TESTING_NOT_AWAITING")
	do("POST", "/runs/run_nope/post", `{}`, http.StatusNotFound, "TESTING_RUN_NOT_FOUND")

	do("POST", "/runs/run_ao-8/discard", `{}`, http.StatusOK, `"state":"cleaned"`, `"commentState":"discarded"`)
	do("POST", "/runs/run_ao-8/post", `{}`, http.StatusConflict, "TESTING_NOT_AWAITING")
	if len(posted) != 1 {
		t.Fatalf("a discarded run posted: %q", posted)
	}

	do("POST", "/rerun", `{"runId":"run_ao-7"}`, http.StatusOK, `"sessionId":"ao-9"`, `"runId":"run_ao-9"`)
	do("POST", "/rerun", `{"runId":"run_nope"}`, http.StatusNotFound, "TESTING_RUN_NOT_FOUND")
	do("POST", "/rerun", `{}`, http.StatusBadRequest, "TESTING_ID_INVALID")
}

func TestTestingAPIProjectSettingsReviewPreservesOmittedValue(t *testing.T) {
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil)
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Testing: svc}, httpd.ControlDeps{})
	put := func(body string, want ...string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/testing/projects/p1/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT settings = %d, want %d: %s", rr.Code, http.StatusOK, rr.Body.String())
		}
		for _, text := range want {
			if !strings.Contains(rr.Body.String(), text) {
				t.Fatalf("PUT settings body %s lacks %q", rr.Body.String(), text)
			}
		}
	}
	put(`{"askBeforePosting":false,"review":true}`, `"review":true`)
	put(`{"askBeforePosting":true}`, `"askBeforePosting":true`, `"review":true`)
	put(`{"askBeforePosting":true,"review":false}`, `"review":false`)
}

// The daemon, not the prompt, keeps a tester out of start, rerun, post and prune.
func TestTestingAPIRefusesTheTesterOnUserVerbs(t *testing.T) {
	svc := litmus.New(litmus.NewStore(t.TempDir()), nil).WithGH(recordedGH{t: t}).WithSpawner(func(context.Context, litmus.SpawnRequest) (string, error) { return "ao-9", nil })
	do := testingServer(t, httpd.APIDeps{Testing: svc})
	do("POST", "/intake", `{"sessionId":"ao-1","prUrl":"https://github.com/aoagents/agent-orchestrator/pull/6394"}`, http.StatusOK)

	do("POST", "/prune", `{"sessionId":"ao-1","dryRun":true}`, http.StatusForbidden, "TESTING_TESTER_FORBIDDEN")
	do("POST", "/start", `{"sessionId":"ao-1","projectId":"p1","prUrl":"aoagents/agent-orchestrator#6394"}`, http.StatusForbidden, "TESTING_TESTER_FORBIDDEN")
	do("POST", "/runs/run_ao-1/post", `{"sessionId":"ao-1"}`, http.StatusForbidden, "TESTING_TESTER_FORBIDDEN")
	do("POST", "/rerun", `{"sessionId":"ao-1","runId":"run_ao-1"}`, http.StatusForbidden, "TESTING_TESTER_FORBIDDEN", "rerun")

	// The user's own session, or no session, is allowed through.
	do("POST", "/prune", `{"sessionId":"ao-orch","dryRun":true}`, http.StatusOK, `"dryRun":true`)
	do("POST", "/prune", `{"dryRun":true}`, http.StatusOK, `"dryRun":true`)
	do("POST", "/start", `{"sessionId":"ao-orch","projectId":"p1","prUrl":"aoagents/agent-orchestrator#6394"}`, http.StatusOK, `"sessionId":"ao-9"`)
	do("POST", "/rerun", `{"sessionId":"ao-orch","runId":"run_ao-1","projectId":"p1"}`, http.StatusOK, `"sessionId":"ao-9"`, `"runId":"run_ao-9"`)
}
