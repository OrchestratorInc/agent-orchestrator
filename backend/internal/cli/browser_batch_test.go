package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// batchBrowserServer records every browser command in order and answers each
// action with respond(action, args), defaulting to {"ok":true}.
type batchBrowserServer struct {
	mu       sync.Mutex
	requests []browserCommandRequestDTO
	respond  func(body browserCommandRequestDTO) (status int, result string)
}

func (s *batchBrowserServer) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, r.Action)
	}
	return out
}

func startBatchBrowserServer(t *testing.T, s *batchBrowserServer) Deps {
	t.Helper()
	setBrowserIdentity(t)
	// A developer's own remote browser must not receive desktop test steps.
	t.Setenv(envAgentBrowserURL, "")
	t.Setenv(envAgentBrowserToken, "")
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/browser/commands" {
			http.NotFound(w, r)
			return
		}
		var body browserCommandRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode command: %v", err)
		}
		s.mu.Lock()
		s.requests = append(s.requests, body)
		s.mu.Unlock()
		status, result := http.StatusOK, `{"ok":true}`
		if body.Action == "snapshot" {
			result = `{"text":"button Sign in [ref=e9]"}`
		}
		if body.Action == "get" {
			result = `{"value":"Order #42 total $19.99"}`
		}
		if s.respond != nil {
			if st, res := s.respond(body); res != "" {
				status, result = st, res
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = io.WriteString(w, result)
			return
		}
		_, _ = io.WriteString(w, `{"requestId":"r1","sessionId":"ao-1","action":"`+body.Action+`","result":`+result+`}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return Deps{ProcessAlive: func(int) bool { return true }}
}

const actMatchedSatisfied = `{"outcome":"matched","resolvedRef":"e3","candidate":{"role":"button","name":"Sign in"},"postcondition":{"kind":"text","status":"satisfied"}}`

func TestParseBrowserBatchLinesQuotesAndJSON(t *testing.T) {
	steps, err := parseBrowserBatch(`
# log in
open http://localhost:5173/login
act "Email address" --action fill --value 'ada@example.test'
ao browser act Sign\ in --expect-text "Welcome, \"Ada\""
`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"open", "http://localhost:5173/login"},
		{"act", "Email address", "--action", "fill", "--value", "ada@example.test"},
		{"act", "Sign in", "--expect-text", `Welcome, "Ada"`},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %#v", steps)
	}

	steps, err = parseBrowserBatch(`[["fill","e2","a b"], "click e3", ["get","text"]]`)
	if err != nil {
		t.Fatal(err)
	}
	want = [][]string{{"fill", "e2", "a b"}, {"click", "e3"}, {"get", "text"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("json steps = %#v", steps)
	}

	if steps, err := parseBrowserBatch(`[["act","Name","--action","fill","--value",""]]`); err != nil || steps[0][5] != "" {
		t.Fatalf("explicit empty value: steps=%#v err=%v", steps, err)
	}

	steps, err = parseBrowserBatch(`wait --text "C:\Users\d+" "a\b" "say \"hi\" \\ ok" 'x\y' plain\ word`)
	if err != nil {
		t.Fatal(err)
	}
	want = [][]string{{"wait", "--text", `C:\Users\d+`, `a\b`, `say "hi" \ ok`, `x\y`, "plain word"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("backslashes in quotes: %#v", steps)
	}

	for name, input := range map[string]string{
		"empty":         "  \n# only a comment\n",
		"unterminated":  `act "Sign in`,
		"bad json":      `[["click", 3]]`,
		"empty step":    `[[]]`,
		"null argument": `[["act","Name","--action","fill","--value",null]]`,
		"too many":      strings.Repeat("snapshot\n", maxBrowserBatchSteps+1),
	} {
		if _, err := parseBrowserBatch(input); err == nil {
			t.Errorf("%s: expected a parse error", name)
		}
	}
}

