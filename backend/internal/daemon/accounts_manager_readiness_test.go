package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type publicRuntimeWithoutExactInspector struct {
	runtimeselect.Runtime
	output  ports.StyledTerminalOutputReader
	handles ports.RuntimeLaunchHandleResolver
}

func (r *publicRuntimeWithoutExactInspector) GetStyledOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	return r.output.GetStyledOutput(ctx, handle, lines)
}

func (r *publicRuntimeWithoutExactInspector) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	return r.handles.LaunchHandles(id)
}

type publicRuntimeUnknownExactInspector struct{ *publicExecutionRuntime }

func (*publicRuntimeUnknownExactInspector) IsExactSupervisedProcessAlive(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (bool, error) {
	return false, errors.New("synthetic exact process probe failed")
}

func TestAccountsManagerControlReadinessRequiresExactProcess(t *testing.T) {
	for _, mode := range []string{"missing capability", "probe error", "replacement generation", "absent target"} {
		t.Run(mode, func(t *testing.T) {
			f, runtime, _ := newPublicExecutionFixtureWithRuntime(t, func(r *publicExecutionRuntime) runtimeselect.Runtime {
				switch mode {
				case "missing capability":
					wrapped := &publicRuntimeWithoutExactInspector{Runtime: r, output: r, handles: r}
					if _, ok := any(wrapped).(ports.ExactSupervisedProcessInspector); ok {
						t.Fatal("negative fixture accidentally exposes exact process proof")
					}
					return wrapped
				case "probe error":
					return &publicRuntimeUnknownExactInspector{r}
				default:
					return r
				}
			})
			binding := f.seed(t, domain.AccountsManagerManaged, "account-a")
			rec, _, err := f.store.GetSession(t.Context(), binding.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			rec.Activity.State = domain.ActivityExited
			if err := f.store.UpdateSession(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			path := "/sessions/" + string(binding.SessionID) + "/account-switches"
			body := fmt.Sprintf(`{"operationId":"readiness-negative","expectedRevision":%d,"mode":"managed","accountId":"account-b","policy":"interrupt","newConversation":true}`, binding.Revision)
			f.request(t, http.MethodPost, path, body, http.StatusAccepted)
			starting := awaitPublicExecutionPhase(t, f, "readiness-negative", domain.AccountsManagerSwitchStarting)
			deadline := time.Now().Add(3 * time.Second)
			for {
				rec, _, err = f.store.GetSession(t.Context(), binding.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if rec.Metadata.RuntimeLaunchID == starting.TargetGeneration {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("target identity was not published")
				}
				time.Sleep(time.Millisecond)
			}
			runtime.mu.Lock()
			runtime.ready = true
			switch mode {
			case "replacement generation":
				owner := runtime.owners[rec.Metadata.RuntimeHandleID]
				owner.Generation = "unrelated-replacement"
				runtime.owners[rec.Metadata.RuntimeHandleID] = owner
			case "absent target":
				delete(runtime.owners, rec.Metadata.RuntimeHandleID)
			}
			runtime.mu.Unlock()
			op := awaitPublicExecutionPhase(t, f, starting.ID, domain.AccountsManagerSwitchRecoveryRequired)
			if op.ErrorCode != "TARGET_NOT_READY" || op.TargetGeneration != starting.TargetGeneration || op.TargetRevision != starting.TargetRevision {
				t.Fatal("unproven target changed the recorded readiness decision")
			}
			if release, allowed := f.manager.AcquireSessionInput(binding.SessionID); allowed {
				release()
				t.Fatal("visible composer released input without exact process proof")
			}
			runtime.mu.Lock()
			defer runtime.mu.Unlock()
			if runtime.destroyed != 1 || len(runtime.launches) != 1 {
				t.Fatal("failed readiness caused extra teardown or launch")
			}
			if mode == "replacement generation" && runtime.owners[rec.Metadata.RuntimeHandleID].Generation != "unrelated-replacement" {
				t.Fatal("readiness failure changed a replacement owner")
			}
		})
	}
}
