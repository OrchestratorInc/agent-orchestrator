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
