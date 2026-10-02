package accountsmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type deletionClient struct {
	lifecycleClient
	client *fakeClient
	remove func(context.Context, string) error
}

func (c *deletionClient) ListCredentials(ctx context.Context) ([]core.CredentialSummary, error) {
	return c.client.ListCredentials(ctx)
}
func (c *deletionClient) CredentialPublicID(ref string) (string, error) {
	return c.client.CredentialPublicID(ref)
}
func (c *deletionClient) OAuthPublicID(state string) (string, error) {
	return c.client.OAuthPublicID(state)
}
func (c *deletionClient) StreamOAuthEvents(ctx context.Context, consume func(core.OAuthEvent) error) error {
	return c.client.StreamOAuthEvents(ctx, consume)
}
func (c *deletionClient) SynchronizeBindings(ctx context.Context, snapshot core.BindingSnapshot) error {
	return c.client.SynchronizeBindings(ctx, snapshot)
}
func (c *deletionClient) MintRoute(ctx context.Context, provider core.Provider, ref, session, account string, revision int64) (core.RouteCapability, error) {
	return c.client.MintRoute(ctx, provider, ref, session, account, revision)
}
func (c *deletionClient) RemoveCredential(ctx context.Context, ref string) error {
	return c.remove(ctx, ref)
}

func TestAccountDeletionFinalizationCrashRecovery(t *testing.T) {
	for _, state := range []string{"present", "already-missing", "remove-response-lost"} {
		t.Run(state, func(t *testing.T) {
			path := t.TempDir()
			st := sqlitetest.MustOpenAt(t, path)
			if err := st.UpsertProject(t.Context(), domain.ProjectRecord{ID: "deletion", Path: path, RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "deletion", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI, CreatedAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			binding, _, err := st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "safe-a"})
			if err != nil {
				t.Fatal(err)
			}
			client := &deletionClient{client: &fakeClient{credentials: []core.CredentialSummary{{Ref: "a", Provider: core.ProviderCodex, Status: core.CredentialActive}}}}
			removals := 0
			client.remove = func(ctx context.Context, ref string) error {
				removals++
				op, _, err := st.GetAccountsManagerRemoval(ctx, "remove-a")
				if err != nil || ref != "a" || !op.StopStarted || !op.BindingsRevoked || !op.Impact.Sessions[0].Stopped {
					t.Fatal("credential removal preceded durable acknowledgements", err)
				}
				if len(client.client.bindings.Bindings) != 1 || !client.client.bindings.Bindings[0].Blocked {
					t.Fatal("credential removal preceded runner revocation")
				}
				client.client.credentials = nil
				if state == "remove-response-lost" {
					return core.ErrUnavailable
				}
				return nil
			}
			if state == "already-missing" {
				client.client.credentials = nil
			}
			svc := New(client, st)
			impact, err := st.AccountsManagerRemovalImpact(t.Context(), "safe-a")
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := svc.PrepareAccountRemoval(t.Context(), "remove-a", "safe-a", impact.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.FinalizeAccountRemoval(t.Context(), op.ID); !errors.Is(err, domain.ErrAccountsManagerRemovalConflict) || removals != 0 {
				t.Fatal("pre-stop finalization was admitted", err)
			}
			if err := st.BeginAccountsManagerRemovalStop(t.Context(), op.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.SynchronizeAgentBindings(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := st.RecordAccountsManagerRemovalBindingsRevoked(t.Context(), op.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.FinalizeAccountRemoval(t.Context(), op.ID); !errors.Is(err, domain.ErrAccountsManagerAccountInUse) || removals != 0 {
				t.Fatal("unstopped owner allowed credential removal", err)
			}
			if err := st.RecordAccountsManagerRemovalStopped(t.Context(), op.ID, rec.ID); err != nil {
				t.Fatal(err)
			}
			err = svc.FinalizeAccountRemoval(t.Context(), op.ID)
			if state == "remove-response-lost" {
				if !errors.Is(err, core.ErrUnavailable) {
					t.Fatal("fixture did not cut the cross-store response", err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				st, err = sqlite.OpenPreMigrated(path)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				svc = New(client, st)
				err = svc.FinalizeAccountRemoval(t.Context(), op.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := svc.FinalizeAccountRemoval(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
			}
			wantRemovals := 1
			if state == "already-missing" {
				wantRemovals = 0
			}
			if removals != wantRemovals {
				t.Fatal("idempotent recovery repeated credential deletion", removals)
			}
			choice, found, err := st.GetAccountsManagerSessionRoute(t.Context(), rec.ID, binding.Provider)
			if err != nil || !found || !choice.Blocked || choice.AccountID != binding.AccountID {
				t.Fatal("finalization discarded the explicit blocked choice", err)
			}
			if _, err := svc.PrepareAgentLaunchRoute(t.Context(), rec.ID, binding.Provider, ""); err == nil {
				t.Fatal("deleted account relaunch was admitted")
			}
		})
	}
}
