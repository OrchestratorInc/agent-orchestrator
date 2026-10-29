package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
)

type testingCapture struct {
	method, path string
	body         map[string]any
}

func testingDaemon(t *testing.T, status int, response string) *testingCapture {
	t.Helper()
	t.Setenv("AO_SESSION_ID", "w1")
	cfg := setConfigEnv(t)
	got := &testingCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = testingCapture{method: r.Method, path: r.URL.RequestURI()}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&got.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return got
}

func runTesting(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, append([]string{"testing"}, args...)...)
	return out, err
}

func TestTestingCommandList(t *testing.T) {
	cmd := newTestingCommand(nil)
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	want := []string{"start", "intake", "prepare", "launch", "browser", "screenshot", "record", "submit", "status", "cleanup", "settings", "post", "prune", "runs", "show", "discard", "recipe"}
	sort.Strings(names)
	sort.Strings(want)
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want %v", names, want)
	}
}

func TestTestingThemeIsUnknownCommand(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "testing", "theme", "dark")
	if err == nil || ExitCode(err) != 2 || err.Error() != `unknown command "theme" for "ao testing"` {
		t.Fatalf("err=%v, exit=%d", err, ExitCode(err))
	}
}

func TestTestingLaunchLineNamesIssueAndPRBuilds(t *testing.T) {
	for _, tc := range []struct{ kind, leg, build string }{
		{"pr", "head", "PR"}, {"pr", "base", "main"},
		{"issue", "main", "main"}, {"issue", "head", "candidate PR"},
	} {
		t.Run(tc.kind+"/"+tc.leg, func(t *testing.T) {
			gap := "The " + tc.build + " build started without its sample content (seed step 1 failed: no fixture)."
			run := litmus.Run{Kind: tc.kind, PRNumber: 7, IssueNumber: 37, Gaps: []string{gap, "The other build started without its sample content (seed step 1 failed: no fixture)."}}
			if got, want := launchLine(run, tc.leg), runLine(run)+"\n"+gap; got != want {
				t.Fatalf("launch line %q, want %q", got, want)
			}
		})
	}
}

func TestTestingVerbsSendOneRequestEach(t *testing.T) {
	path, err := filepath.Abs("scratch/verdict.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args         []string
		method, path string
		body         map[string]any
	}{
		{[]string{"intake", "o/r#12"}, "POST", "/testing/intake", map[string]any{"prUrl": "o/r#12"}},
		{[]string{"prepare"}, "POST", "/testing/prepare", map[string]any{}},
		{[]string{"launch", "head"}, "POST", "/testing/launch", map[string]any{"leg": "head", "relaunch": false}},
		{[]string{"launch", "base"}, "POST", "/testing/launch", map[string]any{"leg": "base", "relaunch": false}},
		{[]string{"launch", "head", "--relaunch"}, "POST", "/testing/launch", map[string]any{"leg": "head", "relaunch": true}},
		{[]string{"status"}, "GET", "/testing/status?sessionId=w1", nil},
		{[]string{"cleanup"}, "POST", "/testing/cleanup", map[string]any{}},
		{[]string{"submit", "--verdict", "scratch/verdict.json"}, "POST", "/testing/submit", map[string]any{"verdictPath": path}},
		{[]string{"screenshot", "--leg", "head", "shot.png"}, "POST", "/testing/drive", map[string]any{"action": "screenshot", "args": []any{"shot.png", "--leg", "head"}}},
		{[]string{"record", "start", "head.mp4"}, "POST", "/testing/drive", map[string]any{"action": "record", "args": []any{"start", "head.mp4"}}},
		{[]string{"record", "stop"}, "POST", "/testing/drive", map[string]any{"action": "record", "args": []any{"stop"}}},
		{[]string{"post", "run_w1"}, "POST", "/testing/runs/run_w1/post", map[string]any{}},
		{[]string{"discard", "run_w1"}, "POST", "/testing/runs/run_w1/discard", map[string]any{}},
		{[]string{"prune", "--keep-runs", "0", "--keep-checkouts", "0", "--dry-run"}, "POST", "/testing/prune", map[string]any{"keepRuns": float64(0), "keepCheckouts": float64(0), "dryRun": true}},
		{[]string{"runs", "--project", "p1", "--limit", "2"}, "GET", "/testing/runs?limit=2&projectId=p1", nil},
		{[]string{"show", "run_w1"}, "GET", "/testing/runs/run_w1", nil},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got := testingDaemon(t, http.StatusOK, `{}`)
			if _, err := runTesting(t, tc.args...); err != nil {
				t.Fatal(err)
			}
			if got.method != tc.method || got.path != "/api/v1"+tc.path {
				t.Fatalf("request = %+v", got)
			}
			if tc.body != nil {
				tc.body["sessionId"] = "w1"
				if !reflect.DeepEqual(got.body, tc.body) {
					t.Fatalf("body=%#v, want %#v", got.body, tc.body)
				}
			}
		})
	}
}

