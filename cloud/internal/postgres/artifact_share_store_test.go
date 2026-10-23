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
			created[index], _, results[index] = store.PublishArtifactShare(ctx, shares[index], "proj-feat-dev-example-com",
				domain.ArtifactShareDocumentKey("proj", "feat", shares[index].ID+".html"), 3)
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
	if listed, err := store.ListArtifactShares(ctx, fixture.userID); err != nil || len(listed) != 3 {
		t.Fatalf("listed = %d, %v", len(listed), err)
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

	if _, _, err := store.PublishArtifactShare(ctx, testShare(fixture.userID, "unrecorded.html"), "proj-feat-a", "proj-feat-a/unrecorded.html", 3); !errors.Is(err, ErrArtifactVersionUnknown) {
		t.Fatalf("create without a recorded upload err = %v, want ErrArtifactVersionUnknown", err)
	}
	share, _, err := store.PublishArtifactShare(ctx, recordedShare(t, store, fixture.userID, "report.html"), "proj-feat-a", "proj-feat-a/report.html", 3)
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
	if _, err := store.RevokeArtifactShare(ctx, otherUserID, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user revoke err = %v, want ErrNotFound", err)
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
	renamed := next
	renamed.Name = "renamed.html"
	if _, err := store.UpdateArtifactShare(ctx, renamed); !errors.Is(err, ErrArtifactShareNameChanged) {
		t.Fatalf("update with another name err = %v, want ErrArtifactShareNameChanged", err)
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

	revoked, err := store.RevokeArtifactShare(ctx, fixture.userID, share.ID)
	if err != nil || revoked.VersionID != "v2" || revoked.RevokedAt == nil {
		t.Fatalf("revoke = %+v, %v", revoked, err)
	}
	if _, err := store.RevokeArtifactShare(ctx, fixture.userID, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoking twice err = %v, want ErrNotFound", err)
	}
	if _, err := store.ArtifactShareBySlug(ctx, share.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked slug err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetArtifactShare(ctx, fixture.userID, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get revoked err = %v, want ErrNotFound", err)
	}
	listed, err := store.ListArtifactShares(ctx, fixture.userID)
	if err != nil || len(listed) != 1 || listed[0].RevokedAt == nil {
		t.Fatalf("listed after revoke = %+v, %v; want the revoked link still listed", listed, err)
	}
	if queued := queuedShareVersions(t, admin, share.S3Key); len(queued) != 2 || queued["v2"] != "replaced" {
		t.Fatalf("queue after revoke = %v, want both versions replaced", queued)
	}
	// A revoked link keeps its slot: at a limit of 1 a new document is
	// refused, and publishing the revoked document again restores it.
	if allowed, err := store.ArtifactSharePublishAllowed(ctx, fixture.userID, "proj-feat-a/new.html", 1); err != nil || allowed {
		t.Fatalf("new document after a revoke allowed = %v, %v", allowed, err)
	}
	if _, _, err := store.PublishArtifactShare(ctx, recordedShare(t, store, fixture.userID, "new.html"), "proj-feat-a", "proj-feat-a/new.html", 1); !errors.Is(err, ErrArtifactShareLimit) {
		t.Fatalf("new document after a revoke err = %v, want ErrArtifactShareLimit", err)
	}
	again := recordedShare(t, store, fixture.userID, "report.html")
	restored, replaced, err := store.PublishArtifactShare(ctx, again, "proj-feat-a", "proj-feat-a/report.html", 1)
	if err != nil || !replaced || restored.ID != share.ID || restored.RevokedAt != nil || restored.VersionID != again.VersionID {
		t.Fatalf("restore = %+v, replaced %v, %v", restored, replaced, err)
	}
	if served, err := store.ArtifactShareBySlug(ctx, share.Slug); err != nil || served.VersionID != again.VersionID {
		t.Fatalf("restored slug = %+v, %v", served, err)
	}
	if queued := queuedShareVersions(t, admin, share.S3Key); len(queued) != 2 {
		t.Fatalf("restoring queued the revoked version again: %v", queued)
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

// Concurrent publishes of one document make one link: the first inserts it,
// every other replaces its version, and each replaced version is queued.
func TestArtifactSharePublishingOneDocumentConcurrentlyMakesOneLink(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	const attempts = 6
	const documentKey = "proj-feat-dev-example-com/report.html"
	shares := make([]domain.ArtifactShare, attempts)
	for index := range attempts {
		shares[index] = recordedShare(t, store, fixture.userID, "report.html")
	}
	var wg sync.WaitGroup
	results := make([]error, attempts)
	replaced := make([]bool, attempts)
	published := make([]domain.ArtifactShare, attempts)
	for index := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			published[index], replaced[index], results[index] = store.PublishArtifactShare(ctx, shares[index], "proj-feat-dev-example-com", documentKey, 1)
		}()
	}
	wg.Wait()
	inserted := 0
	for index, err := range results {
		if err != nil {
			t.Fatalf("publish %d: %v", index, err)
		}
		if !replaced[index] {
			inserted++
		}
		if published[index].Slug != "proj-feat-dev-example-com" {
			t.Fatalf("publish %d slug = %q", index, published[index].Slug)
		}
	}
	if inserted != 1 {
		t.Fatalf("%d publishes inserted a link, want 1", inserted)
	}
	listed, err := store.ListArtifactShares(ctx, fixture.userID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed = %d, %v", len(listed), err)
	}
	var queued int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM ao_artifact_object_versions WHERE user_id = $1 AND reason = 'replaced'`,
		fixture.userID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != attempts-1 {
		t.Fatalf("queued replaced versions = %d, want %d", queued, attempts-1)
	}
	allowed, err := store.ArtifactSharePublishAllowed(ctx, fixture.userID, documentKey, 1)
	if err != nil || !allowed {
		t.Fatalf("republishing at the limit allowed = %v, %v", allowed, err)
	}
	if allowed, err := store.ArtifactSharePublishAllowed(ctx, fixture.userID, "proj-feat-dev-example-com/other.html", 1); err != nil || allowed {
		t.Fatalf("a new document at the limit allowed = %v, %v", allowed, err)
	}
}

// A POST replacing a document's version must see a PUT that committed while
// it waited, so the version that PUT installed is queued too and never lost.
func TestArtifactSharePublishSeesAConcurrentPut(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	const documentKey = "race-document"

	a := recordedShare(t, store, fixture.userID, "report.html")
	share, _, err := store.PublishArtifactShare(ctx, a, "proj-feat-a", documentKey, 3)
	if err != nil {
		t.Fatal(err)
	}

	// The "PUT": lock the link's row, install version B and queue A, as
	// UpdateArtifactShare does, and hold the transaction open.
	put, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer put.Rollback(ctx)
	if _, err := put.Exec(ctx, `SELECT 1 FROM ao_artifact_shares WHERE id = $1 FOR UPDATE`, share.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := put.Exec(ctx, `UPDATE ao_artifact_shares SET version_id = 'v-b' WHERE id = $1`, share.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := put.Exec(ctx,
		`INSERT INTO ao_artifact_object_versions (user_id, s3_key, version_id, reason, size, collect_after)
		VALUES ($1, $2, $3, 'replaced', 1, now() + interval '1 hour')`,
		fixture.userID, share.S3Key, share.VersionID,
	); err != nil {
		t.Fatal(err)
	}

	// The POST of the same document starts while the PUT holds the row.
	c := recordedShare(t, store, fixture.userID, "report.html")
	done := make(chan error, 1)
	go func() {
		_, _, err := store.PublishArtifactShare(ctx, c, "proj-feat-a", documentKey, 3)
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if err := put.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	queued := queuedShareVersions(t, admin, share.S3Key)
	if queued[share.VersionID] != "replaced" || queued["v-b"] != "replaced" {
		t.Fatalf("queue = %v, want both the original version and the PUT's v-b queued", queued)
	}
	served, err := store.ArtifactShareBySlug(ctx, share.Slug)
	if err != nil || served.VersionID != c.VersionID {
		t.Fatalf("served = %+v, %v; want the POST's version", served, err)
	}
}
