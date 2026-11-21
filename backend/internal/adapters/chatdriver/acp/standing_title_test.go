package acp

import "testing"

func TestProviderTitleDropsStandingInstructions(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"Fix the login bug", "Fix the login bug", true},
		{"<ao-standing-instructions>", "", false},
		{"<ao-standing-instructions>\n## Worker Role\nYou are a worker", "", false},
		{"<ao-standing-instructions>\nrules\n</ao-standing-instructions>\n\nSLOW task", "SLOW task", true},
		{"<ao-standing-instructions>\nrules\n</ao-standing-instructions>", "", false},
		{"Explain <ao-standing-instructions>", "Explain <ao-standing-instructions>", true},
	}
	for _, c := range cases {
		got, ok := providerTitle(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("providerTitle(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
