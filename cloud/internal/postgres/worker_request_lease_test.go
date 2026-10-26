package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A model catalog can take its whole 20 s discovery budget on a cold worker.
// With the default lease it was claimed again mid-discovery, started another
// one, and was failed TRANSPORT_TIMEOUT after three claims while the control
// plane was still waiting, so it gets the long lease a Chat steer has.
func TestChatModelsClaimOutlastsDiscovery(t *testing.T) {
	store, admin, fixture := openNotificationTestStore(t)
	ctx := context.Background()
	for _, kind := range []string{"chat.models", "workspace.review.summary"} {
		requestID := uuid.NewString()
		if _, err := admin.Exec(ctx, `INSERT INTO ao_worker_requests
			(id, org_id, session_id, worker_epoch, kind, status, attempt_count, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'pending', 0, now() + interval '1 minute')`,
			requestID, fixture.orgID, fixture.sessionID, fixture.epoch, kind); err != nil {
			t.Fatal(err)
		}
		request, found, err := store.ClaimWorkerRequest(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, 10*time.Second)
		if err != nil || !found || request.ID != requestID {
			t.Fatalf("%s claim = %+v found=%v err=%v", kind, request, found, err)
		}
		var seconds float64
		if err := admin.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM lease_until - now())::float8
			FROM ao_worker_requests WHERE id = $1`, requestID).Scan(&seconds); err != nil {
			t.Fatal(err)
		}
		lease := time.Duration(seconds * float64(time.Second))
		want := 10 * time.Second
		if kind == "chat.models" {
			want = 30 * time.Second
		}
		if lease < want-2*time.Second || lease > want+time.Second {
			t.Fatalf("%s lease = %v, want about %v", kind, lease, want)
		}
		if err := store.CompleteWorkerRequest(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, request.ID, fixture.epoch, request.Attempt, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
}
