package tmux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountRecoveryUsesCanonicalLaunchHandle(t *testing.T) {
	id := domain.SessionID(strings.Repeat("project", 9) + "-1")
	for _, handle := range []string{SessionName(string(id)), string(id), "unrelated"} {
		r, runner := newTestRuntime(0)
		runner.outputs, runner.err = [][]byte{[]byte("no server running on /tmp/isolated")}, &exec.ExitError{}
		got := r.ProbeFencedRuntime(t.Context(), ports.FencedRuntimeRef{SessionID: id, Handle: ports.RuntimeHandle{ID: handle}, Generation: "generation"})
		want := ports.FencedUnknown
		if handle == SessionName(string(id)) {
			want = ports.FencedDead
		}
		if got.Liveness != want {
			t.Fatalf("canonical=%v probe=%+v want=%s", handle == SessionName(string(id)), got, want)
		}
	}
}

func TestAccountRecoveryDistinguishesAbsentServerFromUnknownProbe(t *testing.T) {
	for _, tt := range []struct {
		name, output string
		err          error
		want         ports.FencedLiveness
	}{
		{name: "absent server", output: "no server running on /tmp/tmux-1000/default", err: &exec.ExitError{}, want: ports.FencedDead},
		{name: "absent socket", output: "error connecting to /tmp/tmux-1000/default (No such file or directory)", err: &exec.ExitError{}, want: ports.FencedDead},
		{name: "permission denied", output: "error connecting to /tmp/tmux-1000/default (Permission denied)", err: &exec.ExitError{}, want: ports.FencedUnknown},
		{name: "missing client", err: exec.ErrNotFound, want: ports.FencedUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, runner := newTestRuntime(0)
			runner.outputs, runner.err = [][]byte{[]byte(tt.output)}, tt.err
			got := r.ProbeFencedRuntime(context.Background(), ports.FencedRuntimeRef{SessionID: "account-session", Handle: ports.RuntimeHandle{ID: "account-session"}, Generation: "source"})
			if got.Liveness != tt.want {
				t.Fatalf("probe=%+v want=%s", got, tt.want)
			}
		})
	}
}

func TestAccountRecoveryRealRetainedPaneIsGenerationBound(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "account-residue-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	r := New(Options{Binary: binary, LegacyBinary: binary, SocketName: "residue", Shell: "/bin/sh", Timeout: time.Second})
	ref := ports.FencedRuntimeRef{SessionID: "account-residue", Handle: ports.RuntimeHandle{ID: "account-residue"}, Generation: "exited-generation"}
	t.Cleanup(func() { _ = r.Destroy(context.Background(), ref.Handle) })
	if _, err := r.Create(t.Context(), ports.RuntimeConfig{SessionID: ref.SessionID, WorkspacePath: t.TempDir(),
		Argv: []string{"/bin/true", "agent-process", "supervise", "--session", string(ref.SessionID), "--launch", ref.Generation, "--", "/bin/true", "quoted ' text; $()\nsecond line"},
		Env:  map[string]string{"AO_SUPERVISED_PROCESS": "1", "AO_RUNTIME_LAUNCH_ID": ref.Generation, "UNRELATED": "unexpanded `$()`; 'value'"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, pid, err := r.supervisedProcessTree(t.Context(), ref.Handle)
		if err != nil {
			t.Fatal(err)
		}
		retained := false
		for _, entry := range entries {
			if entry.pid == pid && entry.command == "cat" {
				retained = true
			}
		}
		if retained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not reach the real retained cat pane")
		}
		time.Sleep(10 * time.Millisecond)
	}
	wrong := ref
	wrong.Generation = "foreign-generation"
	if got := r.ProbeFencedRuntime(t.Context(), wrong); got.Liveness != ports.FencedUnknown {
		t.Fatalf("foreign residue generation accepted: %+v", got)
	}
	if got := r.ProbeFencedRuntime(t.Context(), ref); got.Liveness != ports.FencedDead {
		t.Fatalf("real exited retained pane is not recoverable: %+v", got)
	}
}
