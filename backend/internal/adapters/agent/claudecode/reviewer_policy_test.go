package claudecode

import "testing"

func TestReviewerSubmitCommandAllowed(t *testing.T) {
	post := func(body string) string {
		return `printf '%s' '{"event":"COMMENT","body":"` + body + `"}' | ` +
			`gh api --method POST repos/acme/app/pulls/12/reviews --input - --jq '.id'`
	}
	submit := func(session string) string {
		return `printf '%s' '{"reviews":[]}' | ao review submit --session ` + session + ` --reviews -`
	}
	tests := []struct {
		name    string
		command string
		worker  string
		want    bool
	}{
		{"review POST", post("looks good"), "w-1", true},
		{"apostrophe escaped as '\\''", post(`it'\''s fine`), "w-1", true},
		{"apostrophe escaped as '\"'\"'", post(`it'"'"'s fine`), "w-1", true},
		{"command substitution between quotes", post(`x'"$(id)"'y`), "w-1", false},
		{"unescaped apostrophe", post(`it's fine`), "w-1", false},
		{"chained command", post("ok") + "; touch pwned", "w-1", false},
		{"submit for the worker", submit("w-1"), "w-1", true},
		{"submit for another worker", submit("w-2"), "w-1", false},
		{"launch without a worker", post("ok"), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReviewerSubmitCommandAllowed(tt.command, tt.worker); got != tt.want {
				t.Fatalf("ReviewerSubmitCommandAllowed(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
