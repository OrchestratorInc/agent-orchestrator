package domain

import "testing"

func TestCommandMayChangePR(t *testing.T) {
	for command, want := range map[string]bool{
		"gh pr merge --admin --squash":        true,
		"cd /tmp/repo && gh pr close 4":       true,
		"/opt/homebrew/bin/gh pr ready":       true,
		"bash -lc 'gh pr review --approve'":   true,
		"gh pr create --fill":                 true,
		"gh pr edit 3 --title x":              true,
		"git status; git push -u origin HEAD": true,
		"gh pr view 3":                        false,
		"gh pr checks":                        false,
		"gh issue close 3":                    false,
		"git status":                          false,
		"git fetch origin":                    false,
		"":                                    false,
	} {
		if got := CommandMayChangePR(command); got != want {
			t.Errorf("CommandMayChangePR(%q) = %v, want %v", command, got, want)
		}
	}
}