func TestTestingBrowserPreservesArgv(t *testing.T) {
	for _, leg := range []string{"", "head"} {
		t.Run(leg, func(t *testing.T) {
			got := testingDaemon(t, http.StatusOK, `{"text":"saved"}`)
			args := []string{"browser"}
			if leg != "" {
				args = append(args, "--leg", leg)
			}
			args = append(args, "--", "eval", "line one\nline two \"quoted\"", "--json")
			out, err := runTesting(t, args...)
			if err != nil || !strings.Contains(out, "saved") {
				t.Fatalf("out=%q err=%v", out, err)
			}
			want := []any{"eval", "line one\nline two \"quoted\"", "--json", "--leg", leg}
			if !reflect.DeepEqual(got.body["args"], want) {
				t.Fatalf("argv=%#v, want %#v", got.body["args"], want)
			}
		})
	}
}

func TestTestingUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"start"}, {"start", "not-a-url"}, {"submit"}, {"submit", "--verdict", " "},
		{"submit", "--blocked"}, {"submit", "--skip-base"}, {"submit", "--verdict-file", "old.txt"},
		{"browser"}, {"screenshot", "a", "b"},
		{"check", "add", "x"}, {"replay", "c1"}, {"compare", "c1"}, {"settings", "--review", "on"},
		{"discard"}, {"discard", "a", "b"}, {"recipe", "validate"}, {"recipe", "validate", "--sha", "bad"},
		{"recipe", "validate", "--sha", "abcdef0", "--recipe", " "}, {"recipe", "validate", "old.yaml", "--sha", "abcdef0"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, append([]string{"testing"}, args...)...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v, exit=%d", err, ExitCode(err))
			}
		})
	}
	t.Setenv("AO_SESSION_ID", "")
	if _, err := runTesting(t, "prepare"); err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "AO_SESSION_ID") {
		t.Fatalf("missing session: %v", err)
	}
}

func TestTestingRecipeValidateForwardsSHAAndOptionalRecipe(t *testing.T) {
	t.Setenv("AO_PROJECT_ID", "")
	for _, recipe := range []string{"", "recipe file.yaml", "recipe@abcdef0"} {
		t.Run(recipe, func(t *testing.T) {
			got := testingDaemon(t, http.StatusOK, `{"kind":"electron","timings":["prepare 1s","cleanup 0s"]}`)
			args := []string{"recipe", "validate", "--sha", "abcdef0"}
			path := ""
			if recipe != "" {
				args = append(args, "--recipe", recipe)
				var err error
				path, err = filepath.Abs(recipe)
				if err != nil {
					t.Fatal(err)
				}
			}
			out, err := runTesting(t, args...)
			want := map[string]any{"sessionId": "w1", "sha": "abcdef0"}
			if recipe != "" {
				want["path"] = path
			}
			if err != nil || got.method != "POST" || got.path != "/api/v1/testing/recipe/validate" || !reflect.DeepEqual(got.body, want) {
				t.Fatalf("error=%v request=%+v want=%v", err, got, want)
			}
			if !strings.Contains(out, "recipe ok: electron\nprepare 1s\ncleanup 0s") {
				t.Fatalf("missing validation timings: %q", out)
			}
		})
	}
}

