package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func TestRemoteHostTunnelsAreServiceOwnedAndSwept(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store, _ := openAppRoleStore(t, admin)
	ctx := context.Background()
	registered := "h_" + uuid.NewString()
	orphan := "h_" + uuid.NewString()
	if err := store.UpsertRemoteHost(ctx, domain.Principal{UserID: fixture.userID}, domain.RemoteHost{
		HostID: registered, Label: "Mac", URL: "https://h-x.example.dev", EncryptedToken: []byte{1}, TokenNonce: []byte{2},
	}); err != nil {
		t.Fatalf("register host: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_remote_hosts WHERE host_id = $1`, registered)
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_remote_host_tunnels WHERE host_id = ANY($1)`, []string{registered, orphan})
	})
	for _, hostID := range []string{registered, orphan} {
		if err := store.SaveRemoteHostTunnel(ctx, domain.RemoteHostTunnel{
			HostID: hostID, TunnelID: "tun-" + hostID, Hostname: "h-" + hostID + ".example.dev",
			EncryptedRunToken: []byte("sealed"), RunTokenNonce: []byte("nonce"),
		}); err != nil {
			t.Fatalf("save tunnel: %v", err)
		}
	}
	got, found, err := store.GetRemoteHostTunnel(ctx, registered)
	if err != nil || !found || got.TunnelID != "tun-"+registered || string(got.EncryptedRunToken) != "sealed" {
		t.Fatalf("get = %+v, %v, %v", got, found, err)
	}
	if _, found, err := store.GetRemoteHostTunnel(ctx, "h_"+uuid.NewString()); err != nil || found {
		t.Fatalf("unknown host = %v, %v", found, err)
	}

	// Fresh tunnels: only the orphan is a candidate.
	candidates, err := store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c := pick(candidates, registered, orphan); len(c) != 1 || c[0].HostID != orphan || c[0].Registered {
		t.Fatalf("fresh candidates = %+v", c)
	}
	// Past the cutoff, the registered one is a candidate too.
	candidates, err = store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c := pick(candidates, registered); len(c) != 1 || !c[0].Registered {
		t.Fatalf("stale candidates = %+v", c)
	}

	// A delete naming an older tunnel keeps the row.
	if err := store.DeleteRemoteHostTunnel(ctx, registered, "tun-older"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.GetRemoteHostTunnel(ctx, registered); !found {
		t.Fatal("delete of an older tunnel removed the current one")
	}
	if err := store.TouchRemoteHostTunnel(ctx, registered); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRemoteHostTunnel(ctx, registered, "tun-"+registered); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.GetRemoteHostTunnel(ctx, registered); found {
		t.Fatal("tunnel row survived its delete")
	}
}

func pick(candidates []domain.RemoteHostTunnelCandidate, hostIDs ...string) []domain.RemoteHostTunnelCandidate {
	var out []domain.RemoteHostTunnelCandidate
	for _, c := range candidates {
		for _, id := range hostIDs {
			if c.HostID == id {
				out = append(out, c)
			}
		}
	}
	return out
}
