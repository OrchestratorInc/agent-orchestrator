package postgres

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

func TestAttachmentsTaskChatSteerIsolationAndRetries(t *testing.T) {
	store, admin, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	_, err := admin.Exec(ctx, `UPDATE ao_worker_connections SET capabilities='["attachments.images.v1"]' WHERE session_id=$1`, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	makeAttachment := func(session, key string, size int64) domain.Attachment {
		t.Helper()
		a, err := store.PrepareAttachment(ctx, p, f.orgID, key, domain.PrepareAttachment{ProjectID: f.projectID, SessionID: session, Metadata: attachments.Metadata{Filename: "test.png", Size: size, MIMEType: "image/png", SHA256: strings.Repeat("a", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := makeAttachment("", "new-task", 100)
	repeated := makeAttachment("", "new-task", 100)
	if a.ID != repeated.ID {
		t.Fatal("upload retry duplicated attachment")
	}
	_, err = store.PrepareAttachment(ctx, p, f.orgID, "new-task", domain.PrepareAttachment{ProjectID: f.projectID, Metadata: attachments.Metadata{Filename: "other.png", Size: 100, MIMEType: "image/png", SHA256: strings.Repeat("a", 64)}})
	if !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatal("upload mismatch", err)
	}
	input := domain.CreateSession{ProjectID: f.projectID, Kind: "worker", Harness: "codex", DisplayName: "Image only", Provider: "docker", Mode: "trusted", AttachmentIDs: []string{a.ID}}
	if _, err := store.CreateSession(ctx, p, f.orgID, "create-task", 100, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("launched unverified attachment", err)
	}
	if err := readyAttachment(store, ctx, p, f.orgID, a.ID); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateSession(ctx, p, f.orgID, "create-task", 100, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.CreateSession(ctx, p, f.orgID, "create-task", 100, input)
	if err != nil || retry.ID != task.ID {
		t.Fatal("task retry", err)
	}
	var links int
	admin.QueryRow(ctx, `SELECT count(*) FROM ao_message_attachments WHERE session_id=$1`, task.ID).Scan(&links)
	if links != 1 {
		t.Fatal("missing initial image relationship", links)
	}
	b := makeAttachment(f.sessionID, "chat-image", 200)
	if err := readyAttachment(store, ctx, p, f.orgID, b.ID); err != nil {
		t.Fatal(err)
	}
	event, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, "message", "", domain.ChatTurnSettings{AttachmentIDs: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	same, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, "message", "", domain.ChatTurnSettings{AttachmentIDs: []string{b.ID}})
	if err != nil || same.Sequence != event.Sequence {
		t.Fatal("message retry", err)
	}
	_, err = store.SendMessage(ctx, p, f.orgID, f.sessionID, "message", "", domain.ChatTurnSettings{})
	if !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatal("IDs not part of idempotency", err)
	}
	turn, claimed, err := store.ClaimWorkerTurn(ctx, f.orgID, f.sessionID, f.workerID, f.epoch)
	if err != nil || !claimed || len(turn.Attachments) != 1 || turn.Attachments[0].ID != b.ID {
		t.Fatal("image-only delivery", turn, claimed, err)
	}
	steer, err := store.SteerTurnWithAttachments(ctx, p, f.orgID, f.sessionID, turn.ID, "steer", "", []string{b.ID})
	if err != nil || !strings.Contains(string(steer.Payload), b.ID) {
		t.Fatal("image steering", err)
	}
	same, err = store.SteerTurnWithAttachments(ctx, p, f.orgID, f.sessionID, turn.ID, "steer", "", []string{b.ID})
	if err != nil || same.Sequence != steer.Sequence {
		t.Fatal("steering retry", err)
	}
	manifest, err := store.WorkerAttachments(ctx, f.orgID, f.sessionID, f.workerID, f.epoch)
	if err != nil || len(manifest) != 1 {
		t.Fatal("manifest", manifest, err)
	}
	if _, err := store.WorkerAttachments(ctx, f.orgID, f.sessionID, f.workerID, f.epoch-1); !errors.Is(err, ErrStaleWorker) {
		t.Fatal("stale worker reads", err)
	}
	if _, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, "foreign", "", domain.ChatTurnSettings{AttachmentIDs: []string{a.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign session attachment", err)
	}
	if _, err := store.GetAttachment(ctx, p, uuid.NewString(), b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign org attachment", err)
	}
	// Referenced images remain available after the upload TTL, including archive.
	_, err = admin.Exec(ctx, `UPDATE ao_attachments SET expires_at=now()-interval '2 days' WHERE id=$1`, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAttachment(ctx, p, f.orgID, b.ID); err != nil {
		t.Fatal("referenced image expired", err)
	}
	_, err = admin.Exec(ctx, `UPDATE ao_worker_connections SET capabilities='[]' WHERE session_id=$1`, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkerAttachments(ctx, f.orgID, f.sessionID, f.workerID, f.epoch); !errors.Is(err, ErrImageWorkerUpgrade) {
		t.Fatal("old reconnect worker silently skipped the manifest", err)
	}
	if _, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, "old-worker", "image", domain.ChatTurnSettings{AttachmentIDs: []string{b.ID}}); !errors.Is(err, ErrImageWorkerUpgrade) {
		t.Fatal("old worker silently accepted image", err)
	}
	if _, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, "plain-text", "still works", domain.ChatTurnSettings{}); err != nil {
		t.Fatal("text-only regression", err)
	}
}
func TestAttachmentsSubmissionLimitsAndTUIFastPath(t *testing.T) {
	store, admin, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	admin.Exec(ctx, `UPDATE ao_worker_connections SET capabilities='["attachments.images.v1"]' WHERE session_id=$1`, f.sessionID)
	var ids []string
	for i := 0; i < 9; i++ {
		a, err := store.PrepareAttachment(ctx, p, f.orgID, uuid.NewString(), domain.PrepareAttachment{ProjectID: f.projectID, SessionID: f.sessionID, Metadata: attachments.Metadata{Filename: "large.png", Size: 10 << 20, MIMEType: "image/png", SHA256: strings.Repeat("b", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := readyAttachment(store, ctx, p, f.orgID, a.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	for _, submission := range [][]string{ids, ids[:3], {ids[0], ids[0]}, {ids[0], strings.ToUpper(ids[0])}, {uuid.NewString()}} {
		if _, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, uuid.NewString(), "image", domain.ChatTurnSettings{AttachmentIDs: submission}); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid submission", err)
		}
	}
	// An open TUI must still queue image work, not emit text-only terminal.input.
	terminalID := uuid.NewString()
	_, err := admin.Exec(ctx, `INSERT INTO ao_terminal_sessions(id,org_id,session_id,worker_epoch,kind,state,expires_at) VALUES($1,$2,$3,$4,'agent','open',$5)`, terminalID, f.orgID, f.sessionID, f.epoch, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SendMessage(ctx, p, f.orgID, f.sessionID, "tui-image", "", domain.ChatTurnSettings{AttachmentIDs: ids[:2]}); err != nil {
		t.Fatal(err)
	}
	var turns, inputs int
	admin.QueryRow(ctx, `SELECT count(*) FROM ao_turns WHERE session_id=$1`, f.sessionID).Scan(&turns)
	admin.QueryRow(ctx, `SELECT count(*) FROM ao_worker_requests WHERE session_id=$1 AND kind='terminal.input'`, f.sessionID).Scan(&inputs)
	if turns != 1 || inputs != 0 {
		t.Fatal("image used text-only fast path", turns, inputs)
	}
}

type cleanupStorage struct {
	attachments.Storage
	deleted []string
}

func (s *cleanupStorage) Delete(_ context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	return nil
}
func TestAttachmentCleanupRespectsReferencesAndLastGrant(t *testing.T) {
	store, admin, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	admin.Exec(ctx, `UPDATE ao_worker_connections SET capabilities='["attachments.images.v1"]' WHERE session_id=$1`, f.sessionID)
	create := func(key string) domain.Attachment {
		a, err := store.PrepareAttachment(ctx, p, f.orgID, key, domain.PrepareAttachment{ProjectID: f.projectID, SessionID: f.sessionID, Metadata: attachments.Metadata{Filename: "image.png", Size: 100, MIMEType: "image/png", SHA256: strings.Repeat("a", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	expired, retained := create("expired"), create("retained")
	if err := readyAttachment(store, ctx, p, f.orgID, retained.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LeaseAttachments(ctx, p, f.orgID, f.sessionID, []string{retained.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainAttachments(ctx, p, f.orgID, f.sessionID, []string{retained.ID}, f.workerID, f.epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_attachments SET expires_at=now()-interval '1 hour',created_at=now()-interval '2 days' WHERE id=ANY($1::uuid[])`, []string{expired.ID, retained.ID}); err != nil {
		t.Fatal(err)
	}
	storage := &cleanupStorage{}
	if err := store.CleanupAttachments(ctx, storage); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(storage.deleted, "upload-"+expired.ID) || !slices.Contains(storage.deleted, "image-"+expired.ID) {
		t.Fatal("expired image was not cleaned", storage.deleted)
	}
	for _, key := range storage.deleted {
		if strings.Contains(key, retained.ID) {
			t.Fatal("deleted retained attachment", key)
		}
	}
	if _, err := store.GetAttachment(ctx, p, f.orgID, retained.ID); err != nil {
		t.Fatal("referenced image expired", err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_attachments SET upload_expires_at=now()-interval '1 minute' WHERE id=$1`, retained.ID); err != nil {
		t.Fatal(err)
	}
	storage.deleted = nil
	if err := store.CleanupAttachments(ctx, storage); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(storage.deleted, "upload-"+retained.ID) || slices.Contains(storage.deleted, "image-"+retained.ID) {
		t.Fatal("temporary cleanup", storage.deleted)
	}
	storage.deleted = nil
	if err := store.CleanupAttachments(ctx, storage); err != nil || slices.Contains(storage.deleted, "upload-"+retained.ID) {
		t.Fatal("repeated cleanup", err, storage.deleted)
	}
}

func readyAttachment(store *Store, ctx context.Context, p domain.Principal, org, id string) error {
	_, err := store.FinalizeAttachment(ctx, p, org, id, func(context.Context, domain.Attachment) error { return nil })
	return err
}

func TestAttachmentForwardingUsesAuthorizedSourceReferences(t *testing.T) {
	store, admin, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	if _, err := admin.Exec(ctx, `UPDATE ao_sessions SET kind='orchestrator' WHERE id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_worker_connections SET capabilities='["attachments.images.v1"]' WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	create := func(session string) domain.Attachment {
		t.Helper()
		a, err := store.PrepareAttachment(ctx, p, f.orgID, uuid.NewString(), domain.PrepareAttachment{ProjectID: f.projectID, SessionID: session, Metadata: attachments.Metadata{Filename: "image.png", Size: 100, MIMEType: "image/png", SHA256: strings.Repeat("a", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := readyAttachment(store, ctx, p, f.orgID, a.ID); err != nil {
			t.Fatal(err)
		}
		return a
	}
	parentImage := create(f.sessionID)
	child, err := store.CreateOrchestratorChild(ctx, f.orgID, f.sessionID, "spawn-image", 100, domain.CreateSession{Harness: "codex", DisplayName: "Child", Provider: "docker", Mode: "trusted", AttachmentIDs: []string{parentImage.ID}})
	if err != nil {
		t.Fatal("spawn with parent image", err)
	}
	childWorker := "child-" + uuid.NewString()
	if _, err := admin.Exec(ctx, `INSERT INTO ao_worker_connections(session_id,org_id,sandbox_id,epoch,worker_id,version,capabilities) VALUES($1,$2,$1,1,$3,'test','["attachments.images.v1"]')`, child.ID, f.orgID, childWorker); err != nil {
		t.Fatal(err)
	}
	childImage := create(child.ID)
	for _, key := range []string{"forward", "forward-again"} {
		event, err := store.SendOrchestratorChildMessageWithAttachments(ctx, f.orgID, f.sessionID, child.ID, key, "", []string{parentImage.ID})
		if err != nil || !strings.Contains(string(event.Payload), parentImage.ID) {
			t.Fatal("parent to child", event, err)
		}
		retry, err := store.SendOrchestratorChildMessageWithAttachments(ctx, f.orgID, f.sessionID, child.ID, key, "", []string{parentImage.ID})
		if err != nil || retry.Sequence != event.Sequence {
			t.Fatal("forward retry", err)
		}
	}
	if _, err := store.ReportToOrchestratorWithAttachments(ctx, f.orgID, child.ID, "report-image", "", []string{childImage.ID}); err != nil {
		t.Fatal("child to parent", err)
	}
	if _, err := store.ReportToOrchestratorWithAttachments(ctx, f.orgID, child.ID, "report-received-image", "", []string{parentImage.ID}); err != nil {
		t.Fatal("forward received image", err)
	}
	for _, image := range []domain.Attachment{parentImage, childImage} {
		got, err := store.GetAttachment(ctx, p, f.orgID, image.ID)
		if err != nil || got.SessionID != image.SessionID {
			t.Fatal("forward changed source binding", got, err)
		}
	}
	for _, target := range []struct {
		session, worker string
		epoch           int64
	}{{f.sessionID, f.workerID, f.epoch}, {child.ID, childWorker, 1}} {
		manifest, err := store.WorkerAttachments(ctx, f.orgID, target.session, target.worker, target.epoch)
		if err != nil || len(manifest) != 2 {
			t.Fatal("forwarded worker manifest", manifest, err)
		}
	}
	other, err := store.CreateSession(ctx, p, f.orgID, "unrelated", 100, domain.CreateSession{ProjectID: f.projectID, Kind: "worker", Harness: "codex", DisplayName: "Other", Provider: "docker", Mode: "trusted"})
	if err != nil {
		t.Fatal(err)
	}
	otherImage := create(other.ID)
	if _, err := store.SendOrchestratorChildMessageWithAttachments(ctx, f.orgID, f.sessionID, child.ID, "foreign-source", "", []string{otherImage.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unrelated source accepted", err)
	}
	if _, err := store.SendMessage(ctx, p, f.orgID, other.ID, "foreign-user-message", "", domain.ChatTurnSettings{AttachmentIDs: []string{parentImage.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unrelated destination accepted", err)
	}
	if _, err := store.SendMessage(ctx, p, f.orgID, child.ID, "reply-with-received-image", "", domain.ChatTurnSettings{AttachmentIDs: []string{parentImage.ID}}); err != nil {
		t.Fatal("destination cannot reuse received image", err)
	}

	reader := domain.Principal{UserID: uuid.NewString(), Provider: "local"}
	if _, err := admin.Exec(ctx, `INSERT INTO ao_users(id,auth_provider,external_user_id,email,display_name,password_hash) VALUES($1,'local',$1::uuid::text,$1::uuid::text||'@example.test','Reader','hash')`, reader.UserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DELETE FROM ao_users WHERE id=$1`, reader.UserID) })
	_, token, err := store.CreateProjectShareLink(ctx, p, f.orgID, f.projectID, domain.CreateShareLink{SessionID: child.ID, Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RedeemProjectShareLink(ctx, reader, f.orgID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAttachment(ctx, reader, f.orgID, parentImage.ID); err != nil {
		t.Fatal("destination reader cannot preview forwarded image", err)
	}
	privateImage := create(f.sessionID)
	if _, err := store.GetAttachment(ctx, reader, f.orgID, privateImage.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader accessed unforwarded source image", err)
	}
}

func TestAttachmentPreparationLeaseExpiresWithoutAcknowledgement(t *testing.T) {
	store, admin, f := openNotificationTestStore(t)
	ctx := context.Background()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	if _, err := admin.Exec(ctx, `UPDATE ao_worker_connections SET capabilities='["attachments.images.v1"]' WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	a, err := store.PrepareAttachment(ctx, p, f.orgID, "terminal-image", domain.PrepareAttachment{ProjectID: f.projectID, SessionID: f.sessionID, Metadata: attachments.Metadata{Filename: "image.png", Size: 100, MIMEType: "image/png", SHA256: strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := readyAttachment(store, ctx, p, f.orgID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LeaseAttachments(ctx, p, f.orgID, f.sessionID, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_attachments SET expires_at=now()-interval '1 minute' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	storage := &cleanupStorage{}
	if err := store.CleanupAttachments(ctx, storage); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.WorkerAttachments(ctx, f.orgID, f.sessionID, f.workerID, f.epoch)
	if err != nil || len(manifest) != 1 {
		t.Fatal("lease did not permit worker download", manifest, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_worker_connections SET epoch=epoch+1 WHERE session_id=$1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainAttachments(ctx, p, f.orgID, f.sessionID, []string{a.ID}, f.workerID, f.epoch); !errors.Is(err, ErrStaleWorker) {
		t.Fatal("stale worker promoted lease", err)
	}
	var retained bool
	if err := admin.QueryRow(ctx, `SELECT retained FROM ao_attachments WHERE id=$1`, a.ID).Scan(&retained); err != nil || retained {
		t.Fatal("failed preparation retained permanently", retained, err)
	}
	if _, err := admin.Exec(ctx, `UPDATE ao_attachments SET preparation_expires_at=now()-interval '1 minute' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainAttachments(ctx, p, f.orgID, f.sessionID, []string{a.ID}, f.workerID, f.epoch+1); !errors.Is(err, ErrInvalid) {
		t.Fatal("expired preparation promoted", err)
	}
	if err := store.CleanupAttachments(ctx, storage); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(storage.deleted, "image-"+a.ID) {
		t.Fatal("abandoned preparation was not collected", storage.deleted)
	}
	if _, err := store.GetAttachment(ctx, p, f.orgID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("abandoned metadata remains", err)
	}
}

func TestAttachmentFinalizationAndCleanupInterleaving(t *testing.T) {
	store, admin, f := openNotificationTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p := domain.Principal{UserID: f.userID, Provider: "local"}
	storage, err := attachments.NewFilesystem("test", t.TempDir(), bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	a, err := store.PrepareAttachment(ctx, p, f.orgID, "race-image", domain.PrepareAttachment{ProjectID: f.projectID, SessionID: f.sessionID, Metadata: attachments.Metadata{Filename: "image.png", Size: 100, MIMEType: "image/png", SHA256: strings.Repeat("a", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	// Completion starts while valid and pauses before writing past the TTL.
	if _, err := admin.Exec(ctx, `UPDATE ao_attachments SET expires_at=clock_timestamp()+interval '1 second' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	finished := make(chan error, 1)
	go func() {
		_, err := store.FinalizeAttachment(ctx, p, f.orgID, a.ID, func(ctx context.Context, a domain.Attachment) error {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return storage.PutVerified(ctx, "image-"+a.ID, a.Metadata, []byte("canonical"))
		})
		finished <- err
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("completion did not enter write: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Wait only for the real expiry boundary, so cleanup is eligible to delete.
	if _, err := admin.Exec(ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM (expires_at-clock_timestamp())))) FROM ao_attachments WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	cleaned := make(chan error, 1)
	go func() { cleaned <- store.CleanupAttachments(ctx, storage) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'UPDATE ao_attachments SET status=%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-cleaned:
			t.Fatalf("cleanup ran through active finalization: %v", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	release <- struct{}{}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-cleaned; err != nil {
		t.Fatal(err)
	}
	if object, err := storage.Open(ctx, "image-"+a.ID); err == nil {
		object.Close()
		t.Fatal("orphaned canonical image after cleanup")
	}
	var rows int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM ao_attachments WHERE id=$1`, a.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("cleanup metadata", rows, err)
	}
	called := false
	if _, err := store.FinalizeAttachment(ctx, p, f.orgID, a.ID, func(context.Context, domain.Attachment) error { called = true; return nil }); !errors.Is(err, ErrNotFound) || called {
		t.Fatal("completion wrote after cleanup", called, err)
	}
}
