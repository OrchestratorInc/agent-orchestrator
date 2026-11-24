package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/chat"
)

func draftSide(t *testing.T, h *sideWorkflow, key string) domain.SideConversation {
	t.Helper()
	side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return side
}

func sideProviderCount(h *sideWorkflow) int {
	h.driver.mu.Lock()
	defer h.driver.mu.Unlock()
	return len(h.driver.forks)
}

func TestDeferredSideCreationLabels(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		manual, invalid   bool
	}{
		{name: "omitted", want: "Side Chat"},
		{name: "whitespace", input: " \t\n", want: "Side Chat"},
		{name: "custom", input: "Investigate timeout", want: "Investigate timeout", manual: true},
		{name: "trimmed", input: "  Investigate timeout \t", want: "Investigate timeout", manual: true},
		{name: "ASCII boundary", input: strings.Repeat("x", 80), want: strings.Repeat("x", 80), manual: true},
		{name: "Unicode boundary", input: strings.Repeat("界", 80), want: strings.Repeat("界", 80), manual: true},
		{name: "emoji boundary", input: strings.Repeat("🙂", 80), want: strings.Repeat("🙂", 80), manual: true},
		{name: "ASCII too long", input: strings.Repeat("x", 81), invalid: true},
		{name: "Unicode too long", input: strings.Repeat("界", 81), invalid: true},
		{name: "padding counts toward API limit", input: " " + strings.Repeat("x", 80), invalid: true},
		{name: "invalid text", input: string([]byte{0xff}), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSideWorkflow(t, "")
			side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "named", Label: tc.input})
			if tc.invalid {
				if !errors.Is(err, chatsvc.ErrSideLabelInvalid) {
					t.Fatalf("invalid label accepted: %+v, %v", side, err)
				}
			} else if err != nil || side.Label != tc.want || side.ManualLabel != tc.manual || side.State != "draft" {
				t.Fatalf("created label: %+v, %v", side, err)
			}
			sides, err := h.svc.ListIndependentSideChats(t.Context(), testSession)
			if err != nil || tc.invalid && len(sides) != 0 || !tc.invalid && len(sides) != 1 {
				t.Fatalf("created drafts: %+v, %v", sides, err)
			}
			if sideProviderCount(h) != 0 {
				t.Fatal("naming a draft started a provider")
			}
		})
	}
}

func TestDeferredSideCreationLabelSurvivesFirstSendAndRenamedRetry(t *testing.T) {
	h := newSideWorkflow(t, "")
	side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "named", Label: "Investigate timeout"})
	if err != nil {
		t.Fatal(err)
	}
	h.source(t)
	if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{ClientMessageID: "question", Text: "Do not replace the chosen name"}); err != nil {
		t.Fatal(err)
	}
	snap := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "ready" })
	if snap.Side.Label != "Investigate timeout" || !snap.Side.ManualLabel {
		t.Fatalf("first Send replaced the chosen name: %+v", snap.Side)
	}
	if err := h.svc.RenameSideChat(t.Context(), testSession, side.ID, "Renamed investigation"); err != nil {
		t.Fatal(err)
	}
	retried, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "named", Label: "Different retry name"})
	if err != nil || retried.ID != side.ID || retried.Label != "Renamed investigation" || !retried.ManualLabel || sideProviderCount(h) != 1 {
		t.Fatalf("creation retry changed identity/name or restarted provider: %+v, %v", retried, err)
	}
}

func TestDeferredSideCustomNamesAllowDuplicatesWithoutAllocatingDefaultNumbers(t *testing.T) {
	h := newSideWorkflow(t, "")
	last := draftSide(t, h, "last-default")
	if err := h.svc.RenameSideChat(t.Context(), testSession, last.ID, "Side Chat 99"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "over-limit"}); !errors.Is(err, chatsvc.ErrSideNameLimit) {
		t.Fatalf("default numbering limit changed: %v", err)
	}
	var previousID string
	for _, key := range []string{"custom-one", "custom-two"} {
		side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: key, Label: "Investigation"})
		if err != nil || side.ID == previousID || side.Label != "Investigation" || !side.ManualLabel {
			t.Fatalf("custom name reserved a number or merged identities: %+v, %v", side, err)
		}
		previousID = side.ID
	}
	if sideProviderCount(h) != 0 {
		t.Fatal("custom naming started a provider")
	}
}

