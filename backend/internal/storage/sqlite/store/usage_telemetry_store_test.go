package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestUsageStandaloneMessageDoesNotBlockProjectDrain(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "project")
	s.SetUsageTelemetryAllowed(func() bool { return true })
	appendMessage := func(project, conversationID, messageID string) domain.SessionRecord {
		t.Helper()
		record := sampleRecord(project)
		record.Mode = domain.SessionModeChat
		session, err := s.CreateSession(ctx, record)
		if err != nil {
			t.Fatal(err)
		}
		conversation, err := s.CreateConversation(ctx, conversationID, domain.ConversationScopeSession,
			session.ProjectID, session.ID, testNow)
		if err != nil {
			t.Fatal(err)
		}
		created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation",
			domain.ConversationMessage{ID: messageID, Origin: domain.MessageOriginHuman,
				Text: "private prompt", ClientMessageID: messageID}, "turn-"+messageID, testNow)
		if err != nil || !created {
			t.Fatalf("append %s: created=%v err=%v", messageID, created, err)
		}
		return session
	}
	standalone := appendMessage("", "standalone-conversation", "standalone-message")
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var pending int64
	err = raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_message_summary WHERE session_id = ?`, standalone.ID).Scan(&pending)
	if err != nil || pending != 0 {
		t.Fatalf("new standalone summary rows = %d, err=%v", pending, err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO usage_message_summary (session_id, pending_count, first_at, last_at)
		VALUES (?, 1, ?, ?)`, standalone.ID, testNow, testNow); err != nil {
		t.Fatal(err)
	}
	appendMessage("project", "project-conversation", "project-message")
	batches, err := s.DrainUsageMessages(ctx, 1, false, "install-one")
	if err != nil || len(batches) != 1 || batches[0].ProjectID != "project" || batches[0].Count != 1 {
		t.Fatalf("project batch behind legacy standalone row = %+v, err=%v", batches, err)
	}
	if more, err := s.DrainUsageMessages(ctx, 1, false, "install-one"); err != nil || len(more) != 0 {
		t.Fatalf("remaining batches = %+v, err=%v", more, err)
	}
}

func TestUsageProjectIDIsRandomStableAndInstallScoped(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "first")
	seedProject(t, s, "second")
	ctx := context.Background()
	a, err := s.UsageProjectID(ctx, "first", "install-one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.UsageProjectID(ctx, "first", "install-one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.UsageProjectID(ctx, "second", "install-one")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.UsageProjectID(ctx, "first", "install-two")
	if err != nil {
		t.Fatal(err)
	}
	if a != again || a == b || a == c || a == "first" {
		t.Fatalf("project identity scope: %q %q %q %q", a, again, b, c)
	}
}

func TestUsagePendingCountAndProjectIdentitySurviveStoreRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "restart")
	ctx := context.Background()
	projectID, err := s.UsageProjectID(ctx, "restart", "install-one")
	if err != nil {
		t.Fatal(err)
	}
	record := sampleRecord("restart")
	record.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversation(ctx, "restart-conversation", domain.ConversationScopeSession, "restart", session.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	s.SetUsageTelemetryAllowed(func() bool { return true })
	if created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation",
		domain.ConversationMessage{ID: "restart-message", Text: "content", Origin: domain.MessageOriginHuman,
			ClientMessageID: "client-restart"}, "turn-restart", time.Now().UTC()); err != nil || !created {
		t.Fatalf("append: created=%v err=%v", created, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	again, err := reopened.UsageProjectID(ctx, "restart", "install-one")
	if err != nil || again != projectID {
		t.Fatalf("project identity after restart = %q, err=%v", again, err)
	}
	batches, err := reopened.DrainUsageMessages(ctx, 10, false, "install-one")
	if err != nil || len(batches) != 1 || batches[0].Count != 1 {
		t.Fatalf("batches after restart = %+v, err=%v", batches, err)
	}
}

func TestUsageMessagesCountOnlyNewDirectHumanChatPrompts(t *testing.T) {
	s, session, conversation := seededChatConversation(t)
	s.SetUsageTelemetryAllowed(func() bool { return true })
	ctx := context.Background()
	add := func(id, client string, origin domain.MessageOrigin, sender string, retry string) bool {
		t.Helper()
		var created bool
		var err error
		if retry != "" {
			created, err = s.AppendRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root",
				domain.ConversationMessage{ID: id, Text: "private prompt", Origin: origin},
				"turn-"+id, retry, testNow.Add(time.Minute))
		} else {
			created, err = s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root",
				domain.ConversationMessage{ID: id, Text: "private prompt", Origin: origin,
					SenderSessionID: sender, ClientMessageID: client}, "turn-"+id, testNow.Add(time.Minute))
		}
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	if !add("human", "client-human", domain.MessageOriginHuman, "", "") {
		t.Fatal("new prompt not created")
	}
	if add("duplicate", "client-human", domain.MessageOriginHuman, "", "") {
		t.Fatal("duplicate prompt created")
	}
	if !add("automation", "client-auto", domain.MessageOriginAutomation, "worker-1", "") {
		t.Fatal("automation not created")
	}
	if !add("retry", "", domain.MessageOriginHuman, "", "turn-human") {
		t.Fatal("retry not created")
	}
	if created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root",
		domain.ConversationMessage{ID: "opening", Text: "spawn task brief", Origin: domain.MessageOriginHuman,
			SpawnOpeningPrompt: true}, "turn-opening", testNow.Add(time.Minute)); err != nil || !created {
		t.Fatalf("opening prompt: created=%v err=%v", created, err)
	}
	batches, err := s.DrainUsageMessages(ctx, 10, false, "install-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || batches[0].Count != 1 || batches[0].ProjectID != "branches" {
		t.Fatalf("batches = %+v, want one direct human message", batches)
	}
	if more, err := s.DrainUsageMessages(ctx, 10, false, "install-one"); err != nil || len(more) != 0 {
		t.Fatalf("replayed batches = %+v, err = %v", more, err)
	}
	s.SetUsageTelemetryAllowed(func() bool { return false })
	if !add("opted-out", "client-off", domain.MessageOriginHuman, "", "") {
		t.Fatal("opted out prompt not stored")
	}
	if more, err := s.DrainUsageMessages(ctx, 10, false, "install-one"); err != nil || len(more) != 0 {
		t.Fatalf("opted out batches = %+v, err = %v", more, err)
	}
}

