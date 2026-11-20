package domain

import (
	"strings"
	"testing"
)

func TestSanitizeTaskDescription(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"npm run dev", "npm run dev"},
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