func TestDeferredSideDraftDoesNotStartProvider(t *testing.T) {
	h := newSideWorkflow(t, "")
	side := draftSide(t, h, "draft")
	if side.State != "draft" || side.AnchorTurnID != "" || side.OpeningTurnID != "" || len(side.LaunchConfig) != 0 {
		t.Fatalf("not an unused draft: %+v", side)
	}
	if err := h.svc.RenameSideChat(t.Context(), testSession, side.ID, "My draft"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SaveSideDraft(t.Context(), testSession, side.ID, `{"version":1,"text":"unsent","attachments":[],"references":[]}`); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.UpdateSideSettings(t.Context(), testSession, side.ID, "selected", "high"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RetrySideConnection(t.Context(), testSession, side.ID); !errors.Is(err, chatsvc.ErrSideNotReady) {
		t.Fatalf("retry started an unused draft: %v", err)
	}
	if err := h.svc.CloseIndependentSideChat(t.Context(), testSession, side.ID); err != nil {
		t.Fatal(err)
	}
	if sideProviderCount(h) != 0 {
		t.Fatal("unused side created a provider")
	}
}

func TestDeferredSideDraftsKeepSeparateIdentities(t *testing.T) {
	h := newSideWorkflow(t, "")
	one, two := draftSide(t, h, "one"), draftSide(t, h, "two")
	retry := draftSide(t, h, "one")
	if one.ID == two.ID || retry.ID != one.ID {
		t.Fatal("empty anchors merged drafts or lost their creation receipt")
	}
}

func TestDeferredSideFreezesContextAndSettingsOnFirstSend(t *testing.T) {
	h := newSideWorkflow(t, "")
	side := draftSide(t, h, "first")
	h.source(t)
	if err := h.svc.UpdateSideSettings(t.Context(), testSession, side.ID, "chosen", "high"); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "question", ClientMessageID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	snap := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return s.Side.State == "ready" && sideProviderCount(h) == 1 })
	if snap.Side.AnchorTurnID == "" || snap.Side.OpeningTurnID != turn.ID || snap.Side.OpeningStartedAt == nil || !snap.Side.OpeningStartedAt.Equal(turn.CreatedAt) {
		t.Fatalf("missing frozen opening: %+v", snap.Side)
	}
	if !turn.SettingsFrozen || turn.Model != "chosen" || turn.Effort != "high" {
		t.Fatalf("settings not frozen: %+v", turn)
	}
	h.driver.mu.Lock()
	cfg := h.driver.configs[0]
	h.driver.mu.Unlock()
	if cfg.Model != "chosen" || cfg.Effort != "high" {
		t.Fatalf("wrong launch settings: %+v", cfg)
	}
	if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "question", ClientMessageID: "first"}); err != nil {
		t.Fatal(err)
	}
	if sideProviderCount(h) != 1 {
		t.Fatal("retry created another provider")
	}
}

func TestDeferredSideConcurrentFirstSendStartsOnce(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	side := draftSide(t, h, "concurrent")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "once", ClientMessageID: "same"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	snap := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return sideProviderCount(h) == 1 && s.Side.State == "ready" })
	if len(snap.Turns) != 1 {
		t.Fatalf("duplicate first turns: %d", len(snap.Turns))
	}
}

func TestDeferredSideQueuesFollowupsWhileStarting(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.driver.onFork = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	side := draftSide(t, h, "queued")
	first, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "first", ClientMessageID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not begin")
	}
	defer close(release)
	for i := 0; i < 3; i++ {
		if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: fmt.Sprint(i), ClientMessageID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	snap := h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return len(s.Turns) == 4 })
	if snap.Side.OpeningTurnID != first.ID || snap.Side.State != "opening" {
		t.Fatalf("opening identity changed: %+v", snap.Side)
	}
	for _, turn := range snap.Turns {
		if turn.State != "queued" {
			t.Fatalf("dispatched before provider readiness: %+v", turn)
		}
	}
}

