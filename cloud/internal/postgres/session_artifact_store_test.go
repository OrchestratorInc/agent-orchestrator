package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

func testArtifact(path string, size int64) domain.SessionArtifact {
	sum := sha256.Sum256([]byte(path + fmt.Sprint(size)))
	return domain.SessionArtifact{
		Path:        path,
		Name:        path,
		Kind:        "file",
		ContentType: "application/octet-stream",
		Size:        size,
		SHA256:      hex.EncodeToString(sum[:]),
		S3Key:       "orgs/o/sessions/s/" + path,
		VersionID:   "v-" + path,
		MTime:       time.Now().UTC().Truncate(time.Microsecond),
	}
}

// commitTestArtifacts records each upsert as an open upload of the fixture's
// session and commits as its current worker.
func commitTestArtifacts(t *testing.T, store *Store, fixture notificationFixture, upserts []domain.SessionArtifact, deletes []string) error {
	t.Helper()
	ctx := context.Background()
	for _, artifact := range upserts {
		if err := store.RecordArtifactUpload(ctx, fixture.orgID, fixture.sessionID, artifact.S3Key, artifact.VersionID, artifact.Size); err != nil {
			t.Fatalf("record upload %s: %v", artifact.Path, err)
		}
	}
	return store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, upserts, deletes)
}

