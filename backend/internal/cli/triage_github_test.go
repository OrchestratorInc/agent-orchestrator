package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeTriageGH(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake-gh scripts use shell case/printf and /bin/sleep; batch execution has a native Windows fixture")
	}
	bin := t.TempDir()
	path := filepath.Join(bin, "gh")
	t.Setenv("GH_HOST", "other.example")
	t.Setenv("GH_DEBUG", "api")
	script = "#!/bin/sh\n[ \"$GH_HOST\" = github.com ] || exit 91\n[ \"$GH_PROMPT_DISABLED\" = 1 ] && [ \"$GH_NO_UPDATE_NOTIFIER\" = 1 ] || exit 90\n" + script
	script = strings.Replace(script, "#!/bin/sh\n", "#!/bin/sh\nif [ \"$1\" != auth ]; then\ncase \" $* \" in *\" --repo OrchestratorInc/agent-orchestrator \"*) ;; *) exit 92;; esac\nfi\n", 1)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return path
}

func TestTriageJSONAndDraft(t *testing.T) {
	deps, out, state := triageTestDeps(t)
	for _, name := range []string{"daemon", "desktop", "hooks"} {
		if err := os.WriteFile(filepath.Join(state, name+".log"), []byte("ERROR "+name+" connection failed token=private\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := NewRootCommand(deps)
	cmd.SetArgs([]string{"triage", "--no-launch", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var packet map[string]string
	if err := json.Unmarshal(out.Bytes(), &packet); err != nil {
		t.Fatal(err, out.String())
	}
	if len(packet) != 5 {
		t.Fatal(packet)
	}
	for _, key := range []string{"dir", "context", "report", "playbook"} {
		if !filepath.IsAbs(packet[key]) {
			t.Fatalf("%s=%q", key, packet[key])
		}
	}
	for _, key := range []string{"context", "report", "playbook"} {
		if !strings.Contains(packet["prompt"], packet[key]) {
			t.Fatal("prompt missing absolute path", key)
		}
	}
	prompt, _ := os.ReadFile(filepath.Join(packet["dir"], "prompt.md"))
	if string(prompt) != packet["prompt"] || !strings.Contains(string(prompt), "In-app mode") {
		t.Fatal(string(prompt))
	}
	draft, err := os.ReadFile(packet["report"])
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(packet["report"])
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	for _, want := range []string{"status: draft\n", "Title: <one line", "Seen on " + Version, runtime.GOOS, "daemon stopped", "Related: none", triageEvidenceHeading, "- Daemon: stopped\n", "claude: unavailable", "codex: unavailable", "daemon connection failed", "desktop connection failed", "hooks connection failed"} {
		if !strings.Contains(string(draft), want) {
			t.Errorf("missing %q in %s", want, draft)
		}
	}
	if strings.Contains(string(draft), "private") || strings.Contains(string(draft), "Duplicate candidates") || strings.Count(string(draft), "- Log excerpt:") > 8 {
		t.Fatal(string(draft))
	}
	snapshot, _ := os.ReadFile(packet["context"])
	for _, want := range []string{"# Orchestrator diagnostics", "App: ", "gh=missing", "auth=failed", "filing=unavailable", "daemon.log", "desktop.log", "hooks.log"} {
		if !strings.Contains(string(snapshot), want) {
			t.Error("missing", want)
		}
	}
}

func TestTriageJSONRequiresNoLaunch(t *testing.T) {
	deps, _, _ := triageTestDeps(t)
	cmd := NewRootCommand(deps)
	cmd.SetArgs([]string{"triage", "--json"})
	if err := cmd.Execute(); ExitCode(err) != 2 {
		t.Fatal(err)
	}
}

func TestTriageAuthTimeoutStillWritesFiles(t *testing.T) {
	deps, out, _ := triageTestDeps(t)
	deps.LookPath = func(name string) (string, error) { return "/unused/" + name, nil }
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	cmd := NewRootCommand(deps)
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"triage", "--no-launch", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var packet map[string]string
	if err := json.Unmarshal(out.Bytes(), &packet); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"context", "report", "playbook"} {
		if _, err := os.Stat(packet[key]); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _ := os.ReadFile(packet["context"])
	for _, want := range []string{"auth=unknown (timed out)\n", "filing=unknown\n", "label needs-triage=unavailable: timeout\n", "unavailable: timeout"} {
		if !strings.Contains(string(snapshot), want) {
			t.Errorf("missing %q in %s", want, snapshot)
		}
	}
	draft, _ := os.ReadFile(packet["report"])
	for _, name := range []string{"claude", "codex"} {
		if !strings.Contains(string(draft), "- "+name+" version: unavailable: timeout\n") {
			t.Fatal(string(draft))
		}
	}
	if strings.Contains(string(snapshot)+string(draft)+packet["prompt"], "filing=unavailable") {
		t.Fatal("timeout marked filing unavailable")
	}
}

func TestTriageDuplicateEvidence(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"candidate", `printf '[{"number":12,"title":"Stream breaks","state":"OPEN","url":"https://github.com/OrchestratorInc/agent-orchestrator/issues/12"}]'`, "issue #12 [OPEN] Stream breaks"},
		{"none", "printf '[]'", "none found"},
		{"unavailable", "exit 1", "unavailable: issue stream connection failed: search failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, dir := triageTestDeps(t)
			path := fakeTriageGH(t, tc.script)
			if err := os.WriteFile(filepath.Join(dir, "daemon.log"), []byte("ERROR stream connection failed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			deps.LookPath = func(name string) (string, error) {
				if name == "gh" {
					return path, nil
				}
				return "", exec.ErrNotFound
			}
			snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
			_, candidates, ok := strings.Cut(snapshot, "## Possible duplicates")
			if !ok || !strings.Contains(candidates, tc.want) {
				t.Fatal(snapshot)
			}
			if strings.Contains(draft, tc.want) || strings.Contains(draft, "pre-search") || strings.Contains(draft, "keyword search") {
				t.Fatal(draft)
			}
		})
	}
}

func TestTriagePreflightFakeGH(t *testing.T) {
	for _, tc := range []struct{ name, auth, labels, wantAuth, wantLabel, filing string }{
		{"present", "exit 0", `printf '[{"name":"needs-triage"}]'`, "ok", "present", "available"},
		{"stderr beside JSON", "exit 0", `echo warning >&2; printf '[{"name":"needs-triage"}]'`, "ok", "present", "available"},
		{"absent label", "exit 0", "printf '[]'", "ok", "absent", "available"},
		{"failed auth", "echo raw-auth-secret; exit 1", "printf '[]'", "failed", "absent", "unavailable"},
		{"label failure", "exit 0", "exit 1", "ok", "unknown", "available"},
		{"auth timeout", "exec /bin/sleep 10", "printf '[]'", "unknown (timed out)", "absent", "unknown"},
		{"label timeout", "exit 0", "exec /bin/sleep 10", "ok", "unavailable: timeout", "available"},
		{"malformed label", "exit 0", "printf 'bad json'", "ok", "unknown", "available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, dir := triageTestDeps(t)
			fakeTriageGH(t, "case \"$1\" in\nauth) "+tc.auth+";;\nlabel) "+tc.labels+";;\nesac\n")
			deps.LookPath = exec.LookPath
			start := time.Now()
			g := (&commandContext{deps: deps}).triagePreflight(context.Background())
			if g.Path == "" || g.Auth != tc.wantAuth || g.Label != tc.wantLabel || !strings.Contains(g.summary(), "filing="+tc.filing) || strings.Contains(g.summary(), "raw-auth-secret") {
				t.Fatal(g.summary())
			}
			if time.Since(start) > triageBudget {
				t.Fatal("preflight exceeded cap")
			}
			if tc.name == "present" {
				snapshot, _, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
				if !strings.Contains(snapshot, "auth=ok\n") {
					t.Fatal(snapshot)
				}
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		deps, _, _ := triageTestDeps(t)
		t.Setenv("PATH", t.TempDir())
		deps.LookPath = exec.LookPath
		g := (&commandContext{deps: deps}).triagePreflight(context.Background())
		if g.Path != "" || !strings.Contains(g.summary(), "gh=missing") || !strings.Contains(g.summary(), "filing=unavailable") {
			t.Fatal(g.summary())
		}
	})
}

func TestTriageKeywords(t *testing.T) {
	for _, tc := range []struct {
		name, logs string
		want       []string
	}{
		{"empty ready", "", []string{}},
		{"short error", "ERROR connection failed", []string{}},
		{"stopped", "INFO all good", []string{}},
		{"info error field", `level=INFO msg="scm observer poll completed" error="no error"`, []string{}},
		{"info on stderr", `2026-10-21T10:00:45.840Z [error] time=2026 level=INFO msg="scm observer poll completed"`, []string{}},
		{"error detail", `level=ERROR msg="launch failed" error="executable missing"`, []string{"launch failed executable missing"}},
		{"plain strips metadata", "2026-10-20 ERROR session_id=abc123 /Users/name/file 503 connection refused by remote peer", []string{"connection refused remote peer"}},
		{"slog", `time=2026 level=WARN msg="terminal launch failed" id=42`, []string{"terminal launch failed"}},
		{"json", `{"level":"ERROR","msg":"session stream disconnected","id":123}`, []string{"session stream disconnected"}},
		{"recent distinct", "ERROR old connection refused\nWARN terminal launch failed\nWARN terminal launch failed", []string{"terminal launch failed", "old connection refused"}},
		{"limit", "ERROR first signal failed\nERROR second signal failed\nERROR third signal failed\nERROR fourth signal failed", []string{"fourth signal failed", "third signal failed", "second signal failed"}},
		{"redacted before search", redactTriage("ERROR API_TOKEN=private path /home/person/file connection refused 123", "/home/person"), []string{"path connection refused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := triageKeywords(tc.logs); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestTriageLogMessages(t *testing.T) {
	for _, tc := range []struct {
		name, logs string
		want       []string
	}{
		{"info only", "INFO no errors\n" + `level=INFO msg="http request" error="request warning"` + "\n" + `{"level":"INFO","message":"error handler ready"}`, nil},
		{"info on stderr", `2026-10-21T10:00:45.840Z [error] time=2026 level=INFO msg="http request"`, nil},
		{"slog warning", `time=2026 level=WARN msg="terminal launch failed" error="executable not found"`, []string{"terminal launch failed: executable not found"}},
		{"json error", `{"level":"ERROR","message":"session stream disconnected","error":"read \"socket\": EOF"}`, []string{`session stream disconnected: read "socket": EOF`}},
		{"desktop warning", "2026-10-21T10:00:45.840Z [warn] terminal launch failed", []string{"terminal launch failed"}},
		{"panic and fatal", "panic: runtime failed\nfatal error: concurrent map writes\nFATAL daemon stopped", []string{"daemon stopped", "error: concurrent map writes", "runtime failed"}},
		{"deduplicated by message", "level=ERROR msg=\"launch failed\" error=old\nlevel=WARN msg=\"launch failed\" error=new", []string{"launch failed: new"}},
		{"empty error", "level=WARN msg=\"launch failed\" error=\"\"", []string{"launch failed"}},
		{"slog err", `level=ERROR msg="background session reconciliation on boot failed" err="database disk image is malformed"`, []string{"background session reconciliation on boot failed: database disk image is malformed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := triageLogMessages(tc.logs); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
	var logs strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&logs, "ERROR failure %d\n", i)
	}
	if got := triageLogMessages(logs.String()); len(got) != 8 || got[0] != "failure 9" || got[7] != "failure 2" {
		t.Fatal(got)
	}
}

func TestTriageLogEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, logs string
		want       []string
		search     bool
	}{
		{"info only", "level=INFO msg=\"http request\" error=none\n2026-10-21T10:00:45.840Z [error] time=2026 level=INFO msg=\"scm observer poll completed\"\n", []string{"- Logs: no warnings or errors in the last 2 lines\n", "Duplicate pre-search skipped: no error lines to search for"}, false},
		{"warnings and errors", "level=INFO msg=\"http request\"\nlevel=WARN msg=\"terminal launch failed\" error=\"executable not found\"\n{\"level\":\"ERROR\",\"msg\":\"session stream disconnected\",\"error\":\"socket closed\"}\n", []string{"- Log excerpt: terminal launch failed: executable not found\n", "- Log excerpt: session stream disconnected: socket closed\n"}, true},
		{"no keywords", "ERROR failed\n", []string{"- Log excerpt: failed\n", "Duplicate pre-search skipped: no error lines to search for"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, dir := triageTestDeps(t)
			calls := filepath.Join(dir, "calls")
			t.Setenv("GH_TEST_CALLS", calls)
			path := fakeTriageGH(t, `printf '%s\n' "$*" >> "$GH_TEST_CALLS"; echo '[]'`)
			deps.LookPath = func(name string) (string, error) {
				if name == "gh" {
					return path, nil
				}
				return "", exec.ErrNotFound
			}
			if err := os.WriteFile(filepath.Join(dir, "daemon.log"), []byte(tc.logs), 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
			if strings.Contains(draft, "pre-search") || strings.Contains(draft, "keyword search") {
				t.Fatal(draft)
			}
			for _, text := range []string{snapshot, draft} {
				for _, want := range tc.want {
					if text == draft && strings.HasPrefix(want, "Duplicate") {
						continue
					}
					if !strings.Contains(text, want) {
						t.Errorf("missing %q in %s", want, text)
					}
				}
				if strings.Contains(text, "http request") || strings.Contains(text, "scm observer poll completed") {
					t.Fatal(text)
				}
			}
			commands, err := os.ReadFile(calls)
			if err != nil || strings.Contains(string(commands), "issue list") != tc.search {
				t.Fatal(err, string(commands))
			}
			if !tc.search && strings.Contains(draft, "Duplicate candidates") {
				t.Fatal(draft)
			}
		})
	}
}

func TestTriageSnapshotRedactsNestedLogValues(t *testing.T) {
	deps, _, dir := triageTestDeps(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	logger.Error("github sync failed", "err", `{"detail":"token=\"SENTINELPRIVATEVALUE\""}`)
	logger.Error("background session reconciliation on boot failed", "err", "database disk image is malformed")
	if err := os.WriteFile(filepath.Join(dir, "daemon.log"), logs.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
	for _, text := range []string{snapshot, draft} {
		if strings.Contains(text, "SENTINELPRIVATEVALUE") {
			t.Fatalf("secret leaked: %s", text)
		}
		if !strings.Contains(text, "database disk image is malformed") {
			t.Errorf("err detail missing: %s", text)
		}
	}
}

func TestTriageDuplicateSearchFakeGH(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		count              int
	}{
		{"results deduped", `printf '[{"number":12,"title":"Stream breaks","state":"OPEN","url":"https://github.com/OrchestratorInc/agent-orchestrator/issues/12"}]'`, "- issue #12 [OPEN] Stream breaks", 1},
		{"none", "printf '[]'", "none found for: session stream disconnected; terminal launch failure", 0},
		{"issue timeout", `if [ "$1" = issue ]; then exec /bin/sleep 10; fi; printf '[]'`, "unavailable: issue session stream disconnected: timeout", 0},
		{"failed", "echo raw-secret; exit 1", "search failed", 0},
		{"malformed", "printf '[broken'", "search failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fakeTriageGH(t, tc.script)
			start := time.Now()
			got := triageDuplicates(context.Background(), path, []string{"session stream disconnected", "terminal launch failure"})
			if !strings.Contains(got, tc.want) || strings.Count(got, "- issue #")+strings.Count(got, "- pr #") != tc.count || strings.Contains(got, "raw-secret") {
				t.Fatal(got)
			}
			if time.Since(start) > triageBudget {
				t.Fatal("search exceeded parallel cap")
			}
		})
	}
	t.Run("full results capped", func(t *testing.T) {
		type row struct {
			Number            int `json:"number"`
			Title, State, URL string
		}
		var rows []row
		for i := 1; i <= 5; i++ {
			rows = append(rows, row{i, "Candidate", "CLOSED", fmt.Sprintf("https://github.com/%s/issues/%d", triageRepo, i)})
		}
		raw, _ := json.Marshal(rows)
		for i := range rows {
			rows[i].Number += 5
			rows[i].URL = fmt.Sprintf("https://github.com/%s/issues/%d", triageRepo, rows[i].Number)
		}
		more, _ := json.Marshal(rows)
		path := fakeTriageGH(t, "case \"$*\" in *\"first query words\"*) printf '%s' '"+string(raw)+"';; *) printf '%s' '"+string(more)+"';; esac")
		got := triageDuplicates(context.Background(), path, []string{"first query words", "second query words", "third query words"})
		if strings.Count(got, "- issue #") != 5 || strings.Count(got, "- pr #") != 0 {
			t.Fatal(got)
		}
	})
	t.Run("missing gh", func(t *testing.T) {
		if got := triageDuplicates(context.Background(), "", []string{"daemon connection failed"}); !strings.Contains(got, "unavailable: gh missing") {
			t.Fatal(got)
		}
	})
}

func TestTriageRetention(t *testing.T) {
	parent, external := t.TempDir(), t.TempDir()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	old, fresh, boundary := now.Add(-8*24*time.Hour).Format(triageTimestamp), now.Format(triageTimestamp), now.Add(-7*24*time.Hour).Format(triageTimestamp)
	oldWithFreshMtime := now.Add(-20 * 24 * time.Hour).Format(triageTimestamp)
	freshWithOldMtime := now.Add(time.Hour).Format(triageTimestamp)
	for _, name := range []string{old, fresh, boundary, "unknown", oldWithFreshMtime, freshWithOldMtime} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, age := range map[string]time.Duration{old: -8 * 24 * time.Hour, fresh: 0, boundary: -7 * 24 * time.Hour, "unknown": -8 * 24 * time.Hour, oldWithFreshMtime: 0, freshWithOldMtime: -8 * 24 * time.Hour} {
		mtime := now.Add(age)
		if err := os.Chtimes(filepath.Join(parent, name), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	link := now.Add(-9 * 24 * time.Hour).Format(triageTimestamp)
	if err := os.Symlink(external, filepath.Join(parent, link)); err != nil {
		t.Skip(err)
	}
	file := now.Add(-10 * 24 * time.Hour).Format(triageTimestamp)
	if err := os.WriteFile(filepath.Join(parent, file), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneTriage(parent, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{old, freshWithOldMtime} {
		if _, err := os.Stat(filepath.Join(parent, name)); !os.IsNotExist(err) {
			t.Fatal("old folder remains", name, err)
		}
	}
	for _, path := range []string{fresh, boundary, "unknown", oldWithFreshMtime, link, file} {
		if _, err := os.Lstat(filepath.Join(parent, path)); err != nil {
			t.Fatal(path, err)
		}
	}
	if _, err := os.Stat(external); err != nil {
		t.Fatal("symlink target deleted", err)
	}
}

func TestTriageSearchUsesRedactedLogs(t *testing.T) {
	deps, _, dir := triageTestDeps(t)
	calls := filepath.Join(dir, "queries")
	t.Setenv("GH_TEST_CALLS", calls)
	path := fakeTriageGH(t, `printf '%s\n' "$*" >> "$GH_TEST_CALLS"; echo '[]'`)
	deps.LookPath = func(name string) (string, error) {
		if name == "gh" {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
	raw := "ERROR connection handshake failed {\"client_secret\":\"SENTINELVALUE\"}\nERROR pairing code 12345678\nERROR spawn timed out runtime handshake\n"
	if err := os.WriteFile(filepath.Join(dir, "daemon.log"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
	queryBytes, _ := os.ReadFile(calls)
	queries := string(queryBytes)
	for _, text := range []string{snapshot, draft, queries} {
		if strings.Contains(strings.ToLower(text), "sentinelvalue") || strings.Contains(text, "12345678") {
			t.Fatal(text)
		}
	}
	if strings.Contains(queries, "pr list") || strings.Contains(queries, "daemon stopped") || !strings.Contains(queries, "--search connection handshake failed") || !strings.Contains(snapshot, `keyword search: "spawn timed out runtime"; "connection handshake failed client"; candidates, not confirmed`) {
		t.Fatal(queries, snapshot)
	}
}

func TestTriageNoErrorSearch(t *testing.T) {
	if got := triageDuplicates(context.Background(), "unused", []string{"connection failed"}); got != "skipped: no error lines to search for\n" {
		t.Fatal(got)
	}
}