func TestBrowserBatchValidatesEveryStepBeforeRunningAny(t *testing.T) {
	for name, steps := range map[string]string{
		"unknown command":         "open http://x\nclik e3",
		"unknown flag":            "open http://x\nact Save --expect-txt Saved",
		"arg count":               "open http://x\nfill e2",
		"json on a step":          "open http://x\nsnapshot --json",
		"nested batch":            "open http://x\nbatch -",
		"chrome setup":            "open http://x\nchrome setup",
		"flag before batch":       "open http://x\n--json=false batch --steps 'click e1'",
		"flag before chrome":      "open http://x\n--json=false chrome setup",
		"batch flag before batch": "open http://x\n--verbose batch --steps 'click e1'",
		"json with value":         "open http://x\nsnapshot --json=true",
		"json after dash value":   "open http://x\nact Message --action fill --value -- --json",
		"help command":            "open http://x\nhelp click",
		"help flag":               "open http://x\nclick e1 --help",
		"act postconditions":      "click e1\nact Save --expect-text Saved --expect-url /done",
		"wait conditions":         "click e1\nwait --text a --url b",
		"snapshot full":           "click e1\nsnapshot --full",
		"screenshot base64":       "click e1\nscreenshot --base64",
		"network duration":        "click e1\nnetwork start --duration 0",
	} {
		t.Run(name, func(t *testing.T) {
			server := &batchBrowserServer{}
			deps := startBatchBrowserServer(t, server)
			_, _, err := executeCLI(t, deps, "browser", "batch", "--steps", steps)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "step 2") {
				t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
			}
			if got := server.actions(); len(got) != 0 {
				t.Fatalf("a step ran before validation finished: %v", got)
			}
		})
	}
}

