package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

func triageTestDeps(t *testing.T) (Deps, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv("AO_RUN_FILE", filepath.Join(dir, "running.json"))
	out := &bytes.Buffer{}
	return Deps{In: strings.NewReader(""), Out: out, Err: out, Now: func() time.Time { return time.Date(2026, 10, 20, 12, 34, 56, 0, time.UTC) }, LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}, out, dir
}

func TestTriageNoLaunch(t *testing.T) {
	deps, out, state := triageTestDeps(t)
	deps.RunInteractiveCommand = func(context.Context, string, string, []string, io.Reader, io.Writer, io.Writer) error {
		t.Fatal("unexpected launch")
		return nil
	}
	cmd := NewRootCommand(deps)
	cmd.SetArgs([]string{"triage", "--no-launch"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "triage", "2026-10-20T12-34-56.000000000Z")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v", info.Mode())
	}
	for _, name := range []string{"context.md", "prompt.md", "PLAYBOOK.md"} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", name, info.Mode())
		}
		if !strings.Contains(out.String(), path) {
			t.Fatalf("missing printed path %s", path)
		}
		if name == "context.md" {
			home, _ := os.UserHomeDir()
			if !strings.Contains(string(b), "unavailable") || (home != "" && strings.Contains(string(b), home)) {
				t.Fatalf("unexpected context: %s", b)
			}
			if len(b) > triageMaxBytes {
				t.Fatal("context exceeds cap")
			}
		}
		if name == "PLAYBOOK.md" && !bytes.Equal(b, skillassets.BugTriagePlaybook()) {
			t.Fatal("wrong playbook")
		}
	}
}

func TestTriageAgentSelection(t *testing.T) {
	for _, tc := range []struct {
		name, requested, want string
		installed             []string
		wantErr               bool
	}{
		{name: "none"},
		{name: "claude only", installed: []string{"claude"}, want: "claude"},
		{name: "codex only", installed: []string{"codex"}, want: "codex"},
		{name: "both non TTY", installed: []string{"claude", "codex"}, wantErr: true},
		{name: "explicit", requested: "codex", installed: []string{"claude", "codex"}, want: "codex"},
		{name: "missing explicit", requested: "claude", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := triageTestDeps(t)
			deps.LookPath = func(name string) (string, error) {
				for _, s := range tc.installed {
					if s == name {
						return "/bin/" + name, nil
					}
				}
				return "", exec.ErrNotFound
			}
			c := &commandContext{deps: deps}
			got, err := c.selectTriageAgent(NewRootCommand(deps), tc.requested)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("got %q, %v", got, err)
			}
			if tc.name == "missing explicit" && !strings.Contains(err.Error(), "claude is not installed or was not found on PATH") {
				t.Fatal(err)
			}
		})
	}
}

func TestTriageLaunchArgvAndCwd(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			deps, out, state := triageTestDeps(t)
			deps.LookPath = func(name string) (string, error) {
				if name == agent {
					return "/missing/" + name, nil
				}
				return "", exec.ErrNotFound
			}
			called := false
			deps.RunInteractiveCommand = func(_ context.Context, name, dir string, args []string, in io.Reader, stdout, stderr io.Writer) error {
				called = true
				for _, arg := range args {
					if strings.ContainsAny(arg, "\r\n") {
						t.Fatalf("argument contains a newline: %q", arg)
					}
				}
				promptPath := filepath.Join(dir, "prompt.md")
				wantPrompt := fmt.Sprintf(`Read the file "%s" and follow its instructions exactly: it is your bug-triage playbook, and it starts with asking the user what went wrong.`, promptPath)
				if name != "/missing/"+agent || filepath.Dir(dir) != filepath.Join(state, "triage") || !reflect.DeepEqual(args, []string{"--model", "example-model", wantPrompt}) {
					t.Fatalf("launch: %s %q cwd=%s", name, args, dir)
				}
				prompt, err := os.ReadFile(promptPath)
				if err != nil || string(prompt) != triagePrompt(dir, true) {
					t.Fatalf("prompt file: %q, %v", prompt, err)
				}
				if in != deps.In || stdout != out || stderr != out {
					t.Fatal("stdio not inherited")
				}
				return nil
			}
			cmd := NewRootCommand(deps)
			cmd.SetArgs([]string{"triage", "--agent", agent, "--model", "example-model"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("not launched")
			}
		})
	}
}

