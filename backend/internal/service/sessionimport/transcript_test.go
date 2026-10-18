package sessionimport

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const codexThread = "01a10364-aebe-72d1-9f77-5b8d924c4fbd"

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func texts(messages []Message) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, string(m.Role)+": "+m.Text)
	}
	return out
}

func wantTexts(t *testing.T, got []Message, want ...string) {
	t.Helper()
	if g := texts(got); strings.Join(g, "|") != strings.Join(want, "|") {
		t.Fatalf("messages =\n  %q\nwant\n  %q", g, want)
	}
}

// Shapes below are trimmed from real Codex Desktop rollouts.
func TestCodexDesktopRolloutShowsOnlyTypedPrompts(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "sessions", "2026", "10", "08", "rollout-2026-10-08T04-09-28-"+codexThread+".jsonl"), `{"timestamp":"2026-10-07T22:39:28Z","type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"/work/repo","source":"vscode"}}
{"timestamp":"2026-10-07T22:39:33Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/repo\n\n<INSTRUCTIONS>be terse</INSTRUCTIONS>"}]}}
{"timestamp":"2026-10-07T22:39:33Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/work/repo</cwd>\n</environment_context>"}]}}
{"timestamp":"2026-10-07T22:39:33Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"PONYTAIL MODE ACTIVE"}]}}
{"timestamp":"2026-10-07T22:39:34Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"\n<in-app-browser-context source=\"ambient-ui-state\">\n# In app browser:\n- Current URL: https://example.test\n</in-app-browser-context>\n\n## My request:\noverall status of session import?\n"}]}}
{"timestamp":"2026-10-07T22:39:34Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"\n<in-app-browser-context source=\"ambient-ui-state\">\n- Current URL: https://example.test\n</in-app-browser-context>\n\n## My request:\noverall status of session import?\n"}]}}}
{"timestamp":"2026-10-07T22:39:40Z","type":"response_item","payload":{"type":"function_call","name":"exec","arguments":"{}"}}
{"timestamp":"2026-10-07T22:39:51Z","type":"response_item","payload":{"type":"message","role":"assistant","id":"msg_1","content":[{"type":"output_text","text":"Implemented and tested; not merged."}]}}
{"timestamp":"2026-10-07T22:39:51Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"msg_1","content":[{"type":"Text","text":"Implemented and tested; not merged."}]}}}
{"timestamp":"2026-10-08T13:36:25Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u2","content":[{"type":"text","text":"\n# Files mentioned by the user:\n\n## shot.png: /tmp/shot.png\nImage attachment: true\n\n## My request:\nFix the conflicts.\n"}]}}}
{"timestamp":"2026-10-08T13:59:15Z","type":"response_item","payload":{"type":"message","role":"assistant","id":"msg_2","content":[{"type":"output_text","text":"Fixed and pushed."}]}}
`)
	got, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: overall status of session import?", "assistant: Implemented and tested; not merged.", "user: Fix the conflicts.", "assistant: Fixed and pushed.")
	if got.title != "overall status of session import?" || got.cwd != "/work/repo" || got.nativeID != codexThread {
		t.Fatalf("title=%q cwd=%q id=%q", got.title, got.cwd, got.nativeID)
	}
}

func TestCodexResponseOnlyRolloutDropsInjectedContext(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-09-01T00-00-00-"+codexThread+".jsonl"), `{"type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"/work/repo","source":"cli"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/repo"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<external_codex_apps_open_page>{\"page_id\":null}</external_codex_apps_open_page>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>review</name>\n</skill>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"why does <environment_context> appear in logs?"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Because Codex injects it."}]}}
`)
	got, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: why does <environment_context> appear in logs?", "assistant: Because Codex injects it.")
}

func TestCodexRolloutIdentity(t *testing.T) {
	meta := func(id, source string) string {
		return `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/work/repo","source":` + source + `}}` + "\n"
	}
	prompt := `{"type":"event_msg","payload":{"type":"user_message","message":"hello"}}` + "\n"
	ancestor := "01a00000-0000-7000-8000-000000000000"
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"fork keeps its own id":       {meta(codexThread, `"vscode"`) + meta(ancestor, `"vscode"`) + prompt, true},
		"spawned subagent is hidden":  {meta(codexThread, `{"subagent":{"thread_spawn":{"parent_thread_id":"`+ancestor+`"}}}`) + prompt, false},
		"guardian subagent is hidden": {meta(codexThread, `{"subagent":{"other":"guardian"}}`) + prompt, false},
		"foreign id is rejected":      {meta(ancestor, `"cli"`) + prompt, false},
		"no metadata is rejected":     {prompt, false},
		"no prompt is rejected":       {meta(codexThread, `"cli"`), false},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-10-01T00-00-00-"+codexThread+".jsonl"), tc.body)
			got, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if tc.ok && got.nativeID != codexThread {
				t.Fatalf("id = %q", got.nativeID)
			}
		})
	}
}

