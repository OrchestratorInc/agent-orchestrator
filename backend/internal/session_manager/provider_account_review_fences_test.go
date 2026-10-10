package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type accountReviewerFence struct {
	fakeReviewerTerminator
	paused   []domain.SessionID
	released []domain.SessionID
	refuse   domain.SessionID
	problem  error
}

func (f *accountReviewerFence) AcquireAccountRoutingPause(_ context.Context, id domain.SessionID) (func(), error) {
	f.paused = append(f.paused, id)
	if id == f.refuse {
		return nil, f.problem
	}
	var once sync.Once
	return func() { once.Do(func() { f.released = append(f.released, id) }) }, nil
}

func TestAccountManagerFencesEveryRelatedReviewerAndWorkerBeforeAllowingBatchChange(t *testing.T) {
	l := &accountPauseLauncher{}
	m, st, _ := newChatManager(l)
	reviewer := &accountReviewerFence{}
	m.SetReviewerTerminator(reviewer)
	for _, id := range []domain.SessionID{"first", "second"} {
		st.sessions[id] = domain.SessionRecord{ID: id, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	}
	release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"first", "second"})
	if err != nil || release == nil {
		t.Fatalf("idle batch refused: %v", err)
	}
	if !reflect.DeepEqual(reviewer.paused, []domain.SessionID{"first", "second"}) || !reflect.DeepEqual(l.paused, reviewer.paused) {
		t.Fatal("batch did not fence both reviewers and worker conversations")
	}
	for _, id := range []domain.SessionID{"first", "second"} {
		if _, allowed := m.AcquireSessionInput(id); allowed {
			t.Fatalf("worker %s input bypassed account mutation", id)
		}
	}
	done, allowed := m.AcquireSessionInput("unrelated")
	if !allowed {
		t.Fatal("batch mutation blocked an unrelated session")
	}
	done()
	release()
	release()
	if !reflect.DeepEqual(reviewer.released, []domain.SessionID{"second", "first"}) || !reflect.DeepEqual(l.released, reviewer.released) {
		t.Fatal("batch release did not unwind each acquired boundary once")
	}
	for _, id := range []domain.SessionID{"first", "second"} {
		done, allowed := m.AcquireSessionInput(id)
		if !allowed {
			t.Fatalf("worker %s stayed fenced after account change", id)
		}
		done()
	}
	if len(reviewer.calls)+len(reviewer.teardownCalls)+len(reviewer.restoreCalls) != 0 {
		t.Fatal("account mutation interrupted or replaced related reviewer work")
	}
}

func TestAccountManagerRelatedReviewerRefusalRollsBackAllEarlierBoundaries(t *testing.T) {
	for _, failure := range []error{ports.ErrProviderAccountBusy, errors.New("reviewer activity store unavailable"), context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			l := &accountPauseLauncher{}
			m, st, _ := newChatManager(l)
			reviewer := &accountReviewerFence{refuse: "second", problem: failure}
			m.SetReviewerTerminator(reviewer)
			for _, id := range []domain.SessionID{"first", "second", "unvisited"} {
				st.sessions[id] = domain.SessionRecord{ID: id, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
			}
			release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"first", "second", "unvisited"})
			if release != nil || !errors.Is(err, failure) {
				t.Fatalf("related reviewer refusal lost: %v", err)
			}
			if !reflect.DeepEqual(reviewer.paused, []domain.SessionID{"first", "second"}) || !reflect.DeepEqual(reviewer.released, []domain.SessionID{"first"}) {
				t.Fatal("reviewer refusal retained an earlier reviewer fence")
			}
			if !reflect.DeepEqual(l.paused, []domain.SessionID{"first"}) || !reflect.DeepEqual(l.released, []domain.SessionID{"first"}) {
				t.Fatal("reviewer refusal paused a later worker or retained an earlier worker")
			}
			for _, id := range []domain.SessionID{"first", "second", "unvisited"} {
				done, allowed := m.AcquireSessionInput(id)
				if !allowed {
					t.Fatalf("refused batch retained input fence on %s", id)
				}
				done()
			}
			if len(reviewer.calls)+len(reviewer.teardownCalls)+len(reviewer.restoreCalls) != 0 {
				t.Fatal("account refusal changed related reviewer lifecycle")
			}
			reviewer.refuse = ""
			retry, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"first", "second"})
			if err != nil {
				t.Fatal(err)
			}
			retry()
		})
	}
}

func TestAccountManagerWorkerRefusalReleasesItsPreviouslyAcquiredReviewerFence(t *testing.T) {
	l := &accountPauseLauncher{pauseErr: ports.ErrProviderAccountBusy}
	m, st, _ := newChatManager(l)
	reviewer := &accountReviewerFence{}
	m.SetReviewerTerminator(reviewer)
	st.sessions["s"] = domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("worker refusal lost: %v", err)
	}
	if !reflect.DeepEqual(reviewer.paused, []domain.SessionID{"s"}) || !reflect.DeepEqual(reviewer.released, reviewer.paused) {
		t.Fatal("worker refusal left its related reviewer fenced")
	}
	done, allowed := m.AcquireSessionInput("s")
	if !allowed {
		t.Fatal("worker refusal left session input paused")
	}
	done()
	if len(l.released) != 0 {
		t.Fatal("worker refusal released a chat admission never acquired")
	}
	if len(reviewer.calls)+len(reviewer.teardownCalls)+len(reviewer.restoreCalls) != 0 {
		t.Fatal("worker refusal interrupted the reviewer")
	}
}

func TestAccountManagerFencesReviewersAfterWorkerExit(t *testing.T) {
	for _, terminated := range []bool{false, true} {
		name := "exited"
		if terminated {
			name = "terminated"
		}
		t.Run(name, func(t *testing.T) {
			l := &accountPauseLauncher{}
			m, st, _ := newChatManager(l)
			st.sessions["s"] = domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, IsTerminated: terminated, Activity: domain.Activity{State: domain.ActivityExited}}
			reviewer := &accountReviewerFence{refuse: "s", problem: ports.ErrProviderAccountBusy}
			m.SetReviewerTerminator(reviewer)
			release, err := m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
			if release != nil {
				release()
			}
			if !errors.Is(err, ports.ErrProviderAccountBusy) {
				t.Fatalf("busy reviewer of %s worker was not protected: %v", name, err)
			}
			if !reflect.DeepEqual(reviewer.paused, []domain.SessionID{"s"}) || len(l.paused) != 0 {
				t.Fatal("must inspect the reviewer without requiring an exited worker controller")
			}
			done, allowed := m.AcquireSessionInput("s")
			if !allowed {
				t.Fatal("refusal retained worker operation fence")
			}
			done()
			reviewer.refuse = ""
			release, err = m.AcquireAccountMutation(context.Background(), []domain.SessionID{"s"})
			if err != nil {
				t.Fatal(err)
			}
			if len(reviewer.released) != 0 {
				t.Fatal("reviewer admission released before mutation completed")
			}
			if _, allowed := m.AcquireSessionInput("s"); allowed {
				t.Fatal("worker restore/input bypassed routing mutation")
			}
			release()
			release()
			if !reflect.DeepEqual(reviewer.released, []domain.SessionID{"s"}) {
				t.Fatal("reviewer fence was not released exactly once")
			}
			if len(l.paused) != 0 || len(reviewer.calls)+len(reviewer.teardownCalls)+len(reviewer.restoreCalls) != 0 {
				t.Fatal("routing changed worker or reviewer lifecycle")
			}
		})
	}
}