func TestDeferredSideCloseCancelsStartup(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	h.driver.onFork = func(ctx context.Context) error { close(started); <-ctx.Done(); close(cancelled); return ctx.Err() }
	side := draftSide(t, h, "closing")
	if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "question", ClientMessageID: "first"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not begin")
	}
	if err := h.svc.CloseIndependentSideChat(t.Context(), testSession, side.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("startup survived close")
	}
	if sideProviderCount(h) != 0 {
		t.Fatal("closed startup installed a provider")
	}
}

func TestDeferredSideRestoreDraftStaysUnused(t *testing.T) {
	h := newSideWorkflow(t, "")
	side, err := h.svc.CreateIndependentSideChat(t.Context(), testSession, chatsvc.SideCreateRequest{IdempotencyKey: "recover", Label: "Recovered investigation"})
	if err != nil {
		t.Fatal(err)
	}
	records, err := h.svc.ExportSideChatLaunch(t.Context(), "workflow")
	if err != nil {
		t.Fatal(err)
	}
	// Electron transports recovery records through JSON, not Go values.
	wire, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wire, &records); err != nil {
		t.Fatal(err)
	}
	replacement := chatsvc.New(chatsvc.Options{Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st, Drivers: fakeRegistry{driver: h.driver}, AppRunID: "workflow"})
	t.Cleanup(func() { replacement.StopAll(context.Background()) })
	if err := replacement.InitializeSideChats(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := replacement.RecoverSideChatLaunch(t.Context(), "workflow", records); err != nil {
		t.Fatal(err)
	}
	snap, err := replacement.SideSnapshot(t.Context(), testSession, side.ID, time.Time{}, 50)
	if err != nil || snap.Side.State != "draft" || snap.Side.Label != "Recovered investigation" || !snap.Side.ManualLabel || sideProviderCount(h) != 0 {
		t.Fatalf("restored draft started: %+v %v", snap.Side, err)
	}
}

func TestDeferredSideRestoreUncertainFirstSendRequiresRecreation(t *testing.T) {
	h := newSideWorkflow(t, "")
	side := draftSide(t, h, "uncertain-draft")
	if err := h.svc.SaveSideDraft(t.Context(), testSession, side.ID, `{"version":1,"text":"newer edit","pendingDelivery":{"clientMessageId":"frozen","request":{"text":"original question"}},"attachments":[],"references":[]}`); err != nil {
		t.Fatal(err)
	}
	records, err := h.svc.ExportSideChatLaunch(t.Context(), "workflow")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wire, &records); err != nil {
		t.Fatal(err)
	}
	replacement := chatsvc.New(chatsvc.Options{Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st, Drivers: fakeRegistry{driver: h.driver}, AppRunID: "workflow"})
	t.Cleanup(func() { replacement.StopAll(context.Background()) })
	if err := replacement.InitializeSideChats(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := replacement.RecoverSideChatLaunch(t.Context(), "workflow", records); err != nil {
		t.Fatal(err)
	}
	snap, err := replacement.SideSnapshot(t.Context(), testSession, side.ID, time.Time{}, 50)
	if err != nil || snap.Side.State != "failed" || !snap.Side.RecreateRequired || sideProviderCount(h) != 0 {
		t.Fatalf("unsafe restore: %+v %v", snap.Side, err)
	}
	if _, err := replacement.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "original question", ClientMessageID: "frozen"}); !errors.Is(err, chatsvc.ErrSideNotReady) {
		t.Fatalf("uncertain delivery resubmitted: %v", err)
	}
}

