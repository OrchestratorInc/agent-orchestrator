package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTriagePostFakeGH(t *testing.T) {
	old := stdinIsInteractive
	stdinIsInteractive = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsInteractive = old })
	for _, tc := range []struct {
		name, state, related, input, label, auth, fail, want string
		tty, missing, wantErr, crlf                          bool
		calls                                                int
	}{
		{name: "yes", state: "ready", input: "y\n", label: "present", auth: "ok", tty: true, want: "filed", calls: 2},
		{name: "yes no label", state: "ready", input: "yes\n", label: "absent", auth: "ok", tty: true, want: "filed", calls: 2},
		{name: "no", state: "ready", input: "N\n", label: "present", auth: "ok", tty: true, want: "skipped"},
		{name: "empty answer", state: "ready", input: "\n", auth: "ok", tty: true, want: "skipped"},
		{name: "non TTY", state: "ready", input: "y\n", auth: "ok", want: "skipped"},
		{name: "draft", state: "draft", input: "y\n", auth: "ok", tty: true, want: "draft"},
		{name: "missing", missing: true, input: "y\n", auth: "ok", tty: true, want: "draft"},
		{name: "duplicate", state: "duplicate", related: "#12", input: "y\n", label: "present", auth: "ok", tty: true, want: "commented", calls: 3},
		{name: "CRLF ready", state: "ready", input: "y\n", auth: "ok", tty: true, crlf: true, want: "filed", calls: 2},
		{name: "CRLF duplicate", state: "duplicate", related: "#12", input: "y\n", auth: "ok", tty: true, crlf: true, want: "commented", calls: 3},
		{name: "duplicate declined", state: "duplicate", related: "#12", input: "n\n", auth: "ok", tty: true, want: "skipped", calls: 1},
		{name: "duplicate missing issue", state: "duplicate", related: "#12", input: "y\n", auth: "ok", tty: true, fail: "view", want: "skipped", wantErr: true, calls: 1},
		{name: "duplicate invalid issue URL", state: "duplicate", related: "#12", input: "y\n", auth: "ok", tty: true, fail: "view-url", want: "skipped", wantErr: true, calls: 1},
		{name: "duplicate without Related", state: "duplicate", input: "y\n", auth: "ok", tty: true, want: "skipped", wantErr: true},
		{name: "auth failed", state: "ready", input: "y\n", auth: "failed", tty: true, want: "skipped"},
		{name: "ambiguous create", state: "ready", input: "y\n", auth: "ok", tty: true, fail: "create", want: "draft", wantErr: true, calls: 1},
		{name: "malformed create URL", state: "ready", input: "y\n", auth: "ok", tty: true, fail: "url", want: "draft", wantErr: true, calls: 1},
		{name: "evidence comment failed", state: "ready", input: "y\n", auth: "ok", tty: true, fail: "comment", want: "filed", wantErr: true, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			callsPath := filepath.Join(dir, "calls")
			t.Setenv("GH_TEST_CALLS", callsPath)
			t.Setenv("GH_TEST_FAIL", tc.fail)
			path := fakeTriageGH(t, `printf '%s\n' "$*" >> "$GH_TEST_CALLS"
if [ "$GH_TEST_FAIL" = "$2" ]; then exit 1; fi
if [ "$2" = view ]; then
  if [ "$GH_TEST_FAIL" = view-url ]; then echo '{}'; else printf '{"url":"https://github.com/OrchestratorInc/agent-orchestrator/issues/12"}'; fi
  exit 0
fi
if [ "$GH_TEST_FAIL" = url ]; then echo unexpected-output; exit 0; fi
if [ "$2" = create ]; then echo https://github.com/OrchestratorInc/agent-orchestrator/issues/12
else echo https://github.com/OrchestratorInc/agent-orchestrator/issues/12#issuecomment-99; fi
`)
			human := "The view closed. It should have stayed open.\n\nSeen on dev, test OS, daemon stopped. Related: " + tc.related + "."
			comment := "The view closed again today. I saw it after opening a session."
			evidence := triageEvidenceHeading + " (posted as a separate comment; do not edit)\n- App: dev\n"
			report := "status: " + tc.state + "\n\nTitle: View closes\n\n" + human + "\n\n## Comment (fill only if this is a duplicate of an existing issue)\n" + comment + "\n\n" + evidence
			if tc.crlf {
				report = strings.ReplaceAll(report, "\n", "\r\n")
			}
			if !tc.missing {
				if err := os.WriteFile(filepath.Join(dir, "bug-report.md"), []byte(report), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err := finishTriage(context.Background(), strings.NewReader(tc.input), &out, dir, triageGitHub{path, tc.auth, tc.label}, tc.tty)
			if (err != nil) != tc.wantErr {
				t.Fatal(err)
			}
			postErr := err
			raw, err := os.ReadFile(filepath.Join(dir, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]string
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result["status"] != tc.want {
				t.Fatal(string(raw))
			}
			calls, _ := os.ReadFile(callsPath)
			if strings.Count(string(calls), "\n") != tc.calls {
				t.Fatalf("calls: %s", calls)
			}
			if tc.calls == 0 && !tc.wantErr && !strings.Contains(out.String(), "not posted") {
				t.Fatal(out.String())
			}
			if !tc.tty && strings.Contains(out.String(), "Post this") {
				t.Fatal("non-TTY prompt", out.String())
			}
			if tc.state == "duplicate" && tc.related != "" {
				if !strings.HasPrefix(string(calls), "issue view 12 --repo "+triageRepo+" --json url\n") {
					t.Fatal(string(calls))
				}
				if tc.wantErr {
					if !strings.Contains(postErr.Error(), "could not verify duplicate issue") || strings.Contains(out.String(), "Post this") {
						t.Fatal(err, out.String())
					}
				} else {
					preview, _, _ := strings.Cut(out.String(), "Post this")
					if !strings.Contains(preview, "Comment on https://github.com/"+triageRepo+"/issues/12") || !strings.Contains(preview, comment) || !strings.Contains(preview, evidence) || strings.Contains(preview, "Label:") {
						t.Fatal(preview)
					}
				}
			}
			posts := strings.Count(string(calls), "--body-file")
			if posts > 0 {
				if (tc.state == "ready" && !strings.Contains(out.String(), "Title: View closes\n")) || strings.Count(out.String(), "Post this to GitHub? [y/N] ") != 1 {
					t.Fatal(out.String())
				}
				body, err := os.ReadFile(filepath.Join(dir, "post-body.md"))
				if err != nil {
					t.Fatal(err)
				}
				wantBody := human
				if tc.state == "duplicate" {
					wantBody = comment
				}
				if string(body) != wantBody || !strings.Contains(out.String(), string(body)) || strings.Contains(string(body), triageEvidenceHeading) || strings.Contains(string(body), "status:") {
					t.Fatalf("posted body %q, preview %s", body, out.String())
				}
				if tc.state == "ready" {
					if strings.Contains(string(calls), "--label needs-triage") != (tc.label == "present") {
						t.Fatal(string(calls))
					}
				}
				if posts == 2 {
					postedEvidence, _ := os.ReadFile(filepath.Join(dir, "post-evidence.md"))
					if string(postedEvidence) != evidence || !strings.Contains(out.String(), evidence) {
						t.Fatal(string(postedEvidence), out.String())
					}
				}
			}
			if tc.want == "filed" || tc.want == "commented" {
				if !strings.Contains(result["url"], "/issues/12") {
					t.Fatal(result)
				}
			}
		})
	}
}

func TestTriageDefaultModelAndAgentExit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "default model", true: "agent failure"}[fail], func(t *testing.T) {
			deps, _, state := triageTestDeps(t)
			deps.LookPath = func(name string) (string, error) {
				if name == "codex" {
					return "/fake/codex", nil
				}
				return "", exec.ErrNotFound
			}
			deps.RunInteractiveCommand = func(_ context.Context, _ string, dir string, args []string, _ io.Reader, _, _ io.Writer) error {
				if len(args) != 1 || args[0] != triagePrompt(dir, true) || strings.Contains(args[0], "--model") {
					t.Fatal(args)
				}
				if fail {
					return exec.Command("/bin/sh", "-c", "exit 7").Run()
				}
				return nil
			}
			cmd := NewRootCommand(deps)
			cmd.SetArgs([]string{"triage", "--agent", "codex"})
			err := cmd.Execute()
			if fail {
				if ExitCode(err) != 7 {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			result := filepath.Join(state, "triage", deps.Now().UTC().Format(triageTimestamp), "result.json")
			if fail {
				if _, err := os.Stat(result); !os.IsNotExist(err) {
					t.Fatal("agent failure reached posting", err)
				}
			}
		})
	}
}
