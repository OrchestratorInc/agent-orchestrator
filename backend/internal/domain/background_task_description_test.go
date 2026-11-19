package domain

import (
	"strings"
	"testing"
)

func TestSanitizeTaskDescription(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"npm run dev", "npm run dev"},
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