func TestDeferredSideRecoveryDistinguishesAbsentAndFrozenLaunchConfig(t *testing.T) {
	for _, tc := range []struct {
		name, config, pending, state string
		recreate                     bool
	}{
		{name: "absent", state: "draft"},
		{name: "null", config: "null", state: "draft"},
		{name: "whitespace null", config: " \nnull\t", state: "draft"},
		{name: "null pending delivery", config: "null", pending: "null", state: "draft"},
		{name: "frozen config", config: `{}`, state: "failed"},
		{name: "string null is not absent", config: `"null"`, state: "failed"},
		{name: "uncertain receipt", config: "null", pending: `{"clientMessageId":"first","request":{"text":"original question"}}`, state: "failed", recreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSideWorkflow(t, "")
			side := draftSide(t, h, "recover")
			records, err := h.svc.ExportSideChatLaunch(t.Context(), "workflow")
			if err != nil {
				t.Fatal(err)
			}
			records[0].LaunchConfig = json.RawMessage(tc.config)
			if tc.pending != "" {
				records[0].Draft = `{"version":1,"text":"keep my question","attachments":[],"references":[],"pendingDelivery":` + tc.pending + `}`
			}
			replacement := chatsvc.New(chatsvc.Options{Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st, Drivers: fakeRegistry{driver: h.driver}, AppRunID: "workflow"})
			t.Cleanup(func() { replacement.StopAll(context.Background()) })
			if err := replacement.InitializeSideChats(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := replacement.RecoverSideChatLaunch(t.Context(), "workflow", records); err != nil {
				t.Fatal(err)
			}
			snap, err := replacement.SideSnapshot(t.Context(), testSession, side.ID, time.Time{}, 50)
			if err != nil || snap.Side.State != tc.state || snap.Side.RecreateRequired != tc.recreate || sideProviderCount(h) != 0 {
				t.Fatalf("unsafe restore: %+v %v (provider count %d)", snap.Side, err, sideProviderCount(h))
			}
		})
	}
}

func TestDeferredSideKnownForkDoesNotProveQueuedOpeningWasUnsent(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	recorded := make(chan struct{})
	h.driver.afterFork = func(ctx context.Context) error { close(recorded); <-ctx.Done(); return ctx.Err() }
	side := draftSide(t, h, "recorded-opening")
	first, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "Keep this question", ClientMessageID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-recorded:
	case <-time.After(5 * time.Second):
		t.Fatal("fork ownership was not recorded")
	}
	records, err := h.svc.ExportSideChatLaunch(t.Context(), "workflow")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ProviderForkID == "" {
		t.Fatal("missing known fork fixture")
	}
	h.svc.StopAll(context.Background())
	replacement := chatsvc.New(chatsvc.Options{Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st, Drivers: fakeRegistry{driver: h.driver}, AppRunID: "workflow"})
	t.Cleanup(func() { replacement.StopAll(context.Background()) })
	if err := replacement.InitializeSideChats(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := replacement.RecoverSideChatLaunch(t.Context(), "workflow", records); err != nil {
		t.Fatal(err)
	}
	snap, err := replacement.SideSnapshot(t.Context(), testSession, side.ID, time.Time{}, 50)
	if err != nil || !snap.Side.RecreateRequired || snap.Side.State != "failed" || snap.Side.OpeningTurnID != first.ID || len(snap.Messages) != 1 || snap.Messages[0].Text != "Keep this question" {
		t.Fatalf("unsafe queued opening recovery: %+v %v", snap, err)
	}
	if err := replacement.RetrySideConnection(t.Context(), testSession, side.ID); !errors.Is(err, chatsvc.ErrSidePolicyRecreate) {
		t.Fatalf("uncertain first question was replayed: %v", err)
	}
	if sideProviderCount(h) != 0 {
		t.Fatal("unverified first question started a provider")
	}
}

func TestDeferredSideSettingsEditsDuringStartupOnlyAffectLaterTurns(t *testing.T) {
	h := newSideWorkflow(t, "")
	h.source(t)
	started, release := make(chan struct{}), make(chan struct{})
	h.driver.onFork = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	side := draftSide(t, h, "settings")
	if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "first", ClientMessageID: "first"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := h.svc.UpdateSideSettings(t.Context(), testSession, side.ID, "next-model", "high"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SendSideQuestion(t.Context(), testSession, side.ID, ports.ChatUserMessage{Text: "next", ClientMessageID: "next"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool {
		return sideProviderCount(h) == 1 && h.provider(0).sendCallCount() == 1
	})
	provider := h.provider(0)
	provider.mu.Lock()
	first := provider.sent[0].Settings
	provider.mu.Unlock()
	if first.Model != "initial" || first.Effort != "low" {
		t.Fatalf("opening settings changed: %+v", first)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSide(t, side.ID, func(s domain.SideSnapshot) bool { return provider.sendCallCount() == 2 })
	provider.mu.Lock()
	next := provider.sent[1].Settings
	provider.mu.Unlock()
	if next.Model != "next-model" || next.Effort != "high" {
		t.Fatalf("later settings ignored: %+v", next)
	}
}
