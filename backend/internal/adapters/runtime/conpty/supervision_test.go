package conpty

import "testing"

func TestSupervisorInvocationMatchesExactArgvTokens(t *testing.T) {
	cases := []struct {
		name               string
		argv               []string
		supervisor, ofThis bool
	}{
		{"session launch", []string{"/opt/ao", "agent-process", "supervise", "--session", "s-1", "--launch", "l-1", "--", "codex"}, true, true},
		{"inline flags", []string{"ao", "agent-process", "supervise", "--session=s-1", "--launch=l-1", "--", "codex"}, true, true},
		{"reviewer", []string{"ao", "agent-process", "supervise", "--session", "s-1", "--activity-review", "r-7", "--launch", "l-1", "--", "x"}, true, true},
		{"other launch", []string{"ao", "agent-process", "supervise", "--session", "s-1", "--launch", "l-0", "--", "codex"}, true, false},
		{"other session", []string{"ao", "agent-process", "supervise", "--session", "s-2", "--launch", "l-1", "--", "codex"}, true, false},
		{"flags after separator are agent argv", []string{"ao", "agent-process", "supervise", "--", "--session", "s-1", "--launch", "l-1"}, true, false},
		{"prompt text only", []string{"/bin/sh", "-c", "agent-process supervise --session s-1 --launch l-1"}, false, false},
		{"agent argv", []string{"claude", "--resume", "x"}, false, false},
	}
	for _, tc := range cases {
		supervisor, ofThis := supervisorInvocation(tc.argv, "s-1", "l-1")
		if supervisor != tc.supervisor || ofThis != tc.ofThis {
			t.Errorf("%s: supervisorInvocation = %v, %v; want %v, %v", tc.name, supervisor, ofThis, tc.supervisor, tc.ofThis)
		}
	}
}

func TestSupervisionRecordIsTriState(t *testing.T) {
	for _, mode := range []supervisionMode{supervisionUnknown, supervisionSupervised, supervisionDirect} {
		if got := supervisionFromRecord(mode.recorded()); got != mode {
			t.Fatalf("round trip of %v = %v", mode, got)
		}
	}
	if supervisionUnknown.recorded() != nil {
		t.Fatal("unknown supervision must be stored as absent")
	}
	if v := supervisionDirect.recorded(); v == nil || *v {
		t.Fatal("a known direct child must be stored as explicit false")
	}
}