func TestBrowserBatchRunsStepsInOrderWithCompactOutput(t *testing.T) {
	server := &batchBrowserServer{respond: func(body browserCommandRequestDTO) (int, string) {
		if body.Action == "act" && body.Args["action"] == "click" {
			return http.StatusOK, actMatchedSatisfied
		}
		return 0, ""
	}}
	deps := startBatchBrowserServer(t, server)
	out, errOut, err := executeCLI(t, deps, "browser", "batch", "--steps", strings.Join([]string{
		"open http://localhost:5173/login",
		`fill e2 "ada@example.test"`,
		`act "Sign in" --expect-text Welcome`,
		"get text",
	}, "\n"))
	if err != nil {
		t.Fatalf("err=%v stderr=%s out=%s", err, errOut, out)
	}
	if got, want := server.actions(), []string{"open", "fill", "act", "get"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	// Steps reach the daemon with the single command's arguments.
	if got := server.requests[2].Args["postcondition"].(map[string]any)["value"]; got != "Welcome" {
		t.Fatalf("act postcondition = %#v", server.requests[2].Args)
	}
	for _, want := range []string{
		"Batch completed: 4/4 steps ok",
		"[1] ok: open http://localhost:5173/login\n",
		"[2] ok: fill e2 <redacted>\n",
		`[3] ok: act "Sign in" --expect-text Welcome -> matched button [ref=e3]; postcondition text: satisfied`,
		"[4] ok: get text\n<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>\nOrder #42 total $19.99\n<<<END UNTRUSTED EXTERNAL CONTENT>>>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Mutating steps do not echo their own output, and a successful batch
	// takes no snapshot by default.
	if strings.Contains(out, "Browser fill completed") || strings.Contains(out, "Final snapshot") {
		t.Fatalf("output is not compact:\n%s", out)
	}
}

func TestBrowserBatchStopsAtFirstFailure(t *testing.T) {
	cases := map[string]struct {
		respond   func(browserCommandRequestDTO) (int, string)
		wantError string
	}{
		"unmet postcondition": {
			respond: func(b browserCommandRequestDTO) (int, string) {
				if b.Action == "act" {
					return http.StatusOK, `{"outcome":"matched","resolvedRef":"e3","candidate":{"role":"button","name":"Sign in"},"postcondition":{"kind":"text","status":"unmet","reason":"timeout"}}`
				}
				return 0, ""
			},
			wantError: "postcondition text unmet (timeout)",
		},
		"ambiguous act": {
			respond: func(b browserCommandRequestDTO) (int, string) {
				if b.Action == "act" {
					return http.StatusOK, `{"outcome":"ambiguous","candidates":[{"ref":"e1","role":"button","name":"Sign in"},{"ref":"e2","role":"link","name":"Sign in"}]}`
				}
				return 0, ""
			},
			wantError: "act was ambiguous (2 candidates)",
		},
		"no match": {
			respond: func(b browserCommandRequestDTO) (int, string) {
				if b.Action == "act" {
					return http.StatusOK, `{"outcome":"no-match","snapshot":"heading Login"}`
				}
				return 0, ""
			},
			wantError: "act matched no element",
		},
		"cancelled navigation": {
			respond: func(b browserCommandRequestDTO) (int, string) {
				if b.Action == "act" {
					return http.StatusOK, `{"outcome":"matched","resolvedRef":"e3","candidate":{"role":"link","name":"Leave"},"navigation":{"status":"cancelled","reason":"beforeunload"}}`
				}
				return 0, ""
			},
			wantError: "navigation cancelled (beforeunload)",
		},
		"daemon error": {
			respond: func(b browserCommandRequestDTO) (int, string) {
				if b.Action == "act" {
					return http.StatusConflict, `{"error":"conflict","code":"STALE_REF","message":"element reference e3 is stale"}`
				}
				return 0, ""
			},
			wantError: "stale",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := &batchBrowserServer{respond: tc.respond}
			deps := startBatchBrowserServer(t, server)
			out, _, err := executeCLI(t, deps, "browser", "batch", "--steps",
				"open http://localhost:5173/login\nact \"Sign in\" --expect-text Welcome\nclick e5\nget text")
			if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "step 2 of 4") {
				t.Fatalf("err = %v\n%s", err, out)
			}
			// Steps after the failure never reach the browser; the failure
			// snapshot does, so the agent can recover in its next call.
			if name == "no match" {
				// Its own output is already the latest snapshot.
				if got, want := server.actions(), []string{"open", "act"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("actions = %v, want %v", got, want)
				}
				if !strings.Contains(out, "heading Login") || strings.Contains(out, "Final snapshot") {
					t.Fatalf("no-match output:\n%s", out)
				}
				return
			}
			if got, want := server.actions(), []string{"open", "act", "snapshot"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("actions = %v, want %v", got, want)
			}
			if server.requests[2].Args["interactive"] != true {
				t.Fatalf("failure snapshot args = %#v", server.requests[2].Args)
			}
			for _, want := range []string{
				"Batch stopped at step 2 of 4: 1 ok, 1 failed, 2 not run",
				"[2] FAILED: act \"Sign in\" --expect-text Welcome",
				tc.wantError,
				"[3] not run: click e5",
				"[4] not run: get text",
				"Final snapshot (interactive):\n<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>\nbutton Sign in [ref=e9]",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestBrowserBatchSnapshotModesAndVerbose(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	out, _, err := executeCLI(t, deps, "browser", "batch", "--snapshot", "always", "--verbose", "--steps", "click e1")
	if err != nil {
		t.Fatal(err)
	}
	if got := server.actions(); !reflect.DeepEqual(got, []string{"click", "snapshot"}) {
		t.Fatalf("always: actions = %v", got)
	}
	if !strings.Contains(out, "[1] ok: click e1\nBrowser click completed.") || !strings.Contains(out, "Final snapshot") {
		t.Fatalf("always/verbose output:\n%s", out)
	}

	failing := &batchBrowserServer{respond: func(browserCommandRequestDTO) (int, string) {
		return http.StatusBadGateway, `{"error":"bad_gateway","code":"BROWSER_UNAVAILABLE","message":"runtime gone"}`
	}}
	deps = startBatchBrowserServer(t, failing)
	out, _, err = executeCLI(t, deps, "browser", "batch", "--snapshot", "never", "--steps", "click e1")
	if err == nil || strings.Contains(out, "Final snapshot") {
		t.Fatalf("never: err=%v\n%s", err, out)
	}
	if got := failing.actions(); !reflect.DeepEqual(got, []string{"click"}) {
		t.Fatalf("never: actions = %v", got)
	}

	if _, _, err := executeCLI(t, deps, "browser", "batch", "--snapshot", "sometimes", "--steps", "click e1"); ExitCode(err) != 2 {
		t.Fatalf("bad --snapshot err = %v", err)
	}
}

func TestBrowserBatchJSONOutput(t *testing.T) {
	server := &batchBrowserServer{respond: func(b browserCommandRequestDTO) (int, string) {
		if b.Action == "act" {
			return http.StatusOK, `{"outcome":"ambiguous","candidates":[{"ref":"e1"},{"ref":"e2"}]}`
		}
		return 0, ""
	}}
	deps := startBatchBrowserServer(t, server)
	out, _, err := executeCLI(t, deps, "browser", "--json", "batch", "--steps", `[["get","url"],["act","Nope"],["click","e1"]]`)
	if err == nil {
		t.Fatal("expected failure")
	}
	var result browserBatchResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if result.OK || result.Total != 3 || result.Completed != 1 || result.FailedStep != 2 {
		t.Fatalf("result = %+v", result)
	}
	statuses := []string{result.Steps[0].Status, result.Steps[1].Status, result.Steps[2].Status}
	if !reflect.DeepEqual(statuses, []string{"ok", "failed", "skipped"}) || !strings.HasPrefix(result.Steps[1].Error, "act was ambiguous") {
		t.Fatalf("steps = %+v", result.Steps)
	}
	if !strings.Contains(result.Snapshot, "button Sign in [ref=e9]") {
		t.Fatalf("snapshot = %q", result.Snapshot)
	}
}

func TestBrowserBatchReadsFileAndStdin(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	path := filepath.Join(t.TempDir(), "steps.txt")
	if err := os.WriteFile(path, []byte("click e1\nclick e2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCLI(t, deps, "browser", "batch", path); err != nil {
		t.Fatal(err)
	}
	deps.In = strings.NewReader("ao browser click e3\n")
	if _, _, err := executeCLI(t, deps, "browser", "batch"); err != nil {
		t.Fatal(err)
	}
	var refs []any
	for _, r := range server.requests {
		refs = append(refs, r.Args["ref"])
	}
	if !reflect.DeepEqual(refs, []any{"e1", "e2", "e3"}) {
		t.Fatalf("refs = %v", refs)
	}
}

func TestBrowserBatchForwardsEachStepToRemoteBrowser(t *testing.T) {
	var mu sync.Mutex
	var forwarded [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteBrowserRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		forwarded = append(forwarded, body.Args)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if body.Args[0] == "click" {
			_, _ = io.WriteString(w, `{"output":"","error":"element e7 not found"}`)
			return
		}
		_, _ = io.WriteString(w, `{"output":"https://example.com/\n"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envAgentBrowserURL, srv.URL)
	t.Setenv(envAgentBrowserToken, "remote-token")

	out, _, err := executeCLIRoot(t, Deps{HTTPClient: srv.Client()}, "browser", "batch", "--steps", "get url\ntab list\nclick e7\nget title")
	if err == nil || !strings.Contains(out, "element e7 not found") || !strings.Contains(out, "[4] not run: get title") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	want := [][]string{{"get", "url"}, {"tab", "list"}, {"click", "e7"}, {"snapshot", "--interactive"}}
	if !reflect.DeepEqual(forwarded, want) {
		t.Fatalf("forwarded = %#v", forwarded)
	}
}

func TestBrowserBatchRemoteStillRejectsNonPageSteps(t *testing.T) {
	t.Setenv(envAgentBrowserURL, "http://127.0.0.1:1")
	t.Setenv(envAgentBrowserToken, "remote-token")
	for _, steps := range []string{"get url\nbatch -", "get url\n--verbose batch -"} {
		_, _, err := executeCLIRoot(t, Deps{}, "browser", "batch", "--steps", steps)
		if ExitCode(err) != 2 || !strings.Contains(err.Error(), "step 2") {
			t.Fatalf("%q: err = %v", steps, err)
		}
	}
}

func TestBrowserBatchSkipsFailureSnapshotWhenNoMatchAlreadyHasOne(t *testing.T) {
	server := &batchBrowserServer{respond: func(b browserCommandRequestDTO) (int, string) {
		if b.Action == "act" {
			return http.StatusOK, `{"outcome":"no-match","snapshot":"checkbox Terms [ref=e12]"}`
		}
		return 0, ""
	}}
	deps := startBatchBrowserServer(t, server)
	out, _, err := executeCLI(t, deps, "browser", "batch", "--steps", "act Terms --action check\nclick e3")
	if err == nil {
		t.Fatal("expected failure")
	}
	if got := server.actions(); !reflect.DeepEqual(got, []string{"act"}) {
		t.Fatalf("actions = %v", got)
	}
	if strings.Contains(out, "Final snapshot") || !strings.Contains(out, "checkbox Terms [ref=e12]") {
		t.Fatalf("output:\n%s", out)
	}
}

// An approval gate in the browser service refuses a step with
// BROWSER_ACTION_DENIED; the batch must stop there and name the step.
func TestBrowserBatchStopsAtDeniedStep(t *testing.T) {
	server := &batchBrowserServer{respond: func(b browserCommandRequestDTO) (int, string) {
		if b.Action == "click" {
			return http.StatusForbidden, `{"error":"forbidden","code":"BROWSER_ACTION_DENIED","message":"The user denied this browser action"}`
		}
		return 0, ""
	}}
	deps := startBatchBrowserServer(t, server)
	out, _, err := executeCLI(t, deps, "browser", "batch", "--snapshot", "never", "--steps", "fill e2 ada\nclick e3\nget text body")
	if err == nil || !strings.Contains(err.Error(), "step 2 of 3") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if got := server.actions(); !reflect.DeepEqual(got, []string{"fill", "click"}) {
		t.Fatalf("actions = %v", got)
	}
	if !strings.Contains(out, "[2] FAILED: click e3\n    error: The user denied this browser action (BROWSER_ACTION_DENIED)") ||
		!strings.Contains(out, "[3] not run: get text body") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestBrowserBatchAllowsHelpAsData(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	_, _, err := executeCLI(t, deps, "browser", "batch", "--steps",
		"fill e2 help\nact Help --action fill --value help\nwait --text help")
	if err != nil {
		t.Fatal(err)
	}
	if got := server.actions(); !reflect.DeepEqual(got, []string{"fill", "act", "wait"}) {
		t.Fatalf("actions = %v", got)
	}
}

func TestBrowserBatchPrintsRemoteTabList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output":"t1 Example https://example.com/\n"}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envAgentBrowserURL, srv.URL)
	t.Setenv(envAgentBrowserToken, "remote-token")
	out, _, err := executeCLIRoot(t, Deps{HTTPClient: srv.Client()}, "browser", "batch", "--steps", "tab list\ntab new")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "t1 Example https://example.com/") != 1 {
		t.Fatalf("tab list output should print once, tab new not at all:\n%s", out)
	}
}

// lockedBuffer lets the fake daemon read what the CLI has printed so far.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A slow step must not hide the steps that already ran: if the harness kills
// the batch mid-way, the agent still sees which steps touched the page.
func TestBrowserBatchStreamsEachStepAsItFinishes(t *testing.T) {
	out := &lockedBuffer{}
	var seenAtStep2 string
	server := &batchBrowserServer{respond: func(b browserCommandRequestDTO) (int, string) {
		if b.Action == "wait" {
			seenAtStep2 = out.String()
		}
		return 0, ""
	}}
	deps := startBatchBrowserServer(t, server)
	deps.Out = out
	deps.Err = &lockedBuffer{}
	cmd := NewRootCommand(deps)
	cmd.SetArgs([]string{"browser", "batch", "--steps", "click e1\nwait --text Saved\nget text body"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenAtStep2, "[1] ok: click e1") || strings.Contains(seenAtStep2, "[2]") {
		t.Fatalf("output when step 2 started:\n%q", seenAtStep2)
	}
	final := out.String()
	last, summary := strings.Index(final, "[3] ok"), strings.Index(final, "Batch completed: 3/3 steps ok")
	if last < 0 || summary < 0 || last > summary {
		t.Fatalf("summary should come after the steps:\n%s", final)
	}
}

func TestBrowserBatchRefusesTerminalStdin(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	restore := browserBatchStdinIsTerminal
	browserBatchStdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { browserBatchStdinIsTerminal = restore })
	_, _, err := executeCLI(t, deps, "browser", "batch")
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "--steps") {
		t.Fatalf("err = %v", err)
	}
	if got := server.actions(); len(got) != 0 {
		t.Fatalf("actions = %v", got)
	}
}

func TestBrowserBatchStepsFlagWinsEvenWhenEmpty(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	path := filepath.Join(t.TempDir(), "steps.txt")
	if err := os.WriteFile(path, []byte("click e1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCLI(t, deps, "browser", "batch", "--steps=", path); ExitCode(err) != 2 {
		t.Fatalf("--steps= with a file: err = %v", err)
	}
	deps.In = strings.NewReader("click e2\n")
	if _, _, err := executeCLI(t, deps, "browser", "batch", "--steps="); ExitCode(err) != 2 {
		t.Fatalf("--steps= alone: err = %v", err)
	}
	if got := server.actions(); len(got) != 0 {
		t.Fatalf("actions = %v", got)
	}
}

// Fill and type values can be credentials: they reach the browser unchanged
// but never appear in the batch report, its JSON, or validation errors.
func TestBrowserBatchRedactsTypedValues(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	steps := "fill e2 s3cret-pw\ntype e3 s3cret-pw\nact Password --action fill --value s3cret-pw\nact Password --action=fill --value=s3cret-pw\nclick e4"
	out, _, err := executeCLI(t, deps, "browser", "batch", "--verbose", "--steps", steps)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cret-pw") || !strings.Contains(out, "[1] ok: fill e2 <redacted>") ||
		!strings.Contains(out, "--value <redacted>") || !strings.Contains(out, "--value=<redacted>") {
		t.Fatalf("output:\n%s", out)
	}
	if server.requests[0].Args["text"] != "s3cret-pw" || server.requests[2].Args["value"] != "s3cret-pw" {
		t.Fatalf("the browser must still get the real value: %#v", server.requests)
	}
	jsonOut, _, err := executeCLI(t, deps, "browser", "--json", "batch", "--steps", steps)
	if err != nil || strings.Contains(jsonOut, "s3cret-pw") {
		t.Fatalf("json err=%v out=%s", err, jsonOut)
	}
	_, _, err = executeCLI(t, deps, "browser", "batch", "--steps", "fill e2 s3cret-pw\nclik e3\nfill e4 s3cret-pw extra")
	if err == nil || strings.Contains(err.Error(), "s3cret-pw") {
		t.Fatalf("validation error = %v", err)
	}
	if got := browserBatchReportArgs([]string{"fill", "e2", "-pa55"}); got[2] != browserBatchRedacted {
		t.Fatalf("a dash-leading value must be redacted too: %#v", got)
	}
	for _, tc := range []struct{ step, want []string }{
		{[]string{"find", "label", "Password", "fill", "hunter2"}, []string{"find", "label", "Password", "fill", browserBatchRedacted}},
		{[]string{"find", "role", "textbox", "type", "hunter2", "--exact"}, []string{"find", "role", "textbox", "type", browserBatchRedacted, browserBatchRedacted}},
		{[]string{"find", "text", "Sign in", "click"}, []string{"find", "text", "Sign in", "click"}},
		{[]string{"find", "nth", "0", "input[type=password]", "fill", "hunter2"}, []string{"find", "nth", "0", "input[type=password]", "fill", browserBatchRedacted}},
		{[]string{"find", "role", "textbox", "--name", "Password", "fill", "hunter2"}, []string{"find", "role", "textbox", "--name", "Password", "fill", browserBatchRedacted}},
		{[]string{"find", "role", "textbox", "--name=Password", "--exact", "type", "hunter2"}, []string{"find", "role", "textbox", "--name=Password", "--exact", "type", browserBatchRedacted}},
		{[]string{"find", "label", "fill", "click"}, []string{"find", "label", "fill", "click"}},
		{[]string{"set", "credentials", "ada", "hunter2"}, []string{"set", "credentials", browserBatchRedacted, browserBatchRedacted}},
	} {
		if got := browserBatchReportArgs(tc.step); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%v: got %#v", tc.step, got)
		}
	}
	for _, step := range [][]string{{"keyboard", "type", "hunter2"}, {"keyboard", "inserttext", "hunter2"}} {
		if got := browserBatchReportArgs(step); got[2] != browserBatchRedacted || got[1] != step[1] {
			t.Fatalf("remote keyboard text must be redacted: %#v", got)
		}
	}
}

// A flag-parse error quotes the offending token; for a typed value that token
// is the secret, so the error must not repeat it.
func TestBrowserBatchValidationErrorsHideTypedValues(t *testing.T) {
	server := &batchBrowserServer{}
	deps := startBatchBrowserServer(t, server)
	for _, steps := range []string{
		"fill e2 -s3cretpw",
		"type e2 --s3cretpw",
		"type e2 ok -s3cretpw",
	} {
		_, _, err := executeCLI(t, deps, "browser", "batch", "--steps", steps)
		if ExitCode(err) != 2 || strings.Contains(err.Error(), "s3cretpw") || !strings.Contains(err.Error(), "--") {
			t.Fatalf("%q: err = %v", steps, err)
		}
	}
	// Errors that do not echo the value keep their detail.
	_, _, err := executeCLI(t, deps, "browser", "batch", "--steps", "fill e2")
	if err == nil || !strings.Contains(err.Error(), "accepts 2 arg") {
		t.Fatalf("arg count err = %v", err)
	}
}

func TestBrowserBatchReportMasksURLCredentials(t *testing.T) {
	got := browserBatchReportArgs([]string{"open", "https://ada:hunter2@example.test/path?q=1"})
	if strings.Contains(got[1], "hunter2") || strings.Contains(got[1], "ada") || !strings.Contains(got[1], "example.test/path?q=1") {
		t.Fatalf("report = %#v", got)
	}
	if got := browserBatchReportArgs([]string{"open", "https://example.test/a@b"}); got[1] != "https://example.test/a@b" {
		t.Fatalf("a URL without credentials must stay as is: %#v", got)
	}
}

func TestBrowserBatchEchoEscapesControlCharacters(t *testing.T) {
	got := browserBatchEcho([]string{"get", "text", "a\nb\x1b"})
	if strings.ContainsAny(got, "\n\x1b") || !strings.Contains(got, `"a\nb\x1b"`) {
		t.Fatalf("echo = %q", got)
	}
}
