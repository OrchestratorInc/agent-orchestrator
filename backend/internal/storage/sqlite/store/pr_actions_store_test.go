package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMarkPRStateUpdatesStateOnlyAndEmitsCDC(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{
		URL: "https://github.com/acme/repo/pull/8", SessionID: r.ID, Number: 8,
		CI: domain.CIPassing, Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable,
		Provider: "github", Host: "github.com", Repo: "acme/repo", HeadSHA: "head", Title: "keep",
		MetadataHash: "metadata-v1", UpdatedAt: now, StateChangedAt: now, ObservedAt: now,
	}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	for i, state := range []domain.PRState{domain.PRStateDraft, domain.PRStateOpen, domain.PRStateClosed, domain.PRStateOpen} {
		base, err := s.LatestSeq(ctx)
		if err != nil {
			t.Fatal(err)
		}
		changedAt := now.Add(time.Duration(i+1) * time.Minute)
		if err := s.MarkPRState(ctx, pr.URL, state, changedAt); err != nil {
			t.Fatalf("mark %s: %v", state, err)
		}
		got, _, err := s.GetPR(ctx, pr.URL)
		if err != nil {
			t.Fatal(err)
		}
		if got.Draft != (state == domain.PRStateDraft) || got.Closed != (state == domain.PRStateClosed) || got.Merged {
			t.Fatalf("%s: draft=%v closed=%v merged=%v", state, got.Draft, got.Closed, got.Merged)
		}
		if !got.StateChangedAt.Equal(changedAt) || got.Title != "keep" || got.CI != domain.CIPassing || got.MetadataHash != "metadata-v1" || !got.ObservedAt.Equal(now) {
			t.Fatalf("%s: mark state changed observed facts: %+v", state, got)
		}
		events, err := s.EventsAfter(ctx, base, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || string(events[0].Type) != "pr_updated" {
			t.Fatalf("%s: events = %+v, want one pr_updated", state, events)
		}
		var payload map[string]any
		if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["state"] != string(state) {
			t.Fatalf("%s: event payload = %#v", state, payload)
		}
	}

	if err := s.MarkPRMerged(ctx, pr.URL, "merge-sha", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPRState(ctx, pr.URL, domain.PRStateClosed, now.Add(2*time.Hour)); err == nil {
		t.Fatal("mark state rewrote a merged PR")
	}
	if err := s.MarkPRState(ctx, pr.URL, domain.PRStateMerged, now); err == nil {
		t.Fatal("mark state accepted merged as a target")
	}
	if err := s.MarkPRState(ctx, "https://github.com/acme/repo/pull/404", domain.PRStateOpen, now); err == nil {
		t.Fatal("mark state succeeded for an unknown PR")
	}
}

func TestPRDetailsConversationRoundTripAndAppend(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/2", SessionID: r.ID, Number: 2, UpdatedAt: now}

	// No details row yet: the comment waits for the observation.
	ok, err := s.AppendPRConversationComment(ctx, pr.URL, domain.PullRequestConversationComment{ID: "IC_0", Body: "early"})
	if err != nil || ok {
		t.Fatalf("append before details: ok=%v err=%v, want skipped", ok, err)
	}

	details := detailsFixture(now)
	details.Conversation = []domain.PullRequestConversationComment{{ID: "IC_1", Author: "amy", Body: "hello", URL: "https://x/1", CreatedAt: now}}
	pr.Details = details
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GetPRDetails(ctx, pr.URL)
	if err != nil || len(got.Conversation) != 1 || got.Conversation[0] != details.Conversation[0] {
		t.Fatalf("conversation = %+v, err %v", got.Conversation, err)
	}

	posted := domain.PullRequestConversationComment{ID: "IC_2", Author: "me", IsBot: false, Body: "posted", CreatedAt: now.Add(time.Minute)}
	if ok, err := s.AppendPRConversationComment(ctx, pr.URL, posted); err != nil || !ok {
		t.Fatalf("append: ok=%v err=%v", ok, err)
	}
	if ok, err := s.AppendPRConversationComment(ctx, pr.URL, posted); err != nil || !ok {
		t.Fatalf("repeat append: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetPRDetails(ctx, pr.URL)
	if len(got.Conversation) != 2 || got.Conversation[1] != posted || got.Description != details.Description {
		t.Fatalf("after append = %+v", got)
	}

	for i := 0; i < domain.MaxPullRequestConversationComments; i++ {
		if _, err := s.AppendPRConversationComment(ctx, pr.URL, domain.PullRequestConversationComment{ID: fmt.Sprintf("IC_x%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ = s.GetPRDetails(ctx, pr.URL)
	if len(got.Conversation) != domain.MaxPullRequestConversationComments || got.Conversation[0].ID != "IC_x0" {
		t.Fatalf("capped conversation len=%d first=%q", len(got.Conversation), got.Conversation[0].ID)
	}
}

func TestUpsertPRReviewCommentStoresOneReply(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/3", SessionID: r.ID, Number: 3, UpdatedAt: now}
	threads := []domain.PullRequestReviewThread{{ThreadID: "T1", Path: "a.go", Line: 4, UpdatedAt: now}}
	comments := []domain.PullRequestComment{{ThreadID: "T1", ID: "c1", Author: "amy", File: "a.go", Line: 4, Body: "nit", CreatedAt: now}}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, threads, comments, ports.ReviewWriteReplace); err != nil {
		t.Fatal(err)
	}
	reply := domain.PullRequestComment{ThreadID: "T1", ID: "c2", Author: "me", File: "a.go", Line: 4, Body: "done", URL: "https://x/c2", CreatedAt: now.Add(time.Minute)}
	if err := s.UpsertPRReviewComment(ctx, pr.URL, reply); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListPRComments(ctx, pr.URL)
	if err != nil || len(got) != 2 || got[1].ID != "c2" || got[1].Body != "done" || got[1].ThreadID != "T1" || !got[1].CreatedAt.Equal(reply.CreatedAt) {
		t.Fatalf("comments = %+v, err %v", got, err)
	}
}
