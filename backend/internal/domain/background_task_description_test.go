package domain

import (
	"strings"
	"testing"
)

func TestSanitizeTaskDescription(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"npm run dev", "npm run dev"},
		{`curl --authorization=Bearer abc --authorization=Basic def`, `curl --authorization=[redacted] --authorization=[redacted]`},
		{`deploy --githubToken abc --myPassword=secret`, `deploy --githubToken [redacted] --myPassword=[redacted]`},
		{`deploy --github-token ghp_abc123 --openai-api-key sk-secret --db-password hunter2`, `deploy --github-token [redacted] --openai-api-key [redacted] --db-password [redacted]`},
		{`deploy --github-token=ghp_abc123 --openai-api-key=sk-secret --db-password=hunter2`, `deploy --github-token=[redacted] --openai-api-key=[redacted] --db-password=[redacted]`},
		{`git clone https://ghp_abc123@github.com/o/r.git`, `git clone https://[redacted]@github.com/o/r.git`},
		{`sort --sort-key=name --primary-key id`, `sort --sort-key=name --primary-key id`},
		{`sort --sort-key name --primary-key=id`, `sort --sort-key name --primary-key=id`},
		{`docker run -u 1000:1000 img`, `docker run -u [redacted] img`},
		{`python -u -m http.server 8765`, `python -u -m http.server 8765`},
		{`git push -u origin main`, `git push -u origin main`},
		{`sort -u file`, `sort -u file`},
		{`git add -u .`, `git add -u .`},
		{`docker run -u 1000 img`, `docker run -u 1000 img`},
		{`tool --user alice --user=1000`, `tool --user alice --user=1000`},
		{`https://example.com?to=a@b.com https://example.com#user:pass@host`, `https://example.com?to=a@b.com https://example.com#user:pass@host`},
		{`https://alice@example.com/path`, `https://[redacted]@example.com/path`},
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

func TestSanitizeTaskDescriptionCustomCredentialFlags(t *testing.T) {
	for _, prefix := range []string{"github", "openai-api", "db", "vendor-subsystem", "custom_service"} {
		for _, suffix := range []string{"key", "token", "secret", "password", "passwd", "credential"} {
			for _, separator := range []string{" ", "="} {
				flag := "--" + prefix + "-" + suffix + separator
				input, want := "deploy "+flag+"supersecret", "deploy "+flag+"[redacted]"
				if got := SanitizeTaskDescription(input); got != want || SanitizeTaskDescription(got) != got {
					t.Errorf("%q: got %q, want %q (idempotent)", input, got, want)
				}
			}
		}
	}
}
