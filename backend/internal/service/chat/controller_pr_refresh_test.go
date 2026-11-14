package chat

import "testing"

func TestCommandDetailMayChangePR(t *testing.T) {
	for _, tc := range []struct {
		detail string
		want   bool
	}{
		{`{"command":"gh pr merge --squash"}`, true},
		{`{"command":"git push"}`, true},
		{`{"command":"gh pr view"}`, false},
		{`{"path":"a.go"}`, false},
		{`not json`, false},
		{``, false},
	} {
		if got := commandDetailMayChangePR([]byte(tc.detail)); got != tc.want {
			t.Errorf("commandDetailMayChangePR(%q) = %v, want %v", tc.detail, got, tc.want)
		}
	}
}
