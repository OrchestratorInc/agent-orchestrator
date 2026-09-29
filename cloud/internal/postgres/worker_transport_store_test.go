package postgres

import "testing"

// The read-only-session and viewer-role guards in CreateWorkspaceRequest /
// createWorkerRequest gate file mutations by kind. The review file-write path
// dispatches "workspace.review.write", so both write kinds must be recognized or
// a viewer / read-only member could overwrite files through the review endpoint.
func TestIsWorkspaceWriteKind(t *testing.T) {
	for _, kind := range []string{"workspace.write", "workspace.review.write"} {
		if !isWorkspaceWriteKind(kind) {
			t.Errorf("isWorkspaceWriteKind(%q) = false, want true (must be gated by viewer/read-only checks)", kind)
		}
	}
	for _, kind := range []string{
		"workspace.read", "workspace.list", "workspace.diff", "workspace.diff-file",
		"workspace.review", "workspace.review.file", "terminal.open", "browser.fetch",
	} {
		if isWorkspaceWriteKind(kind) {
			t.Errorf("isWorkspaceWriteKind(%q) = true, want false", kind)
		}
	}
}

// A viewer holds read-only access: the browser proxy forwards arbitrary HTTP
// methods to the sandbox dev server, so only safe fetches may pass.
func TestViewerForbiddenRequest(t *testing.T) {
	for _, test := range []struct {
		kind    string
		payload string
		want    bool
	}{
		{"workspace.write", `{}`, true},
		{"workspace.review.write", `{}`, true},
		{"workspace.read", `{}`, false},
		{"workspace.review.diffs", `{}`, false},
		{"browser.fetch", `{"method":"GET"}`, false},
		{"browser.fetch", `{"method":"head"}`, false},
		{"browser.fetch", `{}`, false},
		{"browser.fetch", `{"method":"POST"}`, true},
		{"browser.fetch", `{"method":"delete"}`, true},
		{"browser.fetch", `not json`, true},
	} {
		if got := viewerForbiddenRequest(test.kind, []byte(test.payload)); got != test.want {
			t.Errorf("viewerForbiddenRequest(%q, %s) = %v, want %v", test.kind, test.payload, got, test.want)
		}
	}
}