func TestTriageRedaction(t *testing.T) {
	for _, input := range []string{
		"?api_key=private&ok=yes", `{"token":"private"}`, `{'password':'private'}`,
		`{"secret":"private\"escaped"}`, "Authorization: Basic private", "Authorization: Bearer private",
		"Bearer private", "API_KEY=private", "sk-private", "ghp_12345678901234567890", "github_pat_12345678901234567890",
		"/home/private/.ao", "pairing_code=private", "pairing code: private",
	} {
		got := redactTriage(input, "/home/private")
		if strings.Contains(got, "private") || got == input {
			t.Errorf("%q => %q", input, got)
		}
	}
	if got := redactTriage("/home/person/.ao/data", "/home/person"); got != "~/.ao/data" {
		t.Fatal(got)
	}
}

func TestTriageRedactionCompleteValuesAndHomes(t *testing.T) {
	for _, key := range []string{"password", "passwd", "secret", "token", "api_key", "api-key", "apikey", "authorization", "ACCESS_TOKEN"} {
		for _, value := range []string{`private`, `"first second"`, `'first second'`, `"first\"second"`} {
			for _, input := range []string{key + "=" + value, `{"` + key + `":` + value + `}`, "?" + key + "=" + value + "&ok=yes"} {
				t.Run(input, func(t *testing.T) {
					got := redactTriage(input, "")
					if strings.Contains(got, "private") || strings.Contains(got, "first") || strings.Contains(got, "second") || !strings.Contains(got, "[redacted-secret]") {
						t.Fatalf("%q => %q", input, got)
					}
					if strings.HasPrefix(input, "?") && !strings.HasSuffix(got, "&ok=yes") {
						t.Fatal(got)
					}
				})
			}
		}
	}
	for _, tc := range []struct{ input, home, want string }{
		{`https://user:pass@host/path`, "", `https://***@host/path`},
		{`https://token:private@host/path`, "", `https://***@host/path`},
		{`https://user:pass@word@host/path`, "", `https://***@host/path`},
		{`ssh://user@host/path`, "", `ssh://***@host/path`},
		{`$HOME/.ao`, "", `~/.ao`},
		{`/Users/alice/.ao`, "", `~/.ao`},
		{`/home/alice/.ao`, "", `~/.ao`},
		{`C:\Users\alice\.ao`, "", `~\.ao`},
		{`C:\\Users\\alice\\.ao`, "", `~\\.ao`},
		{`C:/Users/alice/.ao`, `C:\Users\alice`, `~/.ao`},
		{`c:\users\ALICE\.ao`, `C:\Users\alice`, `~\.ao`},
		{`c:\\users\\ALICE\\.ao`, `C:\Users\alice`, `~\\.ao`},
		{`file:///Users/alice/.ao`, "", `file://~/.ao`},
		{`file:///home/alice/.ao`, "", `file://~/.ao`},
		{`file:///C:/Users/alice/.ao`, "", `file:///~/.ao`},
		{`/custom/ALICE/.ao`, `/custom/alice`, `~/.ao`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := redactTriage(tc.input, tc.home); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestTriageRedactionEscapedQuotedValues(t *testing.T) {
	for _, input := range []string{
		`err="token=\"private value\""`,
		`err="{\"detail\":\"token=\\\"private value\\\"\"}" next=ok`,
		`err="password=\"private`,
		`token=\\private`,
		`token=\'private value\'`,
		`"token":\"private value\"`,
		`err="{\"detail\":\"{\\\"token\\\":\\\"private value\\\"}\"}"`,
	} {
		got := redactTriage(input, "")
		if strings.Contains(got, "private") || !strings.Contains(got, "[redacted-secret]") {
			t.Errorf("%q => %q", input, got)
		}
	}
	for input, want := range map[string]string{
		`err="token=\"private\" status=ok"`: `err="token=\"[redacted-secret]\" status=ok"`,
		`token=\\ next`:                     `token=[redacted-secret] next`,
		`password=\\`:                       `password=[redacted-secret]`,
	} {
		if got := redactTriage(input, ""); got != want {
			t.Errorf("%q => %q, want %q", input, got, want)
		}
	}
}

func TestTriageRejectsSymlinkedParent(t *testing.T) {
	deps, _, state := triageTestDeps(t)
	external := t.TempDir()
	old := filepath.Join(external, deps.Now().Add(-8*24*time.Hour).Format(triageTimestamp))
	if err := os.Mkdir(old, 0o700); err != nil {
		t.Fatal(err)
	}
	mtime := deps.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(old, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(state, "triage")
	if err := os.Symlink(external, parent); err != nil {
		t.Skip(err)
	}
	if err := pruneTriage(parent, deps.Now()); err == nil {
		t.Fatal("pruned through symlinked parent")
	}
	cmd := NewRootCommand(deps)
	cmd.SetArgs([]string{"triage", "--no-launch"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(external)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(old) {
		t.Fatalf("symlink target changed: %v, %v", entries, err)
	}
}

func TestTriageLogTailAndCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	var b strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "line %d %s\n", i, strings.Repeat("x", 200))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := triageLogTail(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 24<<10 || strings.Contains(got, "line 0 ") || !strings.Contains(got, "line 399 ") || len(strings.Split(got, "\n")) > 200 {
		t.Fatal("tail is not bounded or not recent")
	}
	out := &triageOutput{remaining: 1024}
	n, err := out.Write([]byte(strings.Repeat("a", 2000)))
	if n != 2000 || err != nil || out.Len() != 1024 {
		t.Fatal("version output not capped")
	}
}

func TestTriageSessionsOnlyIncludesRecentSafeFields(t *testing.T) {
	deps, _, _ := triageTestDeps(t)
	var body strings.Builder
	body.WriteString(`{"sessions":[`)
	for i := 0; i < 25; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":"s%d","harness":"codex","status":"working","updatedAt":"2026-10-%02dT00:00:00Z","transcript":"private-content"}`, i, i+1)
	}
	body.WriteString(`]}`)
	deps.HTTPClient = &http.Client{Transport: triageRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://127.0.0.1:1234/api/v1/sessions" {
			t.Errorf("unexpected URL %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body.String()))}, nil
	})}
	c := &commandContext{deps: deps}
	var out bytes.Buffer
	if err := c.triageSessions(context.Background(), 1234, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "id=") != 20 || !strings.HasPrefix(out.String(), "id=s24 ") || strings.Contains(out.String(), "private-content") || strings.Contains(out.String(), "id=s0 ") {
		t.Fatal(out.String())
	}
}

type triageRoundTrip func(*http.Request) (*http.Response, error)

func (f triageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTriageMalformedDaemonConfigDoesNotAbortSnapshot(t *testing.T) {
	deps, _, dir := triageTestDeps(t)
	t.Setenv("AO_PORT", "invalid")
	c := &commandContext{deps: deps.withDefaults()}
	got, _, _ := c.triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
	if !strings.Contains(got, "Daemon: unavailable: invalid AO_PORT") {
		t.Fatal(got)
	}
}

func TestTriageDaemonFields(t *testing.T) {
	for _, tc := range []struct {
		name, health, ready, wantHealth, wantReady string
		state                                      daemonState
	}{
		{name: "stopped", state: stateStopped},
		{name: "ready", health: "ok", ready: "ready", wantHealth: "ok", wantReady: "ready", state: stateReady},
		{name: "spaces", health: "not ok", wantHealth: `"not ok"`, state: stateUnhealthy},
		{name: "punctuation", health: "not-ok", wantHealth: `"not-ok"`, state: stateUnhealthy},
		{name: "health newline", health: "bad\nhealth", wantHealth: `"bad\nhealth"`, state: stateUnhealthy},
		{name: "ready newline", health: "ok", ready: "not\r\nready", wantHealth: "ok", wantReady: `"not\r\nready"`, state: stateNotReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, dir := triageTestDeps(t)
			run := filepath.Join(dir, "running.json")
			deps.ProcessAlive = func(int) bool { return true }
			deps.HTTPClient = &http.Client{Transport: triageRoundTrip(func(r *http.Request) (*http.Response, error) {
				var body []byte
				switch r.URL.Path {
				case "/healthz", "/readyz":
					status := tc.health
					if r.URL.Path == "/readyz" {
						status = tc.ready
					}
					body, _ = json.Marshal(probeResult{Status: status, Service: daemonmeta.ServiceName, PID: 1234})
				case "/api/v1/sessions":
					body = []byte(`{"sessions":[]}`)
				default:
					t.Fatalf("unexpected request: %s", r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			if tc.state != stateStopped {
				if err := runfile.Write(run, runfile.Info{PID: 1234, Port: 12345}); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, run)
			wantEvidence := "- Daemon: " + string(tc.state)
			for _, field := range []struct{ name, value string }{{"Health", tc.wantHealth}, {"Ready", tc.wantReady}} {
				if field.value == "" {
					if strings.Contains(snapshot, "\n"+field.name+":") {
						t.Errorf("empty %s in context: %s", field.name, snapshot)
					}
					continue
				}
				if !strings.Contains(snapshot, "\n"+field.name+": "+field.value+"\n") {
					t.Errorf("missing %s in context: %s", field.name, snapshot)
				}
				wantEvidence += ", " + strings.ToLower(field.name) + ": " + field.value
			}
			if !strings.Contains(draft, wantEvidence+"\n") {
				t.Errorf("want evidence %q in draft: %s", wantEvidence, draft)
			}
		})
	}
}

func TestTriageVersionProbesRunConcurrently(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	deps, _, dir := triageTestDeps(t)
	t.Setenv("TRIAGE_TEST_PROBES", dir)
	for _, name := range []string{"claude", "codex"} {
		other := "codex"
		if name == "codex" {
			other = "claude"
		}
		script := fmt.Sprintf("#!/bin/sh\n: > \"$TRIAGE_TEST_PROBES/%s.started\"\nwhile [ ! -f \"$TRIAGE_TEST_PROBES/%s.started\" ]; do /bin/sleep 0.01; done\necho '%s test-version'\n", name, other, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	deps.LookPath = func(name string) (string, error) {
		if name == "claude" || name == "codex" {
			return filepath.Join(dir, name), nil
		}
		return "", exec.ErrNotFound
	}
	_, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
	for _, name := range []string{"claude", "codex"} {
		if !strings.Contains(draft, "- "+name+" version: "+name+" test-version\n") {
			t.Fatal(draft)
		}
	}
}

func TestTriageBuildMetadata(t *testing.T) {
	oldCommit, oldDate := Commit, Date
	t.Cleanup(func() { Commit, Date = oldCommit, oldDate })
	for _, metadata := range []string{"", "build-metadata"} {
		t.Run("metadata="+metadata, func(t *testing.T) {
			Commit, Date = metadata, metadata
			deps, _, dir := triageTestDeps(t)
			snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
			if strings.Contains(snapshot, "Commit: "+metadata+"\n") != (metadata != "") || strings.Contains(snapshot, "Build date: "+metadata+"\n") != (metadata != "") || strings.Contains(draft, ", commit "+metadata+",") != (metadata != "") {
				t.Fatal(snapshot, draft)
			}
		})
	}
}

func TestTriageSensitiveKeyForms(t *testing.T) {
	for _, key := range []string{"client_secret", "clientSecret", "refresh_token", "accessToken", "id_token", "private_key", "pairing_code", "pairing password", "session-secret", "GH_TOKEN", "MY_API_KEY_VALUE", "apiToken"} {
		for _, value := range []string{"SENTINELVALUE", strings.Repeat("abCDEF0123+/", 4), strings.Repeat("abcdef0123456789", 3)} {
			for _, input := range []string{
				`{"` + key + `":"` + value + `"}`, `{"` + key + `":` + value + `}`,
				key + `="` + value + `"`, key + `=` + value, key + `: '` + value + `'`, key + `: ` + value,
				`--` + key + ` "` + value + `"`, `--` + key + ` ` + value, `--` + key + `="` + value + `"`, `--` + key + `=` + value,
				`error="{\"` + key + `\":\"` + value + `\"}"`,
			} {
				t.Run(input, func(t *testing.T) {
					got := redactTriage(input, "")
					if strings.Contains(got, value) || !strings.Contains(got, "[redacted-secret]") {
						t.Fatalf("%s => %s", input, got)
					}
					for _, query := range triageKeywords("ERROR connection handshake failed " + input) {
						if strings.Contains(query, strings.ToLower(value)) {
							t.Fatalf("secret in query: %s", query)
						}
					}
				})
			}
		}
	}
	for _, input := range []string{"pairing 123456", "pairing code 1234567", "code: 12345678"} {
		if got := redactTriage(input, ""); strings.ContainsAny(got, "0123456789") {
			t.Fatal(got)
		}
	}
}

func TestTriageManagedAgentMatchesProbeAndLaunch(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			deps, _, data := triageTestDeps(t)
			t.Setenv("AO_MANAGED_HARNESS_INSTALLS", map[bool]string{true: "on", false: "off"}[enabled])
			bin := filepath.Join(data, "harnesses", "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			name, script := "codex", "#!/bin/sh\necho managed-version\n"
			if runtime.GOOS == "windows" {
				name, script = "codex.cmd", "@echo managed-version\r\n"
			}
			managed := filepath.Join(bin, name)
			if err := os.WriteFile(managed, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			fallback := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(fallback, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(fallback))
			t.Setenv("APPDATA", t.TempDir())
			t.Setenv("LOCALAPPDATA", t.TempDir())
			deps.ResolveAgentBinary = DefaultDeps().ResolveAgentBinary
			deps.RunInteractiveCommand = func(_ context.Context, path, dir string, _ []string, _ io.Reader, _, _ io.Writer) error {
				want := fallback
				if enabled {
					want = managed
				}
				if path != want {
					t.Fatalf("launched %q, want %q", path, want)
				}
				raw, err := os.ReadFile(filepath.Join(dir, "context.md"))
				home, _ := os.UserHomeDir()
				if err != nil || !strings.Contains(string(raw), "codex: "+redactTriage(path, home)) {
					t.Fatal(err, string(raw))
				}
				return nil
			}
			cmd := NewRootCommand(deps)
			cmd.SetArgs([]string{"triage", "--agent", "codex"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
