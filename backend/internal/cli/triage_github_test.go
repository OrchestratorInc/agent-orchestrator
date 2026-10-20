package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
		t.Skip("shell fixture")
	}
	bin := t.TempDir()
	path := filepath.Join(bin, "gh")
	t.Setenv("GH_HOST", "other.example")
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
	for _, want := range []string{"status: draft\n", "Title: <one line", "Seen on " + Version, runtime.GOOS, "daemon stopped", "Related: none", triageEvidenceHeading, "health:", "claude: unavailable", "codex: unavailable", "Duplicate candidates", "daemon connection failed", "desktop connection failed", "hooks connection failed"} {
		if !strings.Contains(string(draft), want) {
			t.Errorf("missing %q in %s", want, draft)
		}
	}
	if strings.Contains(string(draft), "private") || strings.Count(string(draft), "- Log excerpt:") > 8 {
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
		{"candidate", `printf '[{"number":12,"title":"Stream breaks","state":"OPEN","url":"https://github.com/OrchestratorInc/agent-orchestrator/issues/12"}]'`, "  - issue #12 [OPEN] Stream breaks\n"},
		{"none", "printf '[]'", "  - none found\n"},
		{"unavailable", "exit 1", "  - unavailable: issue daemon stopped: search failed\n  - unavailable: pr daemon stopped: search failed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, dir := triageTestDeps(t)
			path := fakeTriageGH(t, tc.script)
			deps.LookPath = func(name string) (string, error) {
				if name == "gh" {
					return path, nil
				}
				return "", exec.ErrNotFound
			}
			snapshot, draft, _ := (&commandContext{deps: deps.withDefaults()}).triageSnapshot(context.Background(), dir, dir, filepath.Join(dir, "running.json"))
			_, evidence, _ := strings.Cut(draft, triageEvidenceHeading)
			_, candidates, ok := strings.Cut(evidence, "- Duplicate candidates checked by keyword search only, not confirmed:\n")
			if !ok || candidates != tc.want || strings.Contains(evidence, "## ") || !strings.Contains(snapshot, "## Possible duplicates") {
				t.Fatal(draft)
			}
		})
	}
}

func TestTriagePreflightFakeGH(t *testing.T) {
	for _, tc := range []struct{ name, auth, labels, wantAuth, wantLabel, filing string }{
		{"present", "exit 0", `printf '[{"name":"needs-triage"}]'`, "ok", "present", "available"},
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
		state      daemonState
		want       []string
	}{
		{"empty ready", "", stateReady, []string{}},
		{"stopped", "INFO all good", stateStopped, []string{"daemon stopped"}},
		{"plain strips metadata", "2026-10-20 ERROR session_id=abc123 /Users/name/file 503 connection refused by remote peer", stateReady, []string{"connection refused remote peer"}},
		{"slog", `time=2026 level=WARN msg="terminal launch failed" id=42`, stateReady, []string{"terminal launch failed"}},
		{"json", `{"level":"ERROR","msg":"session stream disconnected","id":123}`, stateReady, []string{"session stream disconnected"}},
		{"recent distinct", "ERROR old connection refused\nWARN terminal launch failed\nWARN terminal launch failed", stateUnhealthy, []string{"terminal launch failed", "old connection refused", "daemon unhealthy"}},
		{"limit", "ERROR first signal failed\nERROR second signal failed\nERROR third signal failed\nERROR fourth signal failed", stateReady, []string{"fourth signal failed", "third signal failed", "second signal failed"}},
		{"redacted before search", redactTriage("ERROR API_TOKEN=private path /home/person/file connection refused 123", "/home/person"), stateReady, []string{"path connection refused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := triageKeywords(tc.logs, tc.state); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestTriageDuplicateSearchFakeGH(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		count              int
	}{
		{"results deduped", `printf '[{"number":12,"title":"Stream breaks","state":"OPEN","url":"https://github.com/OrchestratorInc/agent-orchestrator/issues/12"}]'`, "- issue #12 [OPEN] Stream breaks", 1},
		{"none", "printf '[]'", "none found for: stream disconnected; terminal failure", 0},
		{"issue timeout", `if [ "$1" = issue ]; then exec /bin/sleep 10; fi; printf '[]'`, "unavailable: issue stream disconnected: timeout", 0},
		{"pr timeout", `if [ "$1" = pr ]; then exec /bin/sleep 10; fi; printf '[]'`, "unavailable: pr stream disconnected: timeout", 0},
		{"failed", "echo raw-secret; exit 1", "search failed", 0},
		{"malformed", "printf '[broken'", "search failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fakeTriageGH(t, tc.script)
			start := time.Now()
			got := triageDuplicates(context.Background(), path, []string{"stream disconnected", "terminal failure"})
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
		prs := strings.ReplaceAll(string(raw), "/issues/", "/pull/")
		path := fakeTriageGH(t, "if [ \"$1\" = issue ]; then printf '%s' '"+string(raw)+"'; else printf '%s' '"+prs+"'; fi")
		got := triageDuplicates(context.Background(), path, []string{"first query", "second query", "third query"})
		if strings.Count(got, "- issue #") != 5 || strings.Count(got, "- pr #") != 5 {
			t.Fatal(got)
		}
	})
	t.Run("missing gh", func(t *testing.T) {
		if got := triageDuplicates(context.Background(), "", []string{"daemon stopped"}); !strings.Contains(got, "unavailable: gh missing") {
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
