package tmux

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func retainedTestCommand() string {
	return buildLaunchCommand(ports.RuntimeConfig{SessionID: "session", WorkspacePath: "/tmp/with a ' quote", Argv: []string{"/opt/ao", "agent-process", "supervise", "--session", "session", "--launch", "generation", "--", "worker", "prompt; exec cat >/dev/null\n$() ` ; 'quoted'"},
		Env: map[string]string{"AO_RUNTIME_LAUNCH_ID": "generation", "AO_SUPERVISED_PROCESS": "1", "UNRELATED": "'export AO_RUNTIME_LAUNCH_ID=other'; $(no) `no`"}})
}

func TestAccountRecoveryRetainedOriginRejectsSpoofedTokens(t *testing.T) {
	command := retainedTestCommand()
	for _, tt := range []struct {
		name, script string
		valid        bool
	}{
		{"generated command with quoted content", command, true},
		{"manual sink", "exec cat >/dev/null", false},
		{"generation embedded in prompt", strings.Replace(command, "AO_RUNTIME_LAUNCH_ID='generation'", "AO_RUNTIME_LAUNCH_ID='foreign'", 1), false},
		{"other session", strings.Replace(command, "'--session' 'session'", "'--session' 'other'", 1), false},
		{"interpreting shell", strings.TrimSuffix(command, "; exec cat >/dev/null") + "; exec /bin/sh -i", false},
		{"extra command", command + "; echo unsafe", false},
		{"unquoted substitution", strings.Replace(command, "cd ", "cd $(unsafe)", 1), false},
		{"ownership text alone", "echo " + shellQuote(command) + "; exec cat >/dev/null", false},
		{"NUL cannot forge an operator", command + "\\\x00;", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			generation, ok := retainedPaneGeneration("/bin/sh -c "+strconv.Quote(tt.script), "session")
			if ok != tt.valid || (ok && generation != "generation") {
				t.Fatalf("origin recognized=%v generation=%q", ok, generation)
			}
		})
	}
}

func TestAccountRecoveryRetainedPaneRequiresStableEmptyProcessTree(t *testing.T) {
	for _, name := range []string{"known residue", "metadata unavailable", "multiple panes", "manual cat", "manual child", "changed pid", "changed process", "foreign generation"} {
		t.Run(name, func(t *testing.T) {
			r, fr := newTestRuntime(0)
			origin := "/bin/sh -c " + strconv.Quote(retainedTestCommand())
			fr.outputs = [][]byte{nil, []byte("100\n"), []byte("100 1 cat\n"), []byte("100\n"), []byte(origin), []byte("100\n"), []byte("100 1 cat\n")}
			switch name {
			case "metadata unavailable":
				fr.hook = func(_ context.Context, call int) error {
					if call == 5 {
						return errors.New("metadata unavailable")
					}
					return nil
				}
			case "multiple panes":
				fr.outputs[3] = []byte("100\n200\n")
			case "manual cat":
				fr.outputs[4] = []byte("/bin/sh -c 'exec cat >/dev/null'")
			case "manual child":
				fr.outputs[2] = []byte("100 1 cat\n101 100 worker\n")
			case "changed pid":
				fr.outputs[5] = []byte("200\n")
			case "changed process":
				fr.outputs[6] = []byte("100 1 /bin/sh -i\n")
			case "foreign generation":
				fr.outputs[4] = []byte(strings.ReplaceAll(origin, "generation", "foreign"))
			}
			got := r.ProbeFencedRuntime(t.Context(), ports.FencedRuntimeRef{SessionID: "session", Handle: ports.RuntimeHandle{ID: "session"}, Generation: "generation"})
			want := ports.FencedUnknown
			if name == "known residue" {
				want = ports.FencedDead
			}
			if got.Liveness != want {
				t.Fatalf("probe=%+v want=%s", got, want)
			}
		})
	}
}