func artifactEventCount(t *testing.T, admin *pgxpool.Pool, sessionID string) int {
	t.Helper()
	var count int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM ao_events WHERE session_id = $1 AND type = $2`,
		sessionID, ArtifactChangedEventType).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func sessionOutputType(t *testing.T, admin *pgxpool.Pool, sessionID string) string {
	t.Helper()
	var outputType string
	if err := admin.QueryRow(context.Background(),
		`SELECT output_type FROM ao_sessions WHERE id = $1`, sessionID).Scan(&outputType); err != nil {
		t.Fatal(err)
	}
	return outputType
}

func TestSessionArtifactCommitListAndOutputType(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	report := testArtifact("report.html", 10)
	chart := testArtifact("assets/chart.png", 20)
	if err := commitTestArtifacts(t, store, fixture,
		[]domain.SessionArtifact{report, chart}, nil); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListSessionArtifacts(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Path != "assets/chart.png" || listed[1].Path != "report.html" {
		t.Fatalf("listed = %+v", listed)
	}
	if listed[1].VersionID != report.VersionID || listed[1].SHA256 != report.SHA256 {
		t.Fatalf("report row = %+v", listed[1])
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputArtifact {
		t.Fatalf("output type = %q, want artifact", got)
	}
	if got := artifactEventCount(t, admin, fixture.sessionID); got != 1 {
		t.Fatalf("artifact events = %d, want 1", got)
	}

	got, err := store.SessionArtifact(ctx, fixture.userID, fixture.orgID, fixture.sessionID, "report.html")
	if err != nil || got.S3Key != report.S3Key {
		t.Fatalf("SessionArtifact = %+v, %v", got, err)
	}
	if _, err := store.SessionArtifact(ctx, fixture.userID, fixture.orgID, fixture.sessionID, "missing.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artifact err = %v", err)
	}

	bySession, err := store.SessionArtifactsBySessions(ctx, fixture.orgID, []string{fixture.sessionID})
	if err != nil || len(bySession[fixture.sessionID]) != 2 {
		t.Fatalf("SessionArtifactsBySessions = %v, %v", bySession, err)
	}

	if _, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 7,
		"https://github.test/owner/repo/pull/7", "feature", "main", "sha-7", "Artifacts", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	updated := report
	updated.Size, updated.VersionID = 11, "v-report-2"
	if err := commitTestArtifacts(t, store, fixture,
		[]domain.SessionArtifact{updated}, nil); err != nil {
		t.Fatal(err)
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputPRAndArtifact {
		t.Fatalf("output type = %q, want pr_artifact", got)
	}

	if err := commitTestArtifacts(t, store, fixture,
		nil, []string{"report.html", "assets/chart.png"}); err != nil {
		t.Fatal(err)
	}
	listed, err = store.ListSessionArtifacts(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("after delete listed = %+v, %v", listed, err)
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputPR {
		t.Fatalf("output type = %q, want pr", got)
	}
}

func TestChangedSessionArtifacts(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	same := testArtifact("same.md", 5)
	resized := testArtifact("resized.md", 5)
	rehashed := testArtifact("rehashed.md", 5)
	if err := commitTestArtifacts(t, store, fixture,
		[]domain.SessionArtifact{same, resized, rehashed}, nil); err != nil {
		t.Fatal(err)
	}
	changed, err := store.ChangedSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, []domain.ArtifactManifestEntry{
		{Path: same.Path, Size: same.Size, SHA256: same.SHA256},
		{Path: resized.Path, Size: 6, SHA256: resized.SHA256},
		{Path: rehashed.Path, Size: rehashed.Size, SHA256: same.SHA256},
		{Path: "new.md", Size: 1, SHA256: same.SHA256},
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(changed)
	if !slices.Equal(changed, []string{"new.md", "rehashed.md", "resized.md"}) {
		t.Fatalf("changed = %v", changed)
	}
	if _, err := store.ChangedSessionArtifacts(ctx, fixture.orgID, "00000000-0000-0000-0000-000000000000", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session err = %v", err)
	}
}

func TestSessionArtifactLimits(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	tooMany := make([]domain.SessionArtifact, 0, domain.MaxSessionArtifactFiles+1)
	for index := range domain.MaxSessionArtifactFiles + 1 {
		tooMany = append(tooMany, testArtifact(fmt.Sprintf("f%03d.txt", index), 1))
	}
	if err := commitTestArtifacts(t, store, fixture, tooMany, nil); !errors.Is(err, ErrArtifactLimit) {
		t.Fatalf("over file cap err = %v, want ErrArtifactLimit", err)
	}
	if err := commitTestArtifacts(t, store, fixture, tooMany[:domain.MaxSessionArtifactFiles], nil); err != nil {
		t.Fatalf("at file cap: %v", err)
	}
	if err := commitTestArtifacts(t, store, fixture, nil, func() []string {
		paths := make([]string, 0, domain.MaxSessionArtifactFiles)
		for _, artifact := range tooMany[:domain.MaxSessionArtifactFiles] {
			paths = append(paths, artifact.Path)
		}
		return paths
	}()); err != nil {
		t.Fatal(err)
	}

	tooBig := []domain.SessionArtifact{
		testArtifact("a.bin", domain.MaxSessionArtifactBytes/2),
		testArtifact("b.bin", domain.MaxSessionArtifactBytes/2+1),
	}
	before := artifactEventCount(t, admin, fixture.sessionID)
	if err := commitTestArtifacts(t, store, fixture, tooBig, nil); !errors.Is(err, ErrArtifactLimit) {
		t.Fatalf("over byte cap err = %v, want ErrArtifactLimit", err)
	}
	listed, err := store.ListSessionArtifacts(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("rejected commit left rows: %+v, %v", listed, err)
	}
	if after := artifactEventCount(t, admin, fixture.sessionID); after != before {
		t.Fatalf("rejected commit appended an event: %d -> %d", before, after)
	}
}

// A commit that fails partway leaves no rows and no event.
func TestSessionArtifactCommitIsAllOrNothing(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	bad := testArtifact("bad.html", 1)
	bad.SHA256 = "not-hex"
	err := commitTestArtifacts(t, store, fixture,
		[]domain.SessionArtifact{testArtifact("good.html", 1), bad}, nil)
	if err == nil {
		t.Fatal("commit with an invalid row succeeded")
	}
	listed, err := store.ListSessionArtifacts(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("failed commit left rows: %+v, %v", listed, err)
	}
	if got := artifactEventCount(t, admin, fixture.sessionID); got != 0 {
		t.Fatalf("failed commit appended %d events", got)
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputNone {
		t.Fatalf("failed commit changed output type to %q", got)
	}
}

// One organization can never read another organization's artifact rows.
func TestSessionArtifactsAreIsolatedByOrganization(t *testing.T) {
	_, admin, owner := openNotificationTestStore(t)
	_, _, other := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	if err := commitTestArtifacts(t, store, owner,
		[]domain.SessionArtifact{testArtifact("secret.html", 1)}, nil); err != nil {
		t.Fatal(err)
	}
	otherPrincipal := domain.Principal{UserID: other.userID, Provider: "local"}

	if _, err := store.ListSessionArtifacts(ctx, otherPrincipal, other.orgID, owner.sessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org listing owner session err = %v, want ErrNotFound", err)
	}
	if _, err := store.ListSessionArtifacts(ctx, otherPrincipal, owner.orgID, owner.sessionID); err == nil {
		t.Fatal("non-member listed the owner organization's artifacts")
	}
	if _, err := store.SessionArtifact(ctx, other.userID, other.orgID, owner.sessionID, "secret.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org reading owner artifact err = %v, want ErrNotFound", err)
	}
	bySession, err := store.SessionArtifactsBySessions(ctx, other.orgID, []string{owner.sessionID})
	if err != nil || len(bySession) != 0 {
		t.Fatalf("other org batch read = %v, %v", bySession, err)
	}
	if err := store.CommitSessionArtifacts(ctx, other.orgID, owner.sessionID, other.workerID, other.epoch,
		[]domain.SessionArtifact{testArtifact("planted.html", 1)}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org committing to owner session err = %v, want ErrNotFound", err)
	}
	if _, err := store.ChangedSessionArtifacts(ctx, other.orgID, owner.sessionID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org manifest on owner session err = %v, want ErrNotFound", err)
	}
}

func TestArtifactUploadsAreCapped(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	for index := range domain.MaxPendingArtifactUploads {
		if err := store.RecordArtifactUpload(ctx, fixture.orgID, fixture.sessionID, "orgs/o/sessions/s/x", fmt.Sprintf("v%d", index), 1); err != nil {
			t.Fatalf("upload %d: %v", index, err)
		}
	}
	if allowed, err := store.ArtifactUploadAllowed(ctx, fixture.orgID, fixture.sessionID, 1); err != nil || allowed {
		t.Fatalf("ArtifactUploadAllowed at the cap = %v, %v", allowed, err)
	}
	if err := store.RecordArtifactUpload(ctx, fixture.orgID, fixture.sessionID, "orgs/o/sessions/s/x", "over", 1); !errors.Is(err, ErrArtifactLimit) {
		t.Fatalf("upload over the count cap err = %v, want ErrArtifactLimit", err)
	}
}

func TestArtifactCommitClaimsOpenUploadsAndQueuesReplacedVersions(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	report := testArtifact("report.html", 10)
	notes := testArtifact("notes.md", 5)
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		[]domain.SessionArtifact{report}, nil); !errors.Is(err, ErrArtifactVersionUnknown) {
		t.Fatalf("commit without an upload err = %v, want ErrArtifactVersionUnknown", err)
	}
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{report, notes}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		[]domain.SessionArtifact{report}, nil); !errors.Is(err, ErrArtifactVersionUnknown) {
		t.Fatalf("recommitting a committed version err = %v, want ErrArtifactVersionUnknown", err)
	}
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch+1,
		nil, []string{"notes.md"}); !errors.Is(err, ErrStaleWorker) {
		t.Fatalf("commit from a replaced worker err = %v, want ErrStaleWorker", err)
	}

	updated := report
	updated.VersionID, updated.Size = "v-report-2", 11
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{updated}, []string{"notes.md"}); err != nil {
		t.Fatal(err)
	}
	queued := map[string]string{}
	rows, err := admin.Query(ctx, `SELECT version_id, reason FROM ao_artifact_object_versions WHERE session_id = $1`, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var version, reason string
		if err := rows.Scan(&version, &reason); err != nil {
			t.Fatal(err)
		}
		queued[version] = reason
	}
	rows.Close()
	if len(queued) != 2 || queued[report.VersionID] != "replaced" || queued[notes.VersionID] != "replaced" {
		t.Fatalf("queued versions = %v, want the replaced report and the deleted notes", queued)
	}

	before := artifactEventCount(t, admin, fixture.sessionID)
	if err := commitTestArtifacts(t, store, fixture, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := commitTestArtifacts(t, store, fixture, nil, []string{"never-existed.md"}); err != nil {
		t.Fatal(err)
	}
	if after := artifactEventCount(t, admin, fixture.sessionID); after != before {
		t.Fatalf("commits that changed nothing appended events: %d -> %d", before, after)
	}
}

func TestArtifactSweeperQueue(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	upload := testArtifact("report.html", 3)
	if err := store.RecordArtifactUpload(ctx, fixture.orgID, fixture.sessionID, upload.S3Key, upload.VersionID, upload.Size); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimArtifactObjectVersions(ctx, 10, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("claimed an upload before it expired: %v, %v", claimed, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_artifact_object_versions SET collect_after = now() - interval '1 second' WHERE session_id = $1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimArtifactObjectVersions(ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].VersionID != upload.VersionID {
		t.Fatalf("claimed = %v, %v", claimed, err)
	}
	if again, err := store.ClaimArtifactObjectVersions(ctx, 10, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("claimed a leased version twice: %v, %v", again, err)
	}
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
		[]domain.SessionArtifact{upload}, nil); !errors.Is(err, ErrArtifactVersionUnknown) {
		t.Fatalf("committing a claimed version err = %v, want ErrArtifactVersionUnknown", err)
	}
	if err := store.ForgetArtifactObjectVersions(ctx, claimed); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_artifact_object_versions WHERE session_id = $1`, fixture.sessionID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("versions left after forget = %d, %v", left, err)
	}
}

