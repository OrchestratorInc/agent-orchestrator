package tmux

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRemovalProbeDistinguishesReplacementFromCoexistingOwner(t *testing.T) {
	for _, processes := range []string{
		"101 100 /opt/ao agent-process supervise --session sess-1 --launch original -- worker\n201 100 /opt/ao agent-process supervise --session sess-1 --launch replacement -- worker\n",
		"201 100 /opt/ao agent-process supervise --session sess-1 --launch replacement -- worker\n101 100 /opt/ao agent-process supervise --session sess-1 --launch original -- worker\n",
	} {
		r, fr := newTestRuntime(0)
		fr.outputs = [][]byte{nil, []byte("100\n"), []byte("100 1 /bin/sh -i\n" + processes)}
		probe := r.ProbeFencedRuntime(context.Background(), ports.FencedRuntimeRef{
			Handle: ports.RuntimeHandle{ID: "sess-1"}, SessionID: "sess-1", Generation: "original",
		})
		if probe.Liveness != ports.FencedUnknown || probe.Reason == ports.FencedReasonGenerationMismatch {
			t.Fatalf("coexisting recorded owner reported as replaced: %+v", probe)
		}
	}
}
