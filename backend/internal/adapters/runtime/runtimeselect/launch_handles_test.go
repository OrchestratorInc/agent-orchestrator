package runtimeselect

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type launchBackend struct {
	fakeBackend
	launches []ports.RuntimeHandle
	err      error
	refs     []ports.FencedRuntimeRef
}

func (b *launchBackend) LaunchHandles(domain.SessionID) ([]ports.RuntimeHandle, error) {
	return b.launches, b.err
}

func (b *launchBackend) ProbeFencedRuntime(_ context.Context, ref ports.FencedRuntimeRef) ports.FencedProbeResult {
	b.refs = append(b.refs, ref)
	return ports.FencedProbeResult{Liveness: ports.FencedAlive, Reason: ports.FencedReasonExactMatch}
}

func TestProductionRuntimeLaunchIdentitiesSurviveReconstruction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.json")
	for _, id := range []domain.SessionID{"session-1", domain.SessionID(strings.Repeat("project", 9) + "-1")} {
		var previous []ports.RuntimeHandle
		for range 2 {
			selected := New(nil, path)
			resolver, ok := selected.(ports.RuntimeLaunchHandleResolver)
			if !ok {
				t.Fatalf("selected runtime %T does not expose launch identities", selected)
			}
			handles, err := resolver.LaunchHandles(id)
			if err != nil {
				t.Fatal(err)
			}
			want := []ports.RuntimeHandle{{ID: tmux.SessionName(string(id))}}
			switch runtime.GOOS {
			case "linux", "darwin":
				want = append([]ports.RuntimeHandle{{ID: directHandlePrefix + string(id)}}, want...)
			case "windows":
				want = []ports.RuntimeHandle{{ID: string(id)}}
			}
			if !reflect.DeepEqual(handles, want) || (previous != nil && !reflect.DeepEqual(handles, previous)) {
				t.Fatalf("launch identities=%v want=%v previous=%v", handles, want, previous)
			}
			previous = handles
		}
	}
}

func TestHybridRuntimeLaunchIdentitiesCoverCreateAndFallback(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		id := domain.SessionID(strings.Repeat("project", 9) + "-1")
		raw := ports.RuntimeHandle{ID: string(id)}
		legacyID := ports.RuntimeHandle{ID: tmux.SessionName(string(id))}
		direct := &launchBackend{fakeBackend: fakeBackend{createHandle: raw}, launches: []ports.RuntimeHandle{raw}}
		legacy := &launchBackend{fakeBackend: fakeBackend{createHandle: legacyID}, launches: []ports.RuntimeHandle{legacyID}}
		if fallback {
			direct.createErr = errors.New("host unavailable")
		}
		r := newHybridRuntime(legacy, direct, nil, "test")
		before, err := r.LaunchHandles(id)
		if err != nil || len(direct.calls)+len(legacy.calls) != 0 {
			t.Fatal("identity resolution caused effects or failed", err)
		}
		handle, err := r.Create(t.Context(), ports.RuntimeConfig{SessionID: id})
		if err != nil || !slices.Contains(before, handle) {
			t.Fatal("created runtime was not reserved", err)
		}
		after, err := r.LaunchHandles(id)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("backend availability changed the reservation", err)
		}
		for _, h := range before {
			ref := ports.FencedRuntimeRef{SessionID: id, Handle: h, Generation: "generation"}
			if got := r.ProbeFencedRuntime(t.Context(), ref); got.Liveness != ports.FencedAlive {
				t.Fatalf("probe=%+v", got)
			}
		}
		if len(direct.refs) != 1 || direct.refs[0].Handle != raw || direct.refs[0].SessionID != id || direct.refs[0].Generation != "generation" ||
			len(legacy.refs) != 1 || legacy.refs[0].Handle != legacyID || legacy.refs[0].SessionID != id || legacy.refs[0].Generation != "generation" {
			t.Fatal("wrapper changed the backend ownership identity")
		}
	}
}

func TestHybridRuntimeLaunchIdentitiesFailClosed(t *testing.T) {
	for _, invalid := range []string{"unsupported", "missing", "error", "empty", "duplicate", "prefixed", "unknown version", "delimiter"} {
		for _, directInvalid := range []bool{false, true} {
			valid := &launchBackend{launches: []ports.RuntimeHandle{{ID: "session-1"}}}
			bad := &launchBackend{launches: []ports.RuntimeHandle{{ID: "session-1"}}}
			var backend routedBackend = bad
			switch invalid {
			case "unsupported":
				backend = &fakeBackend{}
			case "missing":
				bad.launches = nil
			case "error":
				bad.err = errors.New("unavailable identity")
			case "empty":
				bad.launches[0].ID = ""
			case "duplicate":
				bad.launches = append(bad.launches, bad.launches[0])
			case "prefixed":
				bad.launches[0].ID = directHandlePrefix + "session-1"
			case "unknown version":
				bad.launches[0].ID = "ptyhost-v2:session-1"
			case "delimiter":
				bad.launches[0].ID = "session\x00-1"
			}
			r := newHybridRuntime(backend, valid, nil, "test")
			if directInvalid {
				r = newHybridRuntime(valid, backend, nil, "test")
			}
			if handles, err := r.LaunchHandles("session-1"); err == nil || len(handles) != 0 {
				t.Fatalf("%s direct=%v accepted: %v %v", invalid, directInvalid, handles, err)
			}
			got := r.ProbeFencedRuntime(t.Context(), ports.FencedRuntimeRef{SessionID: "session-1", Handle: ports.RuntimeHandle{ID: "session-1"}, Generation: "generation"})
			if got.Liveness != ports.FencedUnknown || len(valid.refs)+len(bad.refs) != 0 || len(valid.calls)+len(bad.calls) != 0 {
				t.Fatalf("%s routed an ambiguous identity", invalid)
			}
		}
	}
	for _, id := range []string{"", directHandlePrefix, "ptyhost-v2:session-1", directHandlePrefix + directHandlePrefix + "session-1", "other-session"} {
		direct := &launchBackend{launches: []ports.RuntimeHandle{{ID: "session-1"}}}
		legacy := &launchBackend{launches: []ports.RuntimeHandle{{ID: "session-1"}}}
		r := newHybridRuntime(legacy, direct, nil, "test")
		got := r.ProbeFencedRuntime(t.Context(), ports.FencedRuntimeRef{SessionID: "session-1", Handle: ports.RuntimeHandle{ID: id}, Generation: "generation"})
		if got.Liveness != ports.FencedUnknown || len(direct.refs)+len(legacy.refs) != 0 {
			t.Fatalf("malformed handle %q was routed", id)
		}
	}
}
