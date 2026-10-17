package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

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
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID,
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

	got, err := store.SessionArtifact(ctx, fixture.orgID, fixture.sessionID, "report.html")
	if err != nil || got.S3Key != report.S3Key {
		t.Fatalf("SessionArtifact = %+v, %v", got, err)
	}
	if _, err := store.SessionArtifact(ctx, fixture.orgID, fixture.sessionID, "missing.html"); !errors.Is(err, ErrNotFound) {
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
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID,
		[]domain.SessionArtifact{updated}, nil); err != nil {
		t.Fatal(err)
	}
	if got := sessionOutputType(t, admin, fixture.sessionID); got != domain.SessionOutputPRAndArtifact {
		t.Fatalf("output type = %q, want pr_artifact", got)
	}

	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID,
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
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID,
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
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, tooMany, nil); !errors.Is(err, ErrArtifactLimit) {
		t.Fatalf("over file cap err = %v, want ErrArtifactLimit", err)
	}
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, tooMany[:domain.MaxSessionArtifactFiles], nil); err != nil {
		t.Fatalf("at file cap: %v", err)
	}
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, nil, func() []string {
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
	if err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID, tooBig, nil); !errors.Is(err, ErrArtifactLimit) {
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
	err := store.CommitSessionArtifacts(ctx, fixture.orgID, fixture.sessionID,
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

	if err := store.CommitSessionArtifacts(ctx, owner.orgID, owner.sessionID,
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
	if _, err := store.SessionArtifact(ctx, other.orgID, owner.sessionID, "secret.html"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org reading owner artifact err = %v, want ErrNotFound", err)
	}
	bySession, err := store.SessionArtifactsBySessions(ctx, other.orgID, []string{owner.sessionID})
	if err != nil || len(bySession) != 0 {
		t.Fatalf("other org batch read = %v, %v", bySession, err)
	}
	if err := store.CommitSessionArtifacts(ctx, other.orgID, owner.sessionID,
		[]domain.SessionArtifact{testArtifact("planted.html", 1)}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org committing to owner session err = %v, want ErrNotFound", err)
	}
	if _, err := store.ChangedSessionArtifacts(ctx, other.orgID, owner.sessionID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org manifest on owner session err = %v, want ErrNotFound", err)
	}
}
