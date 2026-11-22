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

func TestSanitizeTaskCommand(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"npm run dev", "npm run dev"},
		{"cat <<EOF\n\tline\nEOF", "cat <<EOF\n\tline\nEOF"},
		{"AO_TEST_TOKEN=sk-test-123 curl -H 'Authorization: Bearer abc.def-123' x", "AO_TEST_TOKEN=[redacted] curl -H 'Authorization: Bearer [redacted]' x"},
		{"echo \u202eevil\u200b\x1b[31m\r", "echo evil[31m"},
		{"echo \U0001F468\u200d\U0001F469", "echo \U0001F468\u200d\U0001F469"},
		{strings.Repeat("x", 300), strings.Repeat("x", 300)},
		// An assignment never spans a line break, and ordinary words are not names.
		{"TOKEN=\nnpm run dev", "TOKEN=\nnpm run dev"},
		{"monkey=1 api_key=abc APIKEY=def", "monkey=1 api_key=[redacted] APIKEY=[redacted]"},
	} {
		if got := SanitizeTaskCommand(tc.input); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