func TestUsagePRTracksWorkerAssociationAndMergedTransitionOnce(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "repo")
	ctx := context.Background()
	worker, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator := sampleRecord("repo")
	orchestrator.Kind = domain.KindOrchestrator
	orch, err := s.CreateSession(ctx, orchestrator)
	if err != nil {
		t.Fatal(err)
	}
	allowed := true
	s.SetUsageTelemetryAllowed(func() bool { return allowed })
	now := time.Now().UTC()
	pr := domain.PullRequest{URL: "https://github.com/org/repo/pull/42", SessionID: orch.ID,
		Provider: "github", Host: "github.com", Repo: "org/repo", Number: 42, UpdatedAt: now}
	write := func() {
		t.Helper()
		if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatal(err)
		}
	}
	write()
	assertPending := func(n int) {
		t.Helper()
		facts, err := s.PendingWorkerPRUsage(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(facts) != n {
			t.Fatalf("pending = %+v, want %d", facts, n)
		}
	}
	assertPending(0)
	pr.SessionID = worker.ID
	if _, err := s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatal(err)
	}
	assertPending(1)
	pr.SessionID = orch.ID
	if _, err := s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatal(err)
	}
	assertPending(1)
	facts, _ := s.PendingWorkerPRUsage(ctx, 10)
	if facts[0].Historical || !facts[0].AssociationPending || facts[0].MergePending {
		t.Fatalf("association = %+v", facts[0])
	}
	if err := s.MarkWorkerPRUsageReported(ctx, facts[0].Key, true, false); err != nil {
		t.Fatal(err)
	}
	write()
	assertPending(0)
	pr.ProviderID = "provider-42"
	write()
	assertPending(0)
	pr.Merged = true
	write()
	assertPending(1)
	facts, _ = s.PendingWorkerPRUsage(ctx, 10)
	if facts[0].AssociationPending || !facts[0].MergePending {
		t.Fatalf("merge = %+v", facts[0])
	}
	if err := s.MarkWorkerPRUsageReported(ctx, facts[0].Key, false, true); err != nil {
		t.Fatal(err)
	}
	write()
	assertPending(0)
	allowed = false
	pr = domain.PullRequest{URL: "https://github.com/org/repo/pull/43", SessionID: worker.ID,
		Provider: "github", Host: "github.com", Repo: "org/repo", Number: 43, Merged: true, UpdatedAt: now}
	write()
	allowed = true
	write()
	assertPending(0)
}

func TestStandaloneWorkerPRHasNoUsageMeasurement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.SetUsageTelemetryAllowed(func() bool { return true })
	worker, err := s.CreateSession(ctx, sampleRecord(""))
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{URL: "https://github.com/org/repo/pull/77", SessionID: worker.ID,
		Provider: "github", Host: "github.com", Repo: "org/repo", Number: 77, UpdatedAt: time.Now().UTC()}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	facts, err := s.PendingWorkerPRUsage(ctx, 10)
	if err != nil || len(facts) != 0 {
		t.Fatalf("standalone worker usage = %+v, err=%v", facts, err)
	}
}

func TestUsageWriteFailureDoesNotFailPROperations(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	seedProject(t, s, "repo")
	worker, err := s.CreateSession(ctx, sampleRecord("repo"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(ctx, `CREATE TRIGGER reject_usage BEFORE INSERT ON usage_pr_measurement
		BEGIN SELECT RAISE(ABORT, 'usage write failed'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pr := domain.PullRequest{URL: "https://github.com/org/repo/pull/88", SessionID: worker.ID,
		Provider: "github", Host: "github.com", Repo: "org/repo", Number: 88, UpdatedAt: now}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("pr observation failed because usage write failed: %v", err)
	}
	if err := s.MarkPRMerged(ctx, pr.URL, "merge-sha", now); err != nil {
		t.Fatalf("merge record failed because usage write failed: %v", err)
	}
	got, found, err := s.GetPR(ctx, pr.URL)
	if err != nil || !found || !got.Merged {
		t.Fatalf("merged pr = %+v, found=%v err=%v", got, found, err)
	}
}
