package postgres

import (
	"context"
	"errors"
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
	// Another account's tunnel for the same host: that account lists nothing.
	otherUser := uuid.NewString()
	// Registered before anything can fail, so a failed run leaves no rows.
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_remote_hosts WHERE host_id = ANY($1)`, []string{registered, orphan})
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_remote_host_tunnels WHERE host_id = ANY($1)`, []string{registered, orphan})
	})
	if err := store.UpsertRemoteHost(ctx, domain.Principal{UserID: fixture.userID}, domain.RemoteHost{
		HostID: registered, Label: "Mac", URL: "https://h-x.example.dev", EncryptedToken: []byte{1}, TokenNonce: []byte{2},
	}, 1); err != nil {
		t.Fatalf("register host: %v", err)
	}
	// A full account still updates the machines it lists, and lists no more.
	if err := store.UpsertRemoteHost(ctx, domain.Principal{UserID: fixture.userID}, domain.RemoteHost{
		HostID: registered, Label: "Mac 2", URL: "https://h-x.example.dev", EncryptedToken: []byte{1}, TokenNonce: []byte{2},
	}, 1); err != nil {
		t.Fatalf("update a listed host on a full account: %v", err)
	}
	if err := store.UpsertRemoteHost(ctx, domain.Principal{UserID: fixture.userID}, domain.RemoteHost{
		HostID: orphan, Label: "Other", URL: "https://h-y.example.dev", EncryptedToken: []byte{1}, TokenNonce: []byte{2},
	}, 1); !errors.Is(err, ErrRemoteHostLimit) {
		t.Fatalf("new host on a full account = %v, want ErrRemoteHostLimit", err)
	}
	save := func(userID, hostID, tunnelID string, recordCreated bool) time.Time {
		t.Helper()
		since, err := store.SaveRemoteHostTunnel(ctx, domain.RemoteHostTunnel{
			UserID: userID, HostID: hostID, TunnelID: tunnelID, Hostname: "h-" + tunnelID + ".example.dev",
			EncryptedRunToken: []byte("sealed"), RunTokenNonce: []byte("nonce"),
		}, recordCreated)
		if err != nil {
			t.Fatalf("save tunnel: %v", err)
		}
		return since
	}
	current := "tun-" + registered
	made := save(fixture.userID, registered, current, true)
	save(fixture.userID, orphan, "tun-"+orphan, true)
	save(otherUser, registered, "tun-other-"+registered, true)

	got, found, err := store.GetRemoteHostTunnel(ctx, fixture.userID, registered)
	if err != nil || !found || got.TunnelID != current || string(got.EncryptedRunToken) != "sealed" || !got.CreatedAt.Equal(made) {
		t.Fatalf("get = %+v, %v, %v", got, found, err)
	}
	if other, _, _ := store.GetRemoteHostTunnel(ctx, otherUser, registered); other.TunnelID != "tun-other-"+registered {
		t.Fatalf("other account's tunnel = %+v", other)
	}
	if byID, found, err := store.FindRemoteHostTunnel(ctx, current); err != nil || !found || byID.UserID != fixture.userID || byID.HostID != registered {
		t.Fatalf("find = %+v, %v, %v", byID, found, err)
	}
	if _, found, err := store.GetRemoteHostTunnel(ctx, fixture.userID, "h_"+uuid.NewString()); err != nil || found {
		t.Fatalf("unknown host = %v, %v", found, err)
	}
	// The DNS record's age survives a repair that kept the record.
	if since := save(fixture.userID, registered, current, false); !since.Equal(made) {
		t.Fatalf("repair moved the record's age from %v to %v", made, since)
	}

	// Fresh tunnels no account lists are kept while their registration may
	// still be on its way.
	candidates, err := store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c := pick(candidates, current, "tun-"+orphan, "tun-other-"+registered); len(c) != 0 {
		t.Fatalf("fresh candidates = %+v", c)
	}
	// Later, the orphan and the other account's tunnel are candidates.
	candidates, err = store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c := pick(candidates, current, "tun-"+orphan, "tun-other-"+registered); len(c) != 2 || c[0].Registered || c[1].Registered ||
		len(pick(c, current)) != 0 {
		t.Fatalf("unregistered candidates = %+v", c)
	}
	// Past the cutoff, the registered one is a candidate too.
	candidates, err = store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(time.Hour), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c := pick(candidates, current); len(c) != 1 || !c[0].Registered {
		t.Fatalf("stale candidates = %+v", c)
	}

	// Released while its machine is offline: only a release naming the
	// current tunnel counts, and saving a new tunnel clears it.
	if err := store.ReleaseRemoteHostTunnel(ctx, "tun-older"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := store.GetRemoteHostTunnel(ctx, fixture.userID, registered); got.Released {
		t.Fatal("a release naming an older tunnel released the current one")
	}
	if err := store.ReleaseRemoteHostTunnel(ctx, current); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := store.GetRemoteHostTunnel(ctx, fixture.userID, registered); !got.Released {
		t.Fatal("release did not stick")
	}
	if candidates, err := store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(time.Hour), time.Now().Add(-time.Hour)); err != nil ||
		len(pick(candidates, current)) != 1 || !pick(candidates, current)[0].Released {
		t.Fatalf("released candidate = %+v, %v", pick(candidates, current), err)
	}
	current = "tun-new-" + registered
	if since := save(fixture.userID, registered, current, false); !since.Equal(made) {
		t.Fatal("a new tunnel under the kept record moved the record's age")
	}
	if got, _, _ := store.GetRemoteHostTunnel(ctx, fixture.userID, registered); got.Released || got.TunnelID != current {
		t.Fatalf("after a new tunnel: %+v", got)
	}

	// A delete naming an older tunnel keeps the row.
	if err := store.DeleteRemoteHostTunnel(ctx, "tun-"+registered); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.GetRemoteHostTunnel(ctx, fixture.userID, registered); !found {
		t.Fatal("delete of an older tunnel removed the current one")
	}
	if err := store.TouchRemoteHostTunnel(ctx, current, time.Now()); err != nil {
		t.Fatal(err)
	}
	if candidates, err := store.ListRemoteHostTunnelSweepCandidates(ctx, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil ||
		len(pick(candidates, current)) != 0 {
		t.Fatalf("a touched tunnel stayed a candidate: %+v, %v", candidates, err)
	}
	if err := store.DeleteRemoteHostTunnel(ctx, current); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.GetRemoteHostTunnel(ctx, fixture.userID, registered); found {
		t.Fatal("tunnel row survived its delete")
	}
}

func pick(candidates []domain.RemoteHostTunnelCandidate, tunnelIDs ...string) []domain.RemoteHostTunnelCandidate {
	var out []domain.RemoteHostTunnelCandidate
	for _, c := range candidates {
		for _, id := range tunnelIDs {
			if c.TunnelID == id {
				out = append(out, c)
			}
		}
	}
	return out
}
