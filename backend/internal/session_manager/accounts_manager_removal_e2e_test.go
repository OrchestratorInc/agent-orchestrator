//go:build e2e && (linux || darwin)

package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestAccountsManagerRemovalRealProduction(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		for _, state := range []string{"retained-pane", "live-owner", "post-error-replacement"} {
			takeover := state == "post-error-replacement"
			name := map[bool]string{false: "direct", true: "fallback"}[fallback] + "/" + state
			t.Run(name, func(t *testing.T) {
				m, st, rt, _, rec, _ := realSelectedAccountRuntimeFixture(t, fallback)
				binary, err := m.executable()
				if err != nil {
					t.Fatal(err)
				}
				create := func(ctx context.Context, id domain.SessionID, generation string) (ports.RuntimeHandle, error) {
					return rt.Create(ctx, ports.RuntimeConfig{SessionID: id, WorkspacePath: rec.Metadata.WorkspacePath,
						Argv: []string{binary, "agent-process", "supervise", "--session", string(id), "--launch", generation, "--", "/bin/sleep", "120"},
						Env:  map[string]string{EnvSupervisedProcess: "1", EnvRuntimeLaunchID: generation, EnvDataDir: m.dataDir, EnvRunFile: m.runFilePath}})
				}
				native := rec
				native.ID = ""
				native, err = st.CreateSession(t.Context(), native)
				if err != nil {
					t.Fatal(err)
				}
				provider, _ := accountsManagerProvider(native.Harness)
				if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: native.ID, Provider: provider, Mode: domain.AccountsManagerNative}); err != nil {
					t.Fatal(err)
				}
				nativeHandle, err := create(t.Context(), native.ID, "unrelated-native-generation")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = rt.Destroy(context.Background(), nativeHandle) })
				nativeRef := ports.FencedRuntimeRef{SessionID: native.ID, Handle: nativeHandle, Generation: "unrelated-native-generation"}
				waitSelectedAccountProbe(t, rt, nativeRef, ports.FencedAlive)
				native.Metadata.RuntimeHandleID, native.Metadata.RuntimeLaunchID = nativeHandle.ID, nativeRef.Generation
				if err := st.UpdateSession(t.Context(), native); err != nil {
					t.Fatal(err)
				}
				other := native
				other.ID = ""
				other, err = st.CreateSession(t.Context(), other)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: other.ID, Provider: provider, Mode: domain.AccountsManagerManaged, AccountID: "account-b"}); err != nil {
					t.Fatal(err)
				}
				otherHandle, err := create(t.Context(), other.ID, "account-b-generation")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = rt.Destroy(context.Background(), otherHandle) })
				otherRef := ports.FencedRuntimeRef{SessionID: other.ID, Handle: otherHandle, Generation: "account-b-generation"}
				waitSelectedAccountProbe(t, rt, otherRef, ports.FencedAlive)
				other.Metadata.RuntimeHandleID, other.Metadata.RuntimeLaunchID = otherHandle.ID, otherRef.Generation
				if err := st.UpdateSession(t.Context(), other); err != nil {
					t.Fatal(err)
				}
				handle := runtimeHandle(rec.Metadata)
				if state != "retained-pane" {
					if err := rt.Destroy(t.Context(), handle); err != nil {
						t.Fatal(err)
					}
					launched, err := create(t.Context(), rec.ID, rec.Metadata.RuntimeLaunchID)
					if err != nil || launched != handle {
						t.Fatal("live source slot changed", err)
					}
					waitSelectedAccountProbe(t, rt, ports.FencedRuntimeRef{SessionID: rec.ID, Handle: handle, Generation: rec.Metadata.RuntimeLaunchID}, ports.FencedAlive)
				}
				wrapped := &accountRealDestroyTakeover{Runtime: rt, handle: handle}
				replacement := ports.FencedRuntimeRef{SessionID: rec.ID, Handle: handle, Generation: "replacement-generation"}
				wrapped.replace = func(ctx context.Context) error {
					if err := rt.Destroy(ctx, handle); err != nil {
						return err
					}
					if takeover {
						created, err := create(ctx, rec.ID, replacement.Generation)
						if err != nil || created != handle {
							return errors.Join(err, errors.New("replacement slot changed"))
						}
						waitSelectedAccountProbe(t, rt, replacement, ports.FencedAlive)
					}
					return errors.New("destroy response unavailable")
				}
				router := &accountRemovalRouter{accountSwitchRouter: &accountSwitchRouter{store: st}, finalErr: errors.New("vault unavailable")}
				m.runtime, m.accountsManager = wrapped, router
				impact, err := st.AccountsManagerRemovalImpact(t.Context(), "account-a")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.StartAccountsManagerRemoval(t.Context(), "real-delete", "account-a", impact.Revision, true); err != nil {
					t.Fatal(err)
				}
				op := waitAccountRemoval(t, m, st, "real-delete")
				if op.Phase != domain.AccountsManagerRemovalRecovery || !op.Impact.Sessions[0].Stopped || !op.BindingsRevoked {
					t.Fatal("runtime stop was not acknowledged", op.Phase, op.ErrorCode)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = sqlite.OpenPreMigrated(rec.Metadata.WorkspacePath)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				router.store = st
				rt = runtimeselect.New(nil, m.runFilePath)
				wrapped.Runtime = rt
				m = New(Deps{Store: st, Runtime: wrapped, Lifecycle: lifecycle.New(st, nil), AccountsManager: router, BackgroundContext: t.Context()})
				if err := m.ReconcileStartupSafety(t.Context()); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if _, err := m.RetryAccountsManagerRemoval(t.Context(), op.ID); err != nil {
						t.Fatal(err)
					}
					waitAccountRemoval(t, m, st, op.ID)
				}
				router.finalErr = nil
				if _, err := m.RetryAccountsManagerRemoval(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
				final := waitAccountRemoval(t, m, st, op.ID)
				if final.Phase != domain.AccountsManagerRemovalComplete || wrapped.destroys != 1 || wrapped.creates != 0 {
					t.Fatal("deletion repeated teardown or launched a fallback", final.Phase, final.ErrorCode, wrapped.destroys)
				}
				waitSelectedAccountProbe(t, rt, nativeRef, ports.FencedAlive)
				waitSelectedAccountProbe(t, rt, otherRef, ports.FencedAlive)
				for _, unrelated := range []domain.SessionID{native.ID, other.ID} {
					if release, ok := m.AcquireSessionInput(unrelated); !ok {
						t.Error("unrelated account/native intake was fenced")
					} else {
						release()
					}
				}
				if takeover {
					waitSelectedAccountProbe(t, rt, replacement, ports.FencedAlive)
				}
			})
		}
	}
}
