package nativetransfer

import "testing"

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
