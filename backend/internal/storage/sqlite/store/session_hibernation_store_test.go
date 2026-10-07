package store_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestListChatHibernationCandidatesFiltersInSQL(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	var want []domain.SessionID
	for _, change := range []func(*domain.SessionRecord){
		func(*domain.SessionRecord) {},
		func(r *domain.SessionRecord) { r.Mode = domain.SessionModeTUI },
		func(r *domain.SessionRecord) { r.IsTerminated = true },
		func(r *domain.SessionRecord) { r.IsTaskPreparation = true },
		func(r *domain.SessionRecord) { r.ProvisionState = domain.SessionProvisionProvisioning },
		func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityActive },
		func(r *domain.SessionRecord) { r.Metadata.ProviderConversationID = "" },
	} {
		rec := sampleRecord("mer")
		rec.Mode, rec.Activity.State, rec.Metadata.ProviderConversationID = domain.SessionModeChat, domain.ActivityIdle, "native-id"
		change(&rec)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if rec.EligibleForChatHibernation() {
			want = append(want, created.ID)
		}
	}
	got, err := s.ListChatHibernationCandidates(ctx)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, %v; want %v", got, err, want)
	}
	current, _, _ := s.GetSession(ctx, want[0])
	now := time.Now().UTC()
	if applied, err := s.SetSessionHibernated(ctx, current.ID, current.Revision, &now); err != nil || !applied {
		t.Fatalf("mark asleep = %v, %v", applied, err)
	}
	if got, err := s.ListChatHibernationCandidates(ctx); err != nil || len(got) != 0 {
		t.Fatalf("already sleeping candidates = %v, %v", got, err)
	}
}

func TestSessionHibernationMarkerUsesRevisionAndSurvivesOrdinaryUpdate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Mode = domain.SessionModeChat
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	stale, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get session = %v, %v", ok, err)
	}

	// A later session write invalidates the idle snapshot used to choose sleep.
	current := stale
	current.DisplayName = "new activity"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Truncate(time.Second)
	if applied, err := s.SetSessionHibernated(ctx, created.ID, stale.Revision, &when); err != nil || applied {
		t.Fatalf("stale hibernate = %v, %v", applied, err)
	}

	current, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("reload session = %v, %v", ok, err)
	}
	if applied, err := s.SetSessionHibernated(ctx, created.ID, current.Revision, &when); err != nil || !applied {
		t.Fatalf("hibernate = %v, %v", applied, err)
	}
	waking, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok || waking.HibernatedAt == nil || !waking.HibernatedAt.Equal(when) {
		t.Fatalf("hibernated session = %+v, %v, %v", waking.HibernatedAt, ok, err)
	}
	current.DisplayName = "another update"
	if err := s.UpdateSession(ctx, current); err != nil {
		t.Fatal(err)
	}
	waking, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok || waking.HibernatedAt == nil {
		t.Fatalf("marker after ordinary update = %+v, %v, %v", waking.HibernatedAt, ok, err)
	}
	if applied, err := s.SetSessionHibernated(ctx, created.ID, waking.Revision, nil); err != nil || !applied {
		t.Fatalf("wake = %v, %v", applied, err)
	}
	waking, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok || waking.HibernatedAt != nil {
		t.Fatalf("awake session = %+v, %v, %v", waking.HibernatedAt, ok, err)
	}
}

func TestSessionHibernationMarkerEmitsOneChangePerTransition(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Mode = domain.SessionModeChat
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConversation(ctx, "hibernate-cdc", domain.ConversationScopeSession, created.ProjectID, created.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	current, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get session = %v, %v", ok, err)
	}
	base, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Truncate(time.Second)
	if applied, err := s.SetSessionHibernated(ctx, created.ID, current.Revision, &when); err != nil || !applied {
		t.Fatalf("hibernate = %v, %v", applied, err)
	}
	events, err := s.EventsAfter(ctx, base, 10)
	if err != nil || len(events) != 1 || string(events[0].Type) != "session_updated" || events[0].SessionID != string(created.ID) {
		t.Fatalf("hibernate events = %+v, %v", events, err)
	}
	var payload struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload.ID != string(created.ID) || payload.ConversationID != "hibernate-cdc" {
		t.Fatalf("hibernate event payload = %+v, %v", payload, err)
	}

	current, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("reload session = %v, %v", ok, err)
	}
	if applied, err := s.SetSessionHibernated(ctx, created.ID, current.Revision, &when); err != nil || !applied {
		t.Fatalf("repeat hibernate = %v, %v", applied, err)
	}
	events, err = s.EventsAfter(ctx, events[0].Seq, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("repeat hibernate events = %+v, %v", events, err)
	}

	current, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("reload session = %v, %v", ok, err)
	}
	if applied, err := s.SetSessionHibernated(ctx, created.ID, current.Revision, nil); err != nil || !applied {
		t.Fatalf("wake = %v, %v", applied, err)
	}
	events, err = s.EventsAfter(ctx, base, 10)
	if err != nil || len(events) != 2 || string(events[1].Type) != "session_updated" || events[1].SessionID != string(created.ID) {
		t.Fatalf("wake events = %+v, %v", events, err)
	}
	payload.ID, payload.ConversationID = "", ""
	if err := json.Unmarshal(events[1].Payload, &payload); err != nil || payload.ConversationID != "hibernate-cdc" {
		t.Fatalf("wake event payload = %+v, %v", payload, err)
	}
}