func zstdFile(t *testing.T, path, body string) string {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	return writeFile(t, path, string(enc.EncodeAll([]byte(body), nil)))
}

// Codex compresses inactive rollouts and continues them in a `_<segment>` file.
func TestCodexCompressedAndSegmentedThread(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "10")
	base := zstdFile(t, filepath.Join(day, "04", "rollout-2026-10-04T01-41-32-"+codexThread+".jsonl.zst"), `{"type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"/work/repo","source":"vscode"}}
{"type":"event_msg","payload":{"type":"user_message","message":"first prompt"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}}
`)
	segment := writeFile(t, filepath.Join(day, "08", "rollout-2026-10-08T04-09-28-"+codexThread+"_01a11885-8f9f-76d1-a6d0-3b60c45dc3a5.jsonl"), `{"type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"/work/repo","source":"vscode"}}
{"type":"event_msg","payload":{"type":"user_message","message":"second prompt"}}
`)
	svc := New(&importStore{}, "")
	files, err := svc.discover(context.Background(), domain.HarnessCodex, canonical(root))
	if err != nil || len(files) != 1 || len(files[0].paths) != 2 {
		t.Fatalf("files = %#v, %v", files, err)
	}
	if !strings.HasSuffix(files[0].paths[0], filepath.Base(base)) || !strings.HasSuffix(files[0].paths[1], filepath.Base(segment)) {
		t.Fatalf("segment order = %q", files[0].paths)
	}
	got, err := readTranscript(context.Background(), domain.HarnessCodex, canonical(root), files[0].paths...)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: first prompt", "assistant: first answer", "user: second prompt")
	if !strings.HasSuffix(got.path, filepath.Base(segment)) {
		t.Fatalf("live path = %q", got.path)
	}
	// While Codex compresses a segment both copies exist; the plain file is current.
	zstdFile(t, segment+".zst", "corrupt")
	files, err = svc.discover(context.Background(), domain.HarnessCodex, canonical(root))
	if err != nil || len(files) != 1 || len(files[0].paths) != 2 || strings.HasSuffix(files[0].paths[1], ".zst") {
		t.Fatalf("twin files = %#v, %v", files, err)
	}
	writeFile(t, filepath.Join(root, "archived_sessions", "rollout-2026-01-01T00-00-00-01a99999-0000-7000-8000-000000000000.jsonl"), "{}")
	if files, _ = svc.discover(context.Background(), domain.HarnessCodex, canonical(root)); len(files) != 1 {
		t.Fatalf("archived Codex threads are not resumable: %#v", files)
	}
}

func TestClaudeTranscriptShapes(t *testing.T) {
	id := "6d842f77-b1fe-49f3-8585-8946df267bae"
	big := strings.Repeat("x", 4<<20) // parsed, never retained
	body := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + id + `","cwd":"/work/repo","message":{"role":"user","content":"start the refactor"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","cwd":"/work/repo","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"hidden"}]}}
{"type":"assistant","uuid":"a1b","parentUuid":"a1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"Working on it."}]}}
{"type":"assistant","uuid":"a1c","parentUuid":"a1b","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}
{"type":"user","uuid":"tool","parentUuid":"a1c","message":{"role":"user","content":[{"type":"tool_result","content":"` + big + `"}]}}
{"type":"user","uuid":"meta","parentUuid":"tool","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /skills/review"}]}}
{"type":"system","subtype":"compact_boundary","uuid":"boundary","parentUuid":null,"logicalParentUuid":"meta","cwd":"/tmp/elsewhere"}
{"type":"user","uuid":"summary","parentUuid":"boundary","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context."}}
{"type":"user","uuid":"cmd","parentUuid":"summary","message":{"role":"user","content":"<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>PR 12</command-args>"}}
{"type":"user","uuid":"out","parentUuid":"cmd","message":{"role":"user","content":"<local-command-stdout>Reviewing</local-command-stdout>"}}
{"type":"assistant","uuid":"a2","parentUuid":"out","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"Review posted."}]}}
{"type":"ai-title","aiTitle":"Refactor and review","sessionId":"` + id + `"}
{"type":"user","uuid":"torn","parentUuid":"a2","message":{"role":"user","content":"half writ`
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "projects", "-work-repo", id+".jsonl"), body)
	got, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: start the refactor", "assistant: Working on it.", "user: /review PR 12", "assistant: Review posted.")
	if got.title != "Refactor and review" || got.cwd != "/work/repo" || got.shortened {
		t.Fatalf("title=%q cwd=%q shortened=%v", got.title, got.cwd, got.shortened)
	}
	custom := writeFile(t, path, body[:strings.LastIndex(body, "\n")]+"\n"+`{"type":"custom-title","customTitle":"My rename","sessionId":"`+id+`"}`+"\n")
	if got, err = readTranscript(context.Background(), domain.HarnessClaudeCode, root, custom); err != nil || got.title != "My rename" {
		t.Fatalf("custom title = %q, %v", got.title, err)
	}
	login := writeFile(t, filepath.Join(root, "projects", "-work-repo", "7e842f77-b1fe-49f3-8585-8946df267bae.jsonl"), `{"type":"user","uuid":"c","message":{"role":"user","content":"<command-name>/login</command-name>\n<command-message>login</command-message>\n<command-args></command-args>"}}
{"type":"user","uuid":"o","parentUuid":"c","message":{"role":"user","content":"<local-command-stdout>Login successful</local-command-stdout>"}}
`)
	if _, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, login); err == nil {
		t.Fatal("a /login-only session is not a conversation")
	}
}

