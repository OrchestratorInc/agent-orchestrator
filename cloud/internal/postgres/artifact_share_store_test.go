package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func testShare(userID, name string) domain.ArtifactShare {
	id := uuid.NewString()
	sum := sha256.Sum256([]byte(name))
	return domain.ArtifactShare{
		ID:          id,
		UserID:      userID,
		Name:        name,
		ContentType: "text/html; charset=utf-8",
		Size:        int64(len(name)),
		SHA256:      hex.EncodeToString(sum[:]),
		S3Key:       "shares/" + id,
		VersionID:   "v1-" + id,
	}
}

// recordedShare returns a share whose upload is recorded in the sweeper's
// queue, as the handler does after storing the object.
func recordedShare(t *testing.T, store *Store, userID, name string) domain.ArtifactShare {
	t.Helper()
	share := testShare(userID, name)
	if err := store.RecordArtifactShareUpload(context.Background(), userID, share.S3Key, share.VersionID, share.Size); err != nil {
		t.Fatal(err)
	}
	return share
}

func queuedShareVersions(t *testing.T, admin *pgxpool.Pool, s3Key string) map[string]string {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT version_id, reason FROM ao_artifact_object_versions WHERE s3_key = $1`, s3Key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	queued := map[string]string{}
	for rows.Next() {
		var version, reason string
		if err := rows.Scan(&version, &reason); err != nil {
			t.Fatal(err)
		}
		queued[version] = reason
	}
	return queued
}

func TestArtifactShareLimitHoldsUnderConcurrency(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	created := make([]domain.ArtifactShare, attempts)
	shares := make([]domain.ArtifactShare, attempts)
	for index := range attempts {
		shares[index] = recordedShare(t, store, fixture.userID, "page.html")
	}
	for index := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created[index], results[index] = store.CreateArtifactShare(ctx, shares[index], "proj-feat-dev-example-com", 3)
		}()
	}
	wg.Wait()

	slugs := map[string]bool{}
	for index, err := range results {
		switch {
		case err == nil:
			slugs[created[index].Slug] = true
		case !errors.Is(err, ErrArtifactShareLimit):
			t.Fatalf("attempt %d: %v", index, err)
		}
	}
	if len(slugs) != 3 {
		t.Fatalf("created %d shares with slugs %v, want exactly 3", len(slugs), slugs)
	}
	for _, slug := range []string{"proj-feat-dev-example-com", "proj-feat-dev-example-com-2", "proj-feat-dev-example-com-3"} {
		if !slugs[slug] {
			t.Fatalf("missing slug %q in %v", slug, slugs)
		}
	}
	if count, err := store.CountArtifactShares(ctx, fixture.userID); err != nil || count != 3 {
		t.Fatalf("count = %d, %v", count, err)
	}
}

func TestArtifactShareOwnershipUpdateAndDelete(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	var otherUserID string
	if err := admin.QueryRow(ctx,
		`INSERT INTO ao_users (auth_provider, external_user_id, email, password_hash)
		VALUES ('local', $1, $2, 'hash') RETURNING id::text`,
		"share-other-"+uuid.NewString(), uuid.NewString()+"@example.com",
	).Scan(&otherUserID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateArtifactShare(ctx, testShare(fixture.userID, "unrecorded.html"), "proj-feat-a", 3); !errors.Is(err, ErrArtifactVersionUnknown) {
		t.Fatalf("create without a recorded upload err = %v, want ErrArtifactVersionUnknown", err)
	}
	share, err := store.CreateArtifactShare(ctx, recordedShare(t, store, fixture.userID, "report.html"), "proj-feat-a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if queued := queuedShareVersions(t, admin, share.S3Key); len(queued) != 0 {
		t.Fatalf("a served version is still queued: %v", queued)
	}
	if _, err := store.GetArtifactShare(ctx, otherUserID, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user get err = %v, want ErrNotFound", err)
	}
	stolen := share
	stolen.UserID = otherUserID
	stolen.VersionID = "v-stolen"
	if _, err := store.UpdateArtifactShare(ctx, stolen); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user update err = %v, want ErrNotFound", err)
	}
	if _, err := store.DeleteArtifactShare(ctx, otherUserID, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user delete err = %v, want ErrNotFound", err)
	}
	if listed, err := store.ListArtifactShares(ctx, otherUserID); err != nil || len(listed) != 0 {
		t.Fatalf("other user list = %+v, %v", listed, err)
	}

	next := share
	next.VersionID = "v2"
	next.Size = 99
	if _, err := store.UpdateArtifactShare(ctx, next); !errors.Is(err, ErrArtifactVersionUnknown) {
		t.Fatalf("update without a recorded upload err = %v, want ErrArtifactVersionUnknown", err)
	}
	if err := store.RecordArtifactShareUpload(ctx, fixture.userID, share.S3Key, "v2", 99); err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateArtifactShare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if updated.VersionID != "v2" || updated.Slug != share.Slug || updated.Size != 99 {
		t.Fatalf("update = %+v", updated)
	}
	if queued := queuedShareVersions(t, admin, share.S3Key); len(queued) != 1 || queued[share.VersionID] != "replaced" {
		t.Fatalf("queue after update = %v, want only the replaced version", queued)
	}
	served, err := store.ArtifactShareBySlug(ctx, share.Slug)
	if err != nil || served.VersionID != "v2" {
		t.Fatalf("by slug = %+v, %v", served, err)
	}

	deleted, err := store.DeleteArtifactShare(ctx, fixture.userID, share.ID)
	if err != nil || deleted.VersionID != "v2" {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}
	if _, err := store.ArtifactShareBySlug(ctx, share.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted slug err = %v, want ErrNotFound", err)
	}
	if count, err := store.CountArtifactShares(ctx, fixture.userID); err != nil || count != 0 {
		t.Fatalf("count after delete = %d, %v", count, err)
	}
	if queued := queuedShareVersions(t, admin, share.S3Key); len(queued) != 2 || queued["v2"] != "replaced" {
		t.Fatalf("queue after delete = %v, want both versions replaced", queued)
	}
}

// Share versions use the session artifacts' sweeper queue: once due, the
// sweeper claims and forgets them like any other version.
func TestArtifactShareVersionsReachTheSweeper(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	share := recordedShare(t, store, fixture.userID, "orphan.html")
	if err := store.DiscardArtifactShareUpload(ctx, fixture.userID, share.S3Key, share.VersionID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimArtifactObjectVersions(ctx, 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, version := range claimed {
		if version.S3Key == share.S3Key && version.VersionID == share.VersionID {
			found = true
		}
	}
	if !found {
		t.Fatalf("discarded share upload not claimed: %+v", claimed)
	}
	if err := store.ForgetArtifactObjectVersions(ctx, []domain.ArtifactObjectVersion{{S3Key: share.S3Key, VersionID: share.VersionID}}); err != nil {
		t.Fatal(err)
	}
	if queued := queuedShareVersions(t, admin, share.S3Key); len(queued) != 0 {
		t.Fatalf("forgotten version still queued: %v", queued)
	}
	// Another user's transaction cannot see or claim this user's queue rows.
	var visible int
	if err := store.withUser(ctx, uuid.NewString(), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM ao_artifact_object_versions WHERE user_id = $1`, fixture.userID).Scan(&visible)
	}); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("another user sees %d of this user's queued versions", visible)
	}
}