func TestTestingRecipeValidateForwardsProjectSelection(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			got := testingDaemon(t, http.StatusOK, `{"kind":"electron"}`)
			t.Setenv("AO_PROJECT_ID", "inherited")
			args := []string{"recipe", "validate", "--sha", "abcdef0"}
			want := "inherited"
			if explicit {
				args = append(args, "--project", "selected")
				want = "selected"
			}
			_, err := runTesting(t, args...)
			if err != nil || got.body["projectId"] != want {
				t.Fatalf("project selection: error=%v body=%v", err, got.body)
			}
		})
	}
}

func TestTestingRestoredUserVerbsNeedNoSession(t *testing.T) {
	for _, args := range [][]string{{"discard", "run_w1"}, {"recipe", "validate", "--sha", "abcdef0"}} {
		got := testingDaemon(t, http.StatusOK, `{}`)
		t.Setenv("AO_SESSION_ID", "")
		if _, err := runTesting(t, args...); err != nil {
			t.Fatal(err)
		}
		if _, exists := got.body["sessionId"]; exists {
			t.Fatalf("unexpected session in user request: %+v", got)
		}
	}
}

func TestTestingSubmitDisplaysTesterVerdict(t *testing.T) {
	testingDaemon(t, http.StatusOK, `{"result":"blocked","headline":"No sample account","blocked_on":"sample credentials","commentState":"off"}`)
	out, err := runTesting(t, "submit", "--verdict", "scratch/verdict.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"verdict: blocked", "No sample account", "blocked on: sample credentials", "comment off"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
}

func TestTestingDaemonErrorPreservesEnvelope(t *testing.T) {
	testingDaemon(t, http.StatusConflict, `{"message":"leg head never ran","code":"TESTING_LEG_NOT_RUN","requestId":"req-litmus"}`)
	_, err := runTesting(t, "submit", "--verdict", "scratch/verdict.json")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("err=%v", err)
	}
	for _, want := range []string{"leg head never ran", "TESTING_LEG_NOT_RUN", "req-litmus"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
}

func TestTestingSettingsPreservesUnchangedFieldsAndForwardsCaller(t *testing.T) {
	for _, session := range []string{" w1 ", ""} {
		t.Run(session, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_PROJECT_ID", "p1")
			t.Setenv("AO_SESSION_ID", session)
			var methods []string
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/telemetry/cli-invoked" {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				methods = append(methods, r.Method)
				if r.URL.Path != "/api/v1/testing/projects/p1/settings" {
					t.Errorf("path=%s", r.URL.Path)
				}
				if r.Method == http.MethodPut {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("settings body: %v", err)
					}
					_ = json.NewEncoder(w).Encode(body)
					return
				}
				_, _ = io.WriteString(w, `{"askBeforePosting":true,"harness":"codex","model":"gpt-6.1-sol","effort":"xhigh"}`)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			if _, err := runTesting(t, "settings", "--ask-before-posting", "off"); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"askBeforePosting": false, "model": "gpt-6.1-sol", "harness": "codex", "effort": "xhigh"}
			if session != "" {
				want["sessionId"] = "w1"
			}
			if !reflect.DeepEqual(methods, []string{"GET", "PUT"}) || !reflect.DeepEqual(body, want) {
				t.Fatalf("methods=%v body=%v, want %v", methods, body, want)
			}
		})
	}
}