func TestOversizedClaudeRecordKeepsHistoryAndReportsLostText(t *testing.T) {
	limits := [2]int{maxRecordBytes, maxSkimString}
	maxRecordBytes, maxSkimString = 1<<10, 256
	t.Cleanup(func() { maxRecordBytes, maxSkimString = limits[0], limits[1] })
	id := "6d842f77-b1fe-49f3-8585-8946df267bae"
	// Claude writes uuid after the tool output; quotes and braces inside it are not structure.
	big := strings.Repeat(`x\"}{[`, 200)
	parts := strings.TrimSuffix(strings.Repeat(`{"type":"text","text":"`+strings.Repeat("m", 200)+`"},`, 8), ",")
	for _, tc := range []struct {
		name, record string
		want         []string
		shortened    bool
	}{
		{"tool output beside text", `{"parentUuid":"a1","type":"user","sessionId":"` + id + `","message":{"role":"user","content":[{"type":"text","text":"note beside output"},{"type":"tool_result","content":"` + big + `"}]},"toolUseResult":{"uuid":"nested"},"uuid":"mid"}`, []string{"user: note beside output"}, false},
		{"text too large", `{"parentUuid":"a1","type":"assistant","message":{"id":"m9","role":"assistant","content":[{"type":"text","text":"` + big + `"}]},"uuid":"mid"}`, nil, true},
		{"too many parts", `{"parentUuid":"a1","type":"assistant","message":{"id":"m9","role":"assistant","content":[` + parts + `]},"uuid":"mid"}`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + id + `","message":{"role":"user","content":"first question"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"first answer"}]}}
` + tc.record + `
{"type":"user","uuid":"u2","parentUuid":"mid","message":{"role":"user","content":"second question"}}
{"type":"assistant","uuid":"a2","parentUuid":"u2","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"second answer"}]}}
`
			root := t.TempDir()
			path := writeFile(t, filepath.Join(root, "projects", "-work-repo", id+".jsonl"), body)
			got, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, path)
			if err != nil {
				t.Fatal(err)
			}
			want := append(append([]string{"user: first question", "assistant: first answer"}, tc.want...), "user: second question", "assistant: second answer")
			wantTexts(t, got.messages, want...)
			if got.shortened != tc.shortened {
				t.Fatalf("shortened = %v", got.shortened)
			}
		})
	}
}

func TestRecordSkim(t *testing.T) {
	limits := [2]int{maxRecordBytes, maxSkimString}
	maxRecordBytes, maxSkimString = 1<<10, 256
	t.Cleanup(func() { maxRecordBytes, maxSkimString = limits[0], limits[1] })
	var skim recordSkim
	skim.reset()
	skim.write([]byte(`{"a":"x\"}","b":{"uuid":"\\"},"long":"` + strings.Repeat(`y\"`, 100) + `","e":true, "f" : null,"uuid":"yes"`))
	if skim.record() != nil {
		t.Fatal("a torn record was kept")
	}
	skim.write([]byte("}\n{}"))
	if got := string(skim.record()); got != `{"a":"x\"}","b":{"uuid":"\\"},"long":"\u0000omitted\u0000","e":true, "f" : null,"uuid":"yes"}` {
		t.Fatalf("rewritten record = %s", got)
	}
	skim.reset()
	skim.write([]byte(`{"uuid":"yes","p":[` + strings.TrimSuffix(strings.Repeat(`"`+strings.Repeat("m", 200)+`",`, 8), ",") + `],"type":"user"}`))
	if got := string(skim.record()); got != `{"uuid":"yes","type":"user","aoOmitted":true}` {
		t.Fatalf("overflowed record = %s", got)
	}
}

