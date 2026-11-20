package domain

import (
	"strings"
	"testing"
)

func TestSanitizeTaskDescription(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"npm run dev", "npm run dev"},
		{`python -u -m http.server 8765`, `python -u -m http.server 8765`},
		{`git push -u origin main`, `git push -u origin main`},
		{`sort -u file`, `sort -u file`},
		{`git add -u .`, `git add -u .`},
		{`docker run -u 1000 img`, `docker run -u 1000 img`},
		{`tool --user alice --user=1000`, `tool --user alice --user=1000`},
		{`https://example.com?to=a@b.com https://example.com#user:pass@host`, `https://example.com?to=a@b.com https://example.com#user:pass@host`},
		{`https://alice@example.com/path`, `https://alice@example.com/path`},
		{`tool --bypass checks --sort-key name --monkey banana`, `tool --bypass checks --sort-key name --monkey banana`},
		{`tool --api-key abc --access-token def`, `tool --api-key [redacted] --access-token [redacted]`},
		{`Authorization=Bearer abc Authorization=Basic def Authorization=token ghi`, `Authorization=[redacted] Authorization=[redacted] Authorization=[redacted]`},
		{`curl -u user:pass --user='user:pass word' --user=user:secret`, `curl -u [redacted] --user=[redacted] --user=[redacted]`},
		{`tool --password hunter2 --token=abc --credential 'with spaces'`, `tool --password [redacted] --token=[redacted] --credential [redacted]`},
		{`curl -H 'Authorization: Basic dXNlcjpwYXNz' -H "Authorization: token ghp_abc"`, `curl -H 'Authorization: Basic [redacted]' -H "Authorization: token [redacted]"`},
		{`DB_PASS=x DB_PWD=y CREDENTIAL=z`, `DB_PASS=[redacted] DB_PWD=[redacted] CREDENTIAL=[redacted]`},
		{`postgres://user:pass@host/db curl -u user:pass --user "user:pass word"`, `postgres://[redacted]@host/db curl -u [redacted] --user [redacted]`},
		{`set "API_KEY=x" && echo ok`, `set "API_KEY=[redacted]" && echo ok`},
		{`set "API_KEY=has spaces"`, `set "API_KEY=[redacted]"`},
		{"cmd /c \"set AO_TEST_TOKEN=sk-test-123 && ping host\"", "cmd /c \"set AO_TEST_TOKEN=[redacted] && ping host\""},
		{`API_KEY="has spaces" SECRET='other value' PASSWORD=hidden`, `API_KEY=[redacted] SECRET=[redacted] PASSWORD=[redacted]`},
		{"curl -H 'Authorization: Bearer abc.def-123'", "curl -H 'Authorization: Bearer [redacted]'"},
		{"one\n\t two\u202ethree\u200b", "one twothree"},
		{strings.Repeat("界", 250), strings.Repeat("界", 239) + "…"},
	} {
		if got := SanitizeTaskDescription(tc.input); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
