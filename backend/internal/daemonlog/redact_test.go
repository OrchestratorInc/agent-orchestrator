package daemonlog

import (
	"strings"
	"testing"
)

func TestRedactRemovesCredentialsAndKeepsContext(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"url userinfo", `msg="clone failed" url=https://bob:hunter2@github.com/o/r.git`, `msg="clone failed" url=https://[redacted]@github.com/o/r.git`},
		{"url token as user", `remote=https://x-access-token:ghs_abc@github.com/o/r`, `remote=https://[redacted]@github.com/o/r`},
		{"authorization header", `header="Authorization: Bearer abc.def"`, `header="Authorization: Bearer [redacted]"`},
		{"bare bearer", `sent Bearer eyabc123 upstream`, `sent Bearer [redacted] upstream`},
		{"query secret", `GET https://cp.example/x?access_token=abc&page=2`, `GET https://cp.example/x?access_token=[redacted]&page=2`},
		{"signed url", `download https://s3.example/f?X-Amz-Signature=deadbeef&X-Amz-Expires=60`, `download https://s3.example/f?X-Amz-Signature=[redacted]&X-Amz-Expires=60`},
		{"slog attr", `level=WARN msg=refresh refresh_token=abc123 status=401`, `level=WARN msg=refresh refresh_token=[redacted] status=401`},
		{"quoted slog attr", `api_key="a b c" next=1`, `api_key=[redacted] next=1`},
		{"json", `{"password":"pw","user":"u"}`, `{"password":[redacted],"user":"u"}`},
		{"github token", `token ghp_abcdefghijklmnopqrstuvwxyz0123 leaked`, `token [redacted] leaked`},
		{"openai key", `key sk-proj-abcdefgh1234`, `key [redacted]`},
		{"jwt", `id eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.sig_part`, `id [redacted]`},
		{"counters stay", `input_tokens=12 output_tokens=4 token_count=9`, `input_tokens=12 output_tokens=4 token_count=9`},
		{"plain url stays", `fetch https://api.github.com/repos/o/r failed`, `fetch https://api.github.com/repos/o/r failed`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(redactWithHome([]byte(tc.in), "")); got != tc.want {
				t.Fatalf("redact(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactShortensTheHomeDirectory(t *testing.T) {
	got := string(redactWithHome([]byte(`dir=/Users/alice/.ao/data worktree=/Users/alice/code/x`), "/Users/alice"))
	if strings.Contains(got, "alice") || got != `dir=~/.ao/data worktree=~/code/x` {
		t.Fatalf("redact = %q", got)
	}
}