func TestVisibleBudgetKeepsOpeningPromptAndNewestMessages(t *testing.T) {
	limit := maxVisibleBytes
	chunk := strings.Repeat("y", limit/4)
	var lines []string
	lines = append(lines, `{"type":"session_meta","payload":{"id":"`+codexThread+`","cwd":"","source":"cli"}}`)
	lines = append(lines, `{"type":"event_msg","payload":{"type":"user_message","message":"opening prompt"}}`)
	for i := range 8 {
		lines = append(lines, `{"type":"event_msg","payload":{"type":"agent_message","message":"`+string(rune('a'+i))+chunk+`"}}`)
	}
	root := t.TempDir()
	path := writeFile(t, filepath.Join(root, "sessions", "rollout-2026-10-01T00-00-00-"+codexThread+".jsonl"), strings.Join(lines, "\n")+"\n")
	got, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.messages) < 2 || got.messages[0].Text != "opening prompt" || got.messages[len(got.messages)-1].Text[0] != 'h' {
		t.Fatalf("kept %d messages", len(got.messages))
	}
	total := 0
	for _, m := range got.messages {
		total += len(m.Text)
	}
	if total > limit || !got.shortened {
		t.Fatalf("visible bytes = %d, shortened = %v", total, got.shortened)
	}
}

func TestReadIgnoresRecordsAppendedDuringRead(t *testing.T) {
	root := t.TempDir()
	id := "6d842f77-b1fe-49f3-8585-8946df267bae"
	path := writeFile(t, filepath.Join(root, "projects", "p", id+".jsonl"), `{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`+"\n")
	providerRoot, err := os.OpenRoot(canonical(root))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = providerRoot.Close() }()
	appended := false
	err = readOne(providerRoot, canonical(root), path, func([]byte) error {
		if !appended {
			appended = true
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			_, _ = f.WriteString(`{"type":"user","uuid":"u2","parentUuid":"u1","message":{"role":"user","content":"more"}}` + "\n")
			return f.Close()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("growth while reading: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = readOne(providerRoot, canonical(root), path, func([]byte) error {
		return os.Truncate(path, 0)
	})
	if err == nil {
		t.Fatal("a transcript that shrank while reading was accepted")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestScanDestinations(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	repo := filepath.Join(home, "repo")
	runGit(t, home, "init", "-q", "-b", "main", repo)
	runGit(t, repo, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	runGit(t, repo, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "init")
	linked := filepath.Join(home, "linked")
	runGit(t, repo, "worktree", "add", "-q", "-b", "side", linked)
	clone := filepath.Join(home, "clone")
	runGit(t, home, "init", "-q", clone)
	runGit(t, clone, "remote", "add", "origin", "https://github.com/acme/widgets")
	other := filepath.Join(home, "other")
	runGit(t, home, "init", "-q", other)
	codexChat := filepath.Join(home, "Documents", "Codex", "2026-10-07", "wh")
	managed := filepath.Join(data, "worktrees", "repo", "repo-1")
	for _, dir := range []string{codexChat, managed, filepath.Join(repo, "pkg")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(&importStore{projects: []domain.ProjectRecord{{ID: "widgets", Path: repo}}}, data)
	svc.home = canonical(home)
	dests, err := svc.destinations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for cwd, want := range map[string]struct {
		id       domain.ProjectID
		eligible bool
	}{
		"":                            {"", true},
		repo:                          {"widgets", true},
		filepath.Join(repo, "pkg"):    {"widgets", true},
		linked:                        {"widgets", true},
		clone:                         {"widgets", true},
		other:                         {"", false},
		home:                          {"", true},
		codexChat:                     {"", true},
		managed:                       {"", false},
		filepath.Join(home, "gone"):   {"", false},
		filepath.Join(repo, "absent"): {"", false},
	} {
		if id, eligible := dests.resolve(cwd); id != want.id || eligible != want.eligible {
			t.Errorf("resolve(%q) = %q,%v want %q,%v", cwd, id, eligible, want.id, want.eligible)
		}
	}
	if !SameRepository(context.Background(), linked, repo) || !SameRepository(context.Background(), clone, repo) || SameRepository(context.Background(), other, repo) {
		t.Fatal("SameRepository")
	}
}

// Every readable conversation is listed; only the recent window is preselected.
func TestScanListsOlderConversations(t *testing.T) {
	claude := t.TempDir()
	now := time.Now()
	for i, name := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"} {
		at := now.Add(time.Duration(-i) * 24 * time.Hour)
		if i == 2 {
			at = now.AddDate(0, 0, -recentDays-1)
		}
		path := writeClaude(t, claude, name, "", "claude "+name[:1], at)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(&importStore{}, "")
	svc.roots = map[domain.AgentHarness]string{domain.HarnessClaudeCode: claude}
	preview, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, c := range preview.Candidates {
		titles = append(titles, fmt.Sprintf("%s %v", c.Title, c.Suggested))
	}
	sort.Strings(titles)
	if strings.Join(titles, ",") != "claude 1 true,claude 2 true,claude 3 false" {
		t.Fatalf("titles=%q", titles)
	}
}