func TestTestingSettingsRepairsOnlyWithExplicitFlagsAndDecodeError(t *testing.T) {
	path := "/data/litmus/projects/p1.json"
	for _, tc := range []struct {
		name, code, message, response string
		status                        int
		flags                         []string
		want                          *litmus.ProjectSettings
	}{
		{name: "show corrupt file", status: 500, code: "TESTING_SETTINGS_UNREADABLE", message: fmt.Sprintf("settings file %s is unreadable (unexpected end of JSON input)", path)},
		{name: "repair syntax error", status: 500, code: "TESTING_SETTINGS_UNREADABLE", message: fmt.Sprintf("settings file %s is unreadable (unexpected end of JSON input)", path), flags: []string{"--ask-before-posting", "off"}, want: &litmus.ProjectSettings{}},
		{name: "repair type error", status: 500, code: "TESTING_SETTINGS_UNREADABLE", message: fmt.Sprintf("settings file %s is unreadable (json: cannot unmarshal number into Go struct field ProjectSettings.effort of type string)", path), flags: []string{"--effort", "xhigh"}, want: &litmus.ProjectSettings{Effort: "xhigh"}},
		{name: "explicit empty model", status: 500, code: "TESTING_SETTINGS_UNREADABLE", message: fmt.Sprintf("settings file %s is unreadable (unexpected end of JSON input)", path), flags: []string{"--model", ""}, want: &litmus.ProjectSettings{}},
		{name: "filesystem error", status: 500, code: "INTERNAL_ERROR", message: "Internal server error", flags: []string{"--ask-before-posting", "off"}},
		{name: "invalid project", status: 400, code: "TESTING_PROJECT_INVALID", message: "invalid project id", flags: []string{"--model", "opus"}},
		{name: "invalid response", status: 200, response: `{"askBeforePosting":true,"effort":42}`, flags: []string{"--effort", "xhigh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_PROJECT_ID", "p1")
			t.Setenv("AO_SESSION_ID", "")
			var methods []string
			var saved litmus.ProjectSettings
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/telemetry/cli-invoked" {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				methods = append(methods, r.Method)
				if r.URL.Path != "/api/v1/testing/projects/p1/settings" {
					t.Errorf("path=%s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPut {
					if err := json.NewDecoder(r.Body).Decode(&saved); err != nil {
						t.Errorf("settings body: %v", err)
					}
					_ = json.NewEncoder(w).Encode(saved)
					return
				}
				w.WriteHeader(tc.status)
				if tc.response != "" {
					_, _ = io.WriteString(w, tc.response)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"code": tc.code, "message": tc.message, "requestId": "req-settings"})
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			args := append([]string{"testing", "settings"}, tc.flags...)
			out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
			if tc.want == nil {
				if err == nil || ExitCode(err) != 1 || !reflect.DeepEqual(methods, []string{"GET"}) || strings.Contains(stderr, "rewriting with the given values") {
					t.Fatalf("nonrepairable settings: methods=%v stderr=%q error=%v", methods, stderr, err)
				}
				if tc.code != "" && (!strings.Contains(err.Error(), tc.code) || !strings.Contains(err.Error(), tc.message) || !strings.Contains(err.Error(), "req-settings")) {
					t.Fatalf("lost API error envelope: %v", err)
				}
				return
			}
			wantWarning := tc.message + "; rewriting with the given values\n"
			if err != nil || !reflect.DeepEqual(methods, []string{"GET", "PUT"}) || saved != *tc.want || stderr != wantWarning || !strings.Contains(out, "ask-before-posting off") {
				t.Fatalf("repair: methods=%v saved=%+v out=%q stderr=%q error=%v, want %+v and warning %q", methods, saved, out, stderr, err, *tc.want, wantWarning)
			}
		})
	}
}

func TestTestingSettingsHelpRefusesTesterChanges(t *testing.T) {
	out, _, err := executeCLI(t, Deps{}, "testing", "settings", "--help")
	if err != nil {
		t.Fatalf("settings help %q error %v", out, err)
	}
	for _, want := range []string{
		"Litmus testers cannot change settings",
		"Unspecified fields keep their stored values",
		"explicit setting flags replace it after a warning with the path and error",
		"unspecified fields then use their defaults",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings help lacks %q: %s", want, out)
		}
	}
}

