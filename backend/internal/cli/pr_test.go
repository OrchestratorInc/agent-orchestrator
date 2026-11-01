package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const prMergeTestURL = "https://github.com/acme/widgets/pull/42"
const prMergeTestHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestPRMergePostsToDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"prNumber":42,"method":"squash"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "#42", "--url", "  "+prMergeTestURL+"  ", "--expected-head-sha", "  "+prMergeTestHeadSHA+"  ")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/prs/42/merge" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var request struct {
		PRURL           string `json:"prUrl"`
		ExpectedHeadSHA string `json:"expectedHeadSha"`
	}
	if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
		t.Fatalf("decode merge request: %v", err)
	}
	if request.PRURL != prMergeTestURL || request.ExpectedHeadSHA != prMergeTestHeadSHA {
		t.Fatalf("merge request = %#v, want the supplied PR URL and reviewed head SHA", request)
	}
	if !strings.Contains(out, "merged PR #42 using squash") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPRMergeOmitsMissingMethodFromOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, `{"ok":true,"prNumber":42}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "42", "--url", prMergeTestURL, "--expected-head-sha", prMergeTestHeadSHA)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if out != "merged PR #42\n" {
		t.Fatalf("stdout = %q, want %q", out, "merged PR #42\n")
	}
}

func TestPRMergeRejectsInvalidNumber(t *testing.T) {
	setConfigEnv(t)

	for _, number := range []string{"0", "-1", "abc", "#"} {
		t.Run(number, func(t *testing.T) {
			_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", number)
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2; err=%v", got, err)
			}
		})
	}
}

func TestPRMergeRequiresExactlyOneArgument(t *testing.T) {
	setConfigEnv(t)

	for _, args := range [][]string{
		{"pr", "merge"},
		{"pr", "merge", "42", "43"},
	} {
		_, _, err := executeCLI(t, aliveDeps(), args...)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args = %v, exit code = %d, want 2; err=%v", args, got, err)
		}
	}
}

func TestPRMergeRequiresURLAndReviewedHead(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "both missing"},
		{name: "head missing", args: []string{"--url", prMergeTestURL}},
		{name: "URL missing", args: []string{"--expected-head-sha", prMergeTestHeadSHA}},
		{name: "blank URL", args: []string{"--url", " \t", "--expected-head-sha", prMergeTestHeadSHA}},
		{name: "blank head", args: []string{"--url", prMergeTestURL, "--expected-head-sha", " \t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var mergeRequests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/prs/42/merge" {
					mergeRequests.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"prNumber":42}`))
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			args := append([]string{"pr", "merge", "42"}, tc.args...)
			out, _, err := executeCLI(t, aliveDeps(), args...)
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2; err=%v", got, err)
			}
			if mergeRequests.Load() != 0 || strings.Contains(out, "merged PR") {
				t.Fatalf("invalid invocation reached merge or reported success: merge requests=%d stdout=%q", mergeRequests.Load(), out)
			}
		})
	}
}

func TestPRMergeHelpExplainsRequiredFlags(t *testing.T) {
	setConfigEnv(t)
	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "--help")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"--url", "--expected-head-sha"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help = %q, want %q", out, want)
		}
	}
}

func TestPRMergeSurfacesDaemonError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		message string
		code    string
	}{
		{name: "not mergeable", status: http.StatusConflict, message: "PR is not mergeable", code: "PR_NOT_MERGEABLE"},
		{name: "changed head", status: http.StatusConflict, message: "PR head changed; refresh before merging", code: "PR_HEAD_CHANGED"},
		{name: "preconditions", status: http.StatusUnprocessableEntity, message: "PR merge preconditions are not met", code: "PR_PRECONDITIONS_UNMET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			body, err := json.Marshal(map[string]string{"message": tc.message, "code": tc.code, "requestId": "req-1"})
			if err != nil {
				t.Fatal(err)
			}
			srv, capture := reviewServer(t, tc.status, string(body))
			writeRunFileFor(t, cfg, srv)
			out, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42", "--url", prMergeTestURL, "--expected-head-sha", prMergeTestHeadSHA)
			if got := ExitCode(err); got != 1 {
				t.Fatalf("exit code = %d, want 1; err=%v", got, err)
			}
			for _, want := range []string{tc.message, tc.code, "req-1"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %q, want %q", err, want)
				}
			}
			if capture.method != http.MethodPost || capture.path != "/api/v1/prs/42/merge" || strings.Contains(out, "merged PR") {
				t.Fatalf("failed merge request = %s; stdout=%q", capture.method, out)
			}
		})
	}
}

func TestPRResolveCommentsPostsIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"resolved":2}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments", "42", "thread-1", "thread-2")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/prs/42/resolve-comments" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req resolveCommentsRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.CommentIDs) != 2 || req.CommentIDs[0] != "thread-1" || req.CommentIDs[1] != "thread-2" {
		t.Fatalf("comment ids = %v", req.CommentIDs)
	}
	if !strings.Contains(out, "resolved 2 review thread(s) on PR #42") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPRResolveCommentsAllowsNoIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"resolved":3}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments", "42"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.TrimSpace(capture.body) != "{}" {
		t.Fatalf("body = %q, want {}", capture.body)
	}
}

func TestPRResolveCommentsRequiresPRNumber(t *testing.T) {
	setConfigEnv(t)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", got, err)
	}
}
