package nativetransfer

import (
	"encoding/json"
	"testing"
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