func TestTestingShowUsesDaemonTexts(t *testing.T) {
	testingDaemon(t, http.StatusOK, `{"id":"run_w1","repo":"o/r","pr":{"number":12},"state":"awaiting-approval","verdict":"fail","verdictMd":"Tester verdict","commentMd":"Edited draft"}`)
	out, err := runTesting(t, "show", "run_w1")
	if err != nil || !strings.Contains(out, "Tester verdict") || !strings.Contains(out, "Edited draft") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestTestingStartSpawnsThroughDaemon(t *testing.T) {
	got := testingDaemon(t, http.StatusOK, `{"project":{"id":"p1"},"sessionId":"w2","name":"Test PR"}`)
	out, err := runTesting(t, "start", "--pr", "o/r#12", "--project", "p1", "--harness", "codex", "--no-comment", "--no-evidence")
	if err != nil || !strings.Contains(out, `spawned session w2 "Test PR"`) {
		t.Fatalf("out=%q err=%v", out, err)
	}
	want := map[string]any{"projectId": "p1", "prUrl": "o/r#12", "harness": "codex", "noComment": true, "noEvidence": true, "sessionId": "w1"}
	if got.method != "POST" || got.path != "/api/v1/testing/start" || !reflect.DeepEqual(got.body, want) {
		t.Fatalf("request=%+v want=%v", got, want)
	}
}

func TestTestingStartPreservesUnreadableSettingsEnvelope(t *testing.T) {
	message := "settings file /data/litmus/projects/p1.json is unreadable (unexpected end of JSON input)"
	body, err := json.Marshal(map[string]string{"code": "TESTING_SETTINGS_UNREADABLE", "message": message, "requestId": "req-settings"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := setConfigEnv(t)
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/telemetry/cli-invoked" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/projects/p1":
			_, _ = io.WriteString(w, `{"project":{"id":"p1"}}`)
		case "POST /api/v1/testing/start":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write(body)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	_, err = runTesting(t, "start", "acme/web#42", "--project", "p1")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "TESTING_SETTINGS_UNREADABLE") || !strings.Contains(err.Error(), message) || !strings.Contains(err.Error(), "req-settings") {
		t.Fatalf("lost settings error envelope: %v", err)
	}
	want := []string{"GET /api/v1/projects/p1", "POST /api/v1/testing/start"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%v, want %v", requests, want)
	}
}

func TestTestingStartAcceptsPositionalPRAndIssueWithCandidate(t *testing.T) {
	for _, tc := range []struct{ target, candidate string }{
		{"https://github.com/acme/widget/pull/11", ""},
		{"https://github.com/acme/widget/issues/7", ""},
		{"https://github.com/acme/widget/issues/7", "https://github.com/acme/widget/pull/11"},
	} {
		t.Run(tc.target+tc.candidate, func(t *testing.T) {
			got := testingDaemon(t, http.StatusOK, `{"project":{"id":"p1"},"sessionId":"w2","name":"Issue worker"}`)
			args := []string{"start", tc.target, "--project", "p1"}
			if tc.candidate != "" {
				args = append(args, "--candidate", tc.candidate)
			}
			if _, err := runTesting(t, args...); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"projectId": "p1", "prUrl": tc.target, "sessionId": "w1"}
			if tc.candidate != "" {
				want["candidateUrl"] = tc.candidate
			}
			if got.path != "/api/v1/testing/start" || !reflect.DeepEqual(got.body, want) {
				t.Fatalf("request %+v want %v", got, want)
			}
		})
	}
}

func TestTestingStartRefusesAmbiguousOrInvalidCandidateInput(t *testing.T) {
	for _, args := range [][]string{
		{"start", "https://github.com/acme/widget/issues/7", "--pr", "acme/widget#11"},
		{"start", "--pr", "https://github.com/acme/widget/issues/7"},
		{"start", "acme/widget#11", "--candidate", "acme/widget#12"},
		{"start", "https://github.com/acme/widget/issues/7", "--candidate", "other/repo#12"},
		{"start", "https://github.com/acme/widget/issues/7", "--candidate", ""},
	} {
		_, err := runTesting(t, args...)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args %v err %v", args, err)
		}
	}
}

func TestTestingIssueVerbsForwardMainAndDisplayIssueNumber(t *testing.T) {
	for _, tc := range []struct {
		args             []string
		path, key, value string
	}{
		{[]string{"intake", "https://github.com/acme/widget/issues/7"}, "/api/v1/testing/intake", "prUrl", "https://github.com/acme/widget/issues/7"},
		{[]string{"launch", "main"}, "/api/v1/testing/launch", "leg", "main"},
	} {
		got := testingDaemon(t, http.StatusOK, `{"id":"run_w1","kind":"issue","repo":"acme/widget","issueNumber":7}`)
		out, err := runTesting(t, tc.args...)
		if err != nil || got.path != tc.path || got.body[tc.key] != tc.value || !strings.Contains(out, "acme/widget#7") || strings.Contains(out, "#0") {
			t.Fatalf("out %q err %v request %+v", out, err, got)
		}
	}
}