func TestSessionArtifactNeedsAnActiveMember(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()

	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{testArtifact("report.html", 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionArtifact(ctx, fixture.userID, fixture.orgID, fixture.sessionID, "report.html"); err != nil {
		t.Fatalf("member read: %v", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_org_memberships SET status = 'disabled' WHERE org_id = $1 AND user_id = $2`, fixture.orgID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionArtifact(ctx, fixture.userID, fixture.orgID, fixture.sessionID, "report.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled member read err = %v, want ErrNotFound", err)
	}
}

// Two commits racing for the last free slots: the session lock lets exactly
// one through.
func TestSessionArtifactConcurrentCommitsRespectTheCap(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	batches := make([][]domain.SessionArtifact, 2)
	for batch := range batches {
		for index := range 150 {
			artifact := testArtifact(fmt.Sprintf("b%d/f%03d.txt", batch, index), 1)
			if err := store.RecordArtifactUpload(context.Background(), fixture.orgID, fixture.sessionID, artifact.S3Key, artifact.VersionID, 1); err != nil {
				t.Fatal(err)
			}
			batches[batch] = append(batches[batch], artifact)
		}
	}
	results := make(chan error, 2)
	for _, batch := range batches {
		go func() {
			results <- store.CommitSessionArtifacts(context.Background(), fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch, batch, nil)
		}()
	}
	succeeded, limited := 0, 0
	for range batches {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrArtifactLimit):
			limited++
		default:
			t.Fatalf("commit err = %v", err)
		}
	}
	if succeeded != 1 || limited != 1 {
		t.Fatalf("succeeded = %d, limited = %d; want 1 and 1", succeeded, limited)
	}
}

func TestSessionReadCountsPullRequestsIntoOutputType(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	if _, err := store.CreatePullRequestRecord(ctx, fixture.orgID, fixture.sessionID, "github", "owner/repo", "author", 9,
		"https://github.test/owner/repo/pull/9", "feature", "main", "sha-9", "PR", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	session, err := store.GetSession(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || session.OutputType != domain.SessionOutputPR {
		t.Fatalf("output type = %q, %v; want pr", session.OutputType, err)
	}
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{testArtifact("report.html", 1)}, nil); err != nil {
		t.Fatal(err)
	}
	session, err = store.GetSession(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || session.OutputType != domain.SessionOutputPRAndArtifact {
		t.Fatalf("output type = %q, %v; want pr_artifact", session.OutputType, err)
	}
}

// The artifacts migration can run again on a database that already has it,
// so it can be renumbered after reaching an environment.
func TestSessionArtifactsMigrationIsRepeatable(t *testing.T) {
	_, admin, _ := openNotificationTestStore(t)
	raw, err := os.ReadFile("migrations/00084_session_artifacts.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	if _, err := admin.Exec(context.Background(), up); err != nil {
		t.Fatalf("re-running the migration: %v", err)
	}
}

// Committing one path over and over leaves replaced versions queued for the
// sweeper; they count toward the same limit as open uploads.
func TestArtifactCommitLoopHitsThePendingCap(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	for index := range domain.MaxPendingArtifactUploads + 2 {
		artifact := testArtifact("report.html", 1)
		artifact.VersionID = fmt.Sprintf("v-%d", index)
		err := store.RecordArtifactUpload(ctx, fixture.orgID, fixture.sessionID, artifact.S3Key, artifact.VersionID, artifact.Size)
		if errors.Is(err, ErrArtifactLimit) {
			if index < domain.MaxPendingArtifactUploads-1 {
				t.Fatalf("limit reached after %d commits, want about %d", index, domain.MaxPendingArtifactUploads)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, fixture.workerID, fixture.epoch,
			[]domain.SessionArtifact{artifact}, nil); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("upload-and-commit loop never reached the pending limit")
}

func TestUserDeletesASessionArtifact(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	report := testArtifact("report.html", 10)
	notes := testArtifact("notes.md", 5)
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{report, notes}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteSessionArtifact(ctx, domain.Principal{UserID: uuid.NewString(), Provider: "local"}, fixture.orgID, fixture.sessionID, "report.html"); err == nil {
		t.Fatal("a non-member deleted an artifact")
	}

	version, err := store.DeleteSessionArtifact(ctx, principal, fixture.orgID, fixture.sessionID, "report.html")
	if err != nil {
		t.Fatal(err)
	}
	if version.S3Key != report.S3Key || version.VersionID != report.VersionID {
		t.Fatalf("deleted version = %+v", version)
	}
	listed, err := store.ListSessionArtifacts(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || len(listed) != 1 || listed[0].Path != "notes.md" {
		t.Fatalf("listed after delete = %+v, %v", listed, err)
	}
	var due bool
	if err := admin.QueryRow(ctx,
		`SELECT collect_after <= now() FROM ao_artifact_object_versions WHERE s3_key = $1 AND version_id = $2`,
		report.S3Key, report.VersionID).Scan(&due); err != nil || !due {
		t.Fatalf("the deleted version is not due for collection: %v, %v", due, err)
	}
	if got := artifactEventCount(t, admin, fixture.sessionID); got != 2 {
		t.Fatalf("artifact events = %d, want 2", got)
	}

	if _, err := store.DeleteSessionArtifact(ctx, principal, fixture.orgID, fixture.sessionID, "report.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting a deleted path err = %v", err)
	}
	if _, err := store.DeleteSessionArtifact(ctx, principal, fixture.orgID, fixture.sessionID, "notes.md"); err != nil {
		t.Fatal(err)
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputNone {
		t.Fatalf("output type after the last delete = %q, want none", got)
	}
}

func TestDeletingAProjectDeletesItsArtifacts(t *testing.T) {
	_, admin, fixture := openNotificationTestStore(t)
	store := openRLSStore(t, admin)
	ctx := context.Background()
	principal := domain.Principal{UserID: fixture.userID, Provider: "local"}

	report := testArtifact("report.html", 10)
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{report, testArtifact("notes.md", 5)}, nil); err != nil {
		t.Fatal(err)
	}
	// A newer report replaces the first, and one upload is still open.
	updated := report
	updated.Size, updated.VersionID = 11, "v-report-2"
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{updated}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordArtifactUpload(ctx, fixture.orgID, fixture.sessionID, "orgs/o/sessions/s/draft.md", "v-draft", 3); err != nil {
		t.Fatal(err)
	}

	if err := store.ArchiveProject(ctx, principal, fixture.orgID, fixture.projectID); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListSessionArtifacts(ctx, principal, fixture.orgID, fixture.sessionID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("artifacts after the project was deleted = %+v, %v", listed, err)
	}
	var queued, notDue int
	if err := admin.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE collect_after > now())
		FROM ao_artifact_object_versions WHERE session_id = $1`,
		fixture.sessionID).Scan(&queued, &notDue); err != nil {
		t.Fatal(err)
	}
	// report v1 (replaced), report v2, notes.md, and the open draft upload.
	if queued != 4 || notDue != 0 {
		t.Fatalf("queued versions = %d, %d not due now; want 4, all due", queued, notDue)
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputNone {
		t.Fatalf("output type = %q, want none", got)
	}
	if err := commitTestArtifacts(t, store, fixture, []domain.SessionArtifact{testArtifact("late.md", 2)}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a commit after the project was deleted = %v, want ErrNotFound", err)
	}
	// Deleting it again changes nothing and does not fail.
	if err := store.ArchiveProject(ctx, principal, fixture.orgID, fixture.projectID); err != nil {
		t.Fatal(err)
	}
}
