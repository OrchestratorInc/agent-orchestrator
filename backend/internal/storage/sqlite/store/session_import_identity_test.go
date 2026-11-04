package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestImportedNativeIdentityPublicationIsFencedAndPreservesSource(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rec := domain.SessionRecord{Kind: domain.KindWorker, Harness: domain.HarnessGemini, Mode: domain.SessionModeTUI, Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	rec.Metadata = domain.SessionMetadata{AgentSessionID: "original", ProviderConversationID: "original", RuntimeHandleID: "handle", RuntimeLaunchID: "launch", WorkspacePath: "/workspace", ImportSource: &domain.SessionImportSource{NativeID: "original", ConfigDir: "/provider", CWD: "/source", ResumeCwd: "/workspace", ResumeTranscriptPath: "/provider/original.jsonl", Fingerprint: "synthetic-native-snapshot"}}
	created, _, err := s.CreateImportedSession(ctx, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ handle, launch string }{{"wrong", "launch"}, {"handle", "old"}} {
		if ok, err := s.CommitImportedNativeSessionID(ctx, created.ID, created.Metadata.ImportSource, tc.handle, tc.launch, "fork"); err != nil || ok {
			t.Fatalf("unowned publication = %v, %v", ok, err)
		}
	}
	if ok, err := s.CommitImportedNativeSessionID(ctx, created.ID, created.Metadata.ImportSource, "handle", "launch", "fork"); err != nil || !ok {
		t.Fatalf("owned publication = %v, %v", ok, err)
	}
	loaded, _, err := s.GetSession(ctx, created.ID)
	if err != nil || loaded.Metadata.AgentSessionID != "fork" || loaded.Metadata.ProviderConversationID != "fork" || loaded.Metadata.ImportSource.ResumeNativeID != "fork" || loaded.Metadata.ImportSource.NativeID != "original" || loaded.Metadata.ImportSource.ConfigDir != "/provider" || loaded.Metadata.ImportSource.ResumeTranscriptPath != "" {
		t.Fatalf("published native fork = %#v, %v", loaded.Metadata, err)
	}
	if loaded.Metadata.ImportSource.Fingerprint != "synthetic-native-snapshot" {
		t.Fatal("native identity takeover lost the pinned source fingerprint")
	}
	if ok, err := s.CommitImportedNativeSessionID(ctx, created.ID, created.Metadata.ImportSource, "handle", "launch", "another-fork"); err != nil || ok {
		t.Fatalf("stale checkpoint accepted = %v, %v", ok, err)
	}
	adopted := *loaded.Metadata.ImportSource
	adopted.Adopted = true
	loaded.Metadata.ImportSource = &adopted
	if err := s.UpdateSession(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CommitImportedNativeSessionID(ctx, loaded.ID, &adopted, "handle", "launch", "another-fork"); err != nil || ok {
		t.Fatalf("adopted session accepted a new fork = %v, %v", ok, err)
	}
}

func TestMetadataOnlyImportRequiresNativeTerminalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode domain.SessionMode
		id   string
		want bool
	}{
		{name: "native terminal", mode: domain.SessionModeTUI, id: "native", want: true},
		{name: "chat requires readable history", mode: domain.SessionModeChat, id: "native"},
		{name: "terminal missing identity", mode: domain.SessionModeTUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			rec := domain.SessionRecord{Kind: domain.KindWorker, Harness: domain.HarnessGemini, Mode: tc.mode, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			rec.Metadata = domain.SessionMetadata{AgentSessionID: tc.id, ProviderConversationID: tc.id, ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: "/provider"}}
			_, fresh, err := s.CreateImportedSession(context.Background(), rec, nil)
			if tc.want != (err == nil && fresh) {
				t.Fatalf("metadata-only import: fresh=%v err=%v", fresh, err)
			}
		})
	}
}
