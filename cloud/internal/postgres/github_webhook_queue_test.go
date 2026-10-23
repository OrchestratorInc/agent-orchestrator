package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSCMWebhookDoesNotWaitForRetryingDelivery(t *testing.T) {
	store, admin, _ := openNotificationTestStore(t)
	ctx := context.Background()
	installationID := time.Now().UnixNano()
	first, second := uuid.NewString(), uuid.NewString()
	for _, row := range []struct {
		id, status     string
		received, next time.Time
	}{
		{first, "retry", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().Add(time.Hour)},
		{second, "pending", time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		if _, err := admin.Exec(ctx, `INSERT INTO ao_github_webhook_deliveries
			(github_delivery_id, event, github_installation_id, payload, payload_hash, status, received_at, next_attempt_at)
			VALUES ($1, 'pull_request', $2, '{}'::bytea, decode(repeat('00', 32), 'hex'), $3, $4, $5)`,
			row.id, installationID, row.status, row.received, row.next); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_github_webhook_deliveries WHERE github_delivery_id = ANY($1)`, []string{first, second})
	})
	got, err := store.ClaimGitHubWebhook(ctx, "queue-test", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveryID != second {
		t.Fatalf("claimed %q, want newer ready SCM delivery %q", got.DeliveryID, second)
	}
}

func TestRecentOpenedPullRequestWebhooksReplaysProcessedDelivery(t *testing.T) {
	store, admin, _ := openNotificationTestStore(t)
	ctx := context.Background()
	repositoryID := time.Now().UnixNano()
	deliveryID := uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_github_webhook_deliveries
		(github_delivery_id, event, action, github_repository_id, payload, payload_hash, status)
		VALUES ($1, 'pull_request', 'opened', $2, $3, decode(repeat('00', 32), 'hex'), 'processed')`,
		deliveryID, repositoryID, []byte(`{"pull_request":{"number":7}}`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_github_webhook_deliveries WHERE github_delivery_id = $1`, deliveryID)
	})
	deliveries, err := store.RecentOpenedPullRequestWebhooks(ctx, repositoryID, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].DeliveryID != deliveryID {
		t.Fatalf("replayed deliveries = %+v", deliveries)
	}
}

func TestPruneGitHubWebhooksKeepsRecentAndInFlightDeliveries(t *testing.T) {
	store, admin, _ := openNotificationTestStore(t)
	ctx := context.Background()
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := old.Add(time.Hour)
	ids := map[string]struct {
		status   string
		received time.Time
		kept     bool
	}{
		uuid.NewString(): {"processed", old, false},
		uuid.NewString(): {"failed", old, false},
		uuid.NewString(): {"processed", cutoff.Add(time.Minute), true},
		uuid.NewString(): {"pending", old, true},
		uuid.NewString(): {"retry", old, true},
		uuid.NewString(): {"processing", old, true},
	}
	all := make([]string, 0, len(ids))
	for id, row := range ids {
		all = append(all, id)
		if _, err := admin.Exec(ctx, `INSERT INTO ao_github_webhook_deliveries
			(github_delivery_id, event, payload, payload_hash, status, received_at)
			VALUES ($1, 'check_run', '{}'::bytea, decode(repeat('00', 32), 'hex'), $2, $3)`,
			id, row.status, row.received); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM ao_github_webhook_deliveries WHERE github_delivery_id = ANY($1)`, all)
	})
	// Other tests' rows may also be old and finished, so count only ours.
	for {
		deleted, err := store.PruneGitHubWebhooks(ctx, cutoff, 1)
		if err != nil {
			t.Fatal(err)
		}
		if deleted == 0 {
			break
		}
	}
	for id, row := range ids {
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ao_github_webhook_deliveries WHERE github_delivery_id = $1)`, id).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != row.kept {
			t.Fatalf("%s delivery received %v: kept = %v, want %v", row.status, row.received, exists, row.kept)
		}
	}
}

func TestGitHubWebhookClaimIndexesExist(t *testing.T) {
	_, admin, _ := openNotificationTestStore(t)
	for _, name := range []string{"ao_github_webhook_install_order_idx", "ao_github_webhook_processing_idx", "ao_access_tickets_session_purpose_idx"} {
		var valid bool
		if err := admin.QueryRow(context.Background(), `SELECT i.indisvalid FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = $1`, name).Scan(&valid); err != nil {
			t.Fatalf("index %s: %v", name, err)
		}
		if !valid {
			t.Fatalf("index %s is not valid", name)
		}
	}
}
