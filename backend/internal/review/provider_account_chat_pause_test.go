package review

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountReviewList struct {
	*fakeStore
	reviews []domain.Review
}

func (s *accountReviewList) ListReviewsBySession(context.Context, domain.SessionID) ([]domain.Review, error) {
	return s.reviews, nil
}

type accountReviewLauncher struct {
	fakeLauncher
	mu               sync.Mutex
	paused, released []string
	failID           string
	fail             error
}

func (l *accountReviewLauncher) AcquireReviewAccountPause(ctx context.Context, id string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.paused = append(l.paused, id)
	if id == l.failID {
		return nil, l.fail
	}
	var once sync.Once
	return func() { once.Do(func() { l.mu.Lock(); defer l.mu.Unlock(); l.released = append(l.released, id) }) }, nil
}
func TestProviderReviewChatPauseFencesAllIdleRelatedConversations(t *testing.T) {
	store := &accountReviewList{fakeStore: &fakeStore{}, reviews: []domain.Review{
		{ID: "codex-review", SessionID: "owner", Harness: domain.ReviewerCodex, InterfaceMode: domain.ReviewerInterfaceChat, ReviewerActivityState: domain.ActivityIdle},
		{ID: "terminal-review", SessionID: "owner", Harness: domain.ReviewerClaudeCode, InterfaceMode: domain.ReviewerInterfaceTUI, ReviewerActivityState: domain.ActivityIdle},
		{ID: "claude-review", SessionID: "owner", Harness: domain.ReviewerClaudeCode, InterfaceMode: domain.ReviewerInterfaceChat, ReviewerActivityState: domain.ActivityWaitingInput},
	}}
	launcher := &accountReviewLauncher{}
	engine := newEngineForTest(store, nil, nil, nil, launcher)
	release, err := engine.AcquireAccountRoutingPause(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(launcher.paused, []string{"codex-review", "claude-review"}) {
		t.Fatalf("related chat fences=%v", launcher.paused)
	}
	if len(launcher.released) != 0 {
		t.Fatal("review chat was released before account acknowledgement")
	}
	if engine.workerMutex("owner").TryLock() {
		engine.workerMutex("owner").Unlock()
		t.Fatal("new reviewer launch bypasses chat fence")
	}
	for _, review := range store.reviews {
		if review.ReviewerActivityState == domain.ActivityActive {
			t.Fatal("pause mutated native review activity")
		}
	}
	release()
	release()
	if !reflect.DeepEqual(launcher.released, []string{"claude-review", "codex-review"}) {
		t.Fatalf("reverse cleanup=%v", launcher.released)
	}
	if !engine.workerMutex("owner").TryLock() {
		t.Fatal("owner launch remains fenced after release")
	}
	engine.workerMutex("owner").Unlock()
	if launcher.cancelled || launcher.destroyed || launcher.spawned {
		t.Fatal("account fence interrupted or relaunched reviewer")
	}
}
func TestProviderReviewChatPauseRollsBackEarlierConversationsOnRefusal(t *testing.T) {
	for _, failure := range []error{ports.ErrProviderAccountBusy, errors.New("review controller storage unavailable"), context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			store := &accountReviewList{fakeStore: &fakeStore{}, reviews: []domain.Review{
				{ID: "idle", SessionID: "owner", InterfaceMode: domain.ReviewerInterfaceChat},
				{ID: "busy", SessionID: "owner", InterfaceMode: domain.ReviewerInterfaceChat},
				{ID: "unreached", SessionID: "owner", InterfaceMode: domain.ReviewerInterfaceChat},
			}}
			launcher := &accountReviewLauncher{failID: "busy", fail: failure}
			engine := newEngineForTest(store, nil, nil, nil, launcher)
			release, err := engine.AcquireAccountRoutingPause(context.Background(), "owner")
			if release != nil || !errors.Is(err, failure) {
				t.Fatalf("refused pause=%v release=%v", err, release != nil)
			}
			if !reflect.DeepEqual(launcher.paused, []string{"idle", "busy"}) {
				t.Fatalf("pause order=%v", launcher.paused)
			}
			if !reflect.DeepEqual(launcher.released, []string{"idle"}) {
				t.Fatalf("earlier reviewer remains paused=%v", launcher.released)
			}
			if !engine.workerMutex("owner").TryLock() {
				t.Fatal("review controller refusal retained owner launch fence")
			}
			engine.workerMutex("owner").Unlock()
			if launcher.cancelled || launcher.destroyed || launcher.spawned {
				t.Fatal("refusal changed a native reviewer")
			}
			launcher.failID = ""
			retry, err := engine.AcquireAccountRoutingPause(context.Background(), "owner")
			if err != nil {
				t.Fatal(err)
			}
			retry()
			if !reflect.DeepEqual(launcher.released, []string{"idle", "unreached", "busy", "idle"}) {
				t.Fatalf("explicit retry cleanup=%v", launcher.released)
			}
		})
	}
}
func TestProviderReviewChatPauseRejectsMissingControllerBoundary(t *testing.T) {
	store := &accountReviewList{fakeStore: &fakeStore{}, reviews: []domain.Review{{ID: "chat", SessionID: "owner", InterfaceMode: domain.ReviewerInterfaceChat}}}
	launcher := &fakeLauncher{}
	engine := newEngineForTest(store, nil, nil, nil, launcher)
	release, err := engine.AcquireAccountRoutingPause(context.Background(), "owner")
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("unguarded chat review=%v", err)
	}
	if !engine.workerMutex("owner").TryLock() {
		t.Fatal("missing boundary retained owner fence")
	}
	engine.workerMutex("owner").Unlock()
	if launcher.cancelled || launcher.spawned || launcher.destroyed {
		t.Fatal("missing boundary interrupted reviewer")
	}
}
func TestProviderReviewChatPauseActiveSiblingReleasesIdleFence(t *testing.T) {
	store := &accountReviewList{fakeStore: &fakeStore{}, reviews: []domain.Review{
		{ID: "idle", SessionID: "owner", InterfaceMode: domain.ReviewerInterfaceChat, ReviewerActivityState: domain.ActivityIdle},
		{ID: "active", SessionID: "owner", InterfaceMode: domain.ReviewerInterfaceChat, ReviewerActivityState: domain.ActivityActive},
	}}
	launcher := &accountReviewLauncher{}
	engine := newEngineForTest(store, nil, nil, nil, launcher)
	release, err := engine.AcquireAccountRoutingPause(context.Background(), "owner")
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("active sibling=%v", err)
	}
	if !reflect.DeepEqual(launcher.paused, []string{"idle"}) || !reflect.DeepEqual(launcher.released, []string{"idle"}) {
		t.Fatal("active sibling leaked earlier idle fence")
	}
	if launcher.cancelled || launcher.destroyed {
		t.Fatal("active sibling was interrupted")
	}
	store.reviews[1].ReviewerActivityState = domain.ActivityIdle
	retry, err := engine.AcquireAccountRoutingPause(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	retry()
	if !reflect.DeepEqual(launcher.released, []string{"idle", "active", "idle"}) {
		t.Fatalf("idle retry cleanup=%v", launcher.released)
	}
}

