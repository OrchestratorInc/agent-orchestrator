package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

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

func TestArtifactShareLimitHoldsUnderConcurrency(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	created := make([]domain.ArtifactShare, attempts)
	for index := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created[index], results[index] = store.CreateArtifactShare(ctx,
				testShare(fixture.userID, "page.html"), "proj-feat-dev-example-com", 3)
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

	share, err := store.CreateArtifactShare(ctx, testShare(fixture.userID, "report.html"), "proj-feat-a", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArtifactShare(ctx, otherUserID, share.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user get err = %v, want ErrNotFound", err)
	}
	stolen := share
	stolen.UserID = otherUserID
	stolen.VersionID = "v-stolen"
	if _, _, err := store.UpdateArtifactShare(ctx, stolen); !errors.Is(err, ErrNotFound) {
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
	updated, previous, err := store.UpdateArtifactShare(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if previous != share.VersionID || updated.VersionID != "v2" || updated.Slug != share.Slug || updated.Size != 99 {
		t.Fatalf("update = %+v, previous %q", updated, previous)
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
}
