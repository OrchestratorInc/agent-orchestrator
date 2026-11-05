package store

import (
	"context"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// GetSchedulerLeader 读取当前任期；本方法不授予派发许可。
func (s *Store) GetSchedulerLeader(ctx context.Context) (domain.SchedulerLeader, error) {
	r, err := s.qr.GetSchedulerLeader(ctx)
	return schedulerLeaderFromGen(r), err
}

// AcquireSchedulerLeader 仅在旧租约到期后原子增加任期，接管者必须先恢复。
func (s *Store) AcquireSchedulerLeader(ctx context.Context, owner string, now, until time.Time) (domain.SchedulerLeader, bool, error) {
	if strings.TrimSpace(owner) == "" || now.IsZero() || !until.After(now) {
		return domain.SchedulerLeader{}, false, domain.ErrSchedulerFence
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var leader domain.SchedulerLeader
	var changed bool
	err := s.inSchedulerTx(ctx, "acquire scheduler leader", func(q *gen.Queries) error {
		n, err := q.AcquireSchedulerLeader(ctx, gen.AcquireSchedulerLeaderParams{OwnerID: owner, Now: now.UTC(), LeaseUntil: until.UTC()})
		if err != nil {
			return err
		}
		r, err := q.GetSchedulerLeader(ctx)
		leader = schedulerLeaderFromGen(r)
		changed = n == 1
		return err
	})
	return leader, changed, err
}

// RenewSchedulerLeader 同时比较 owner、epoch、revision 与未过期租约。
func (s *Store) RenewSchedulerLeader(ctx context.Context, expected domain.SchedulerLeader, now, until time.Time) (domain.SchedulerLeader, bool, error) {
	if !until.After(now) || until.Before(expected.LeaseUntil) {
		return domain.SchedulerLeader{}, false, domain.ErrSchedulerFence
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var leader domain.SchedulerLeader
	err := s.inSchedulerTx(ctx, "renew scheduler leader", func(q *gen.Queries) error {
		n, err := q.RenewSchedulerLeader(ctx, gen.RenewSchedulerLeaderParams{OwnerID: expected.Fence.OwnerID, Epoch: expected.Fence.Epoch, Revision: expected.Revision, Now: now.UTC(), LeaseUntil: until.UTC()})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerFence
		}
		r, err := q.GetSchedulerLeader(ctx)
		leader = schedulerLeaderFromGen(r)
		return err
	})
	return leader, err == nil, err
}

// CompleteSchedulerRecovery 由恢复协调者在所有不确定执行已重新占位后开放准入。
func (s *Store) CompleteSchedulerRecovery(ctx context.Context, f domain.SchedulerFence, revision int64, now time.Time) (domain.SchedulerLeader, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var leader domain.SchedulerLeader
	err := s.inSchedulerTx(ctx, "complete scheduler recovery", func(q *gen.Queries) error {
		n, err := q.CompleteSchedulerRecovery(ctx, gen.CompleteSchedulerRecoveryParams{OwnerID: f.OwnerID, Epoch: f.Epoch, Revision: revision, Now: now.UTC()})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerFence
		}
		r, err := q.GetSchedulerLeader(ctx)
		leader = schedulerLeaderFromGen(r)
		return err
	})
	return leader, err == nil, err
}

// schedulerFence 在同一 writer 事务内拒绝过期、换主与未恢复的执行资格。
func schedulerFence(ctx context.Context, q *gen.Queries, f domain.SchedulerFence, now time.Time, recovered bool) error {
	r, err := q.GetSchedulerLeader(ctx)
	if err != nil {
		return err
	}
	if now.IsZero() || r.OwnerID != f.OwnerID || r.Epoch != f.Epoch || r.Epoch <= 0 || !r.LeaseUntil.After(now) || recovered && r.Recovered != 1 {
		return domain.ErrSchedulerFence
	}
	return nil
}

func schedulerLeaderFromGen(r gen.SchedulerLeader) domain.SchedulerLeader {
	return domain.SchedulerLeader{Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, LeaseUntil: r.LeaseUntil, Revision: r.Revision, Recovered: r.Recovered == 1, Cursor: r.Cursor}
}

// inSchedulerTx 在读取前取得 SQLite writer 锁，避免两个 deferred 事务同时读后升级而 SQLITE_BUSY。
func (s *Store) inSchedulerTx(ctx context.Context, what string, fn func(*gen.Queries) error) error {
	return s.inTx(ctx, what, func(q *gen.Queries) error {
		if err := q.LockSchedulerWriter(ctx); err != nil {
			return err
		}
		return fn(q)
	})
}