type pausedReviewChat struct {
	sqliteReviewChatController
	paused, released []string
	failure          error
}

func (c *pausedReviewChat) AcquireReviewAccountPause(_ context.Context, id string) (func(), error) {
	c.paused = append(c.paused, id)
	if c.failure != nil {
		return nil, c.failure
	}
	return func() { c.released = append(c.released, id) }, nil
}
func TestProviderReviewLauncherDelegatesPauseToTypedChatOwner(t *testing.T) {
	chat := &pausedReviewChat{}
	launcher := &agentLauncher{chat: chat}
	release, err := launcher.AcquireReviewAccountPause(context.Background(), "saved-review")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chat.paused, []string{"saved-review"}) || len(chat.released) != 0 {
		t.Fatal("launcher did not hold typed review owner")
	}
	release()
	if !reflect.DeepEqual(chat.released, []string{"saved-review"}) {
		t.Fatal("launcher did not release typed reviewer owner")
	}
	chat.failure = ports.ErrProviderAccountBusy
	if release, err = launcher.AcquireReviewAccountPause(context.Background(), "busy-review"); release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("launcher swallowed busy chat=%v", err)
	}
	launcher.chat = &sqliteReviewChatController{}
	if release, err = launcher.AcquireReviewAccountPause(context.Background(), "unfenced-review"); release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatal("launcher admitted missing account fence")
	}
}
