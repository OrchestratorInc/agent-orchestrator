package nativetransfer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReplacePathPrefix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/w/web-3", "/b/web-9"},
		{"/w/web-3/a.go", "/b/web-9/a.go"},
		{"cd /w/web-3 && ls /w/web-3/src", "cd /b/web-9 && ls /b/web-9/src"},
		{"file:///w/web-3/a.go", "file:///b/web-9/a.go"},
		{"/w/web-30/a.go", "/w/web-30/a.go"},
		{"/w/web-3.bak", "/w/web-3.bak"},
		{"/x/w/web-3/a.go", "/x/w/web-3/a.go"},
		{"no path here", "no path here"},
	} {
		got, changed := replacePathPrefix(tc.in, "/w/web-3", "/b/web-9")
		if got != tc.want || changed != (tc.in != tc.want) {
			t.Errorf("replacePathPrefix(%q) = %q, %v; want %q", tc.in, got, changed, tc.want)
		}
	}
}

func TestRewriteLineFollowsTheSourcePathFormat(t *testing.T) {
	const winA, winB, posix = `C:\Users\a\w\web-3`, `D:\b\web-9`, "/b/web-9"
	for _, tc := range []struct{ from, to, in, want string }{
		{winA, winB, winA, winB},
		{winA, winB, winA + `\src\a.go`, winB + `\src\a.go`},
		{winA, winB, "C:/Users/a/w/web-3/src/a.go", "D:/b/web-9/src/a.go"},
		{winA, winB, winA + `0\a.go`, winA + `0\a.go`},
		{winA, posix, "cd " + winA + `\src\a.go && type ` + `C:\tools\x`, "cd /b/web-9/src/a.go && type " + `C:\tools\x`},
		{winA, posix, "C:/Users/a/w/web-3/a.go", "/b/web-9/a.go"},
		{"/w/web-3", winB, "/w/web-3/src/a.go", winB + `\src\a.go`},
		{`\\server\share\web-3`, winB, `\\server\share\web-3\a.go`, winB + `\a.go`},
		{`/w/odd\name`, posix, `/w/odd\name/a.go and /w/odd/name`, "/b/web-9/a.go and /w/odd/name"},
	} {
		m, err := newPathMap(map[string]string{tc.from: tc.to})
		if err != nil {
			t.Fatalf("newPathMap(%q): %v", tc.from, err)
		}
		line, _ := json.Marshal(map[string]string{"p": tc.in})
		out, _, err := m.rewriteLine(line)
		var got map[string]string
		if err != nil || json.Unmarshal(out, &got) != nil || got["p"] != tc.want {
			t.Errorf("%q to %q: rewrote %q to %q (%v), want %q", tc.from, tc.to, tc.in, got["p"], err, tc.want)
		}
	}
	for _, root := range []string{`C:\`, "C:/", "/", "relative/path", `web-3\src`, "C:"} {
		if _, err := newPathMap(map[string]string{root: "/b"}); err == nil {
			t.Errorf("prefix %q accepted", root)
		}
	}
}

func TestRewriteLineLeavesUnmentionedRecordsAlone(t *testing.T) {
	m, err := newPathMap(map[string]string{"/w/web-3": "/b/web-9"})
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(`{"z":1,"a":"é unrelated"}`)
	got, changed, err := m.rewriteLine(line)
	if err != nil || changed || string(got) != string(line) {
		t.Fatalf("got %s, %v, %v", got, changed, err)
	}
	if _, err := newPathMap(map[string]string{"/": "/b"}); err == nil {
		t.Fatal("root prefix accepted")
	}
}

// A replacement is never matched again by another pair: each pair rewrites
// the original text, in one pass.
func TestRewriteStringDoesNotChainReplacements(t *testing.T) {
	m, err := newPathMap(map[string]string{"/w/a": "/w/b", "/w/b": "/w/c"})
	if err != nil {
		t.Fatal(err)
	}
	got, changed := m.rewriteString("/w/a/x and /w/b/y and /w/ab")
	if want := "/w/b/x and /w/c/y and /w/ab"; !changed || got != want {
		t.Fatalf("rewriteString = %q, %v; want %q", got, changed, want)
	}
	// Nested prefixes: the longer, more specific pair still wins.
	m, err = newPathMap(map[string]string{"/w": "/x", "/w/app": "/y/app"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.rewriteString("/w/app/a.go /w/b.go"); got != "/y/app/a.go /x/b.go" {
		t.Fatalf("nested = %q", got)
	}
}

// OpenCode's reasoning parts are signed by the model provider, which checks
// the text when the conversation resumes: a path inside one must not change,
// while the same path in any other part is rewritten.
func TestRewriteOpenCodeExportLeavesSignedReasoningAlone(t *testing.T) {
	const dir = "/Users/me/ao/worktrees/app-3"
	reasoning := "I will edit " + dir + "/main.go next."
	doc := `{"info":{"id":"ses_x","location":{"directory":"` + dir + `"}},"messages":[{"parts":[` +
		`{"type":"reasoning","text":"` + reasoning + `","metadata":{"anthropic":{"signature":"sig"}}},` +
		`{"type":"text","text":"thinking about ` + dir + `","metadata":{"anthropic":{"signature":"sig2"}}},` +
		`{"type":"tool","state":{"input":{"filePath":"` + dir + `/main.go"}}}]}]}`
	out, err := RewriteOpenCodeExport([]byte(doc), CloudCwdPlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Info struct {
			Location struct{ Directory string } `json:"location"`
		} `json:"info"`
		Messages []struct {
			Parts []map[string]any `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	parts := got.Messages[0].Parts
	if parts[0]["text"] != reasoning {
		t.Errorf("reasoning text = %q, want it unchanged", parts[0]["text"])
	}
	if parts[1]["text"] != "thinking about "+dir {
		t.Errorf("signed text part = %q, want it unchanged", parts[1]["text"])
	}
	input := parts[2]["state"].(map[string]any)["input"].(map[string]any)
	if input["filePath"] != CloudCwdPlaceholder+"/main.go" {
		t.Errorf("tool path = %q, want it rewritten", input["filePath"])
	}
	if got.Info.Location.Directory != CloudCwdPlaceholder {
		t.Errorf("directory = %q, want it rewritten", got.Info.Location.Directory)
	}
}

// A long string that lists many paths for one prefix and none for another
// (a tool result listing the workspace, with no transcript-folder path) is
// rewritten in linear time: the absent prefix is not searched for again after
// every replacement.
func TestRewriteStringIsLinearInTheListedPaths(t *testing.T) {
	m, err := newPathMap(map[string]string{
		"/Users/me/ao/worktrees/app-3":                            "/ao-moved/cwd",
		"/Users/me/.claude/projects/-Users-me-ao-worktrees-app-3": "/ao-moved/claude-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	const n = 200_000
	s := strings.Repeat("/Users/me/ao/worktrees/app-3/a.go\n", n)
	start := time.Now()
	got, changed := m.rewriteString(s)
	elapsed := time.Since(start)
	if !changed || strings.Count(got, "/ao-moved/cwd/a.go") != n {
		t.Fatalf("rewrote %d of %d paths", strings.Count(got, "/ao-moved/cwd/a.go"), n)
	}
	// Linear takes milliseconds; rescanning the rest of the string for the
	// absent prefix after each of the n replacements takes minutes.
	if elapsed > 5*time.Second {
		t.Fatalf("rewriting %d listed paths took %v", n, elapsed)
	}
}
