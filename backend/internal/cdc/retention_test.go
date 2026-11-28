package cdc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeRetentionStore struct {
	ageRows  []int64
	sizeRows []int64
	ageErr   error
	sizeErr  error
	ages     int
	sizes    int
}

func (s *fakeRetentionStore) PruneChangeLogBefore(context.Context, time.Time, int64) (int64, error) {
	s.ages++
	if s.ageErr != nil {
		return 0, s.ageErr
	}
	return nextRetentionResult(&s.ageRows), nil
}

func (s *fakeRetentionStore) PruneChangeLogToMaxRows(context.Context, int64, int64) (int64, error) {
	s.sizes++
	if s.sizeErr != nil {
		return 0, s.sizeErr
	}
	return nextRetentionResult(&s.sizeRows), nil
}

func nextRetentionResult(rows *[]int64) int64 {
	if len(*rows) == 0 {
		return 0
	}
	n := (*rows)[0]
	*rows = (*rows)[1:]
	return n
}

func TestRetentionJanitorRunOncePrunesAgeAndSizeUntilStable(t *testing.T) {
	store := &fakeRetentionStore{
		ageRows:  []int64{3, 3, 1},
		sizeRows: []int64{3, 0},
	}
	j := NewRetentionJanitor(store, RetentionConfig{
		Retention: time.Hour,
		MaxRows:   10,
		Batch:     3,
		Clock:     func() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.ages != 3 || store.sizes != 2 {
		t.Fatalf("prune calls = age %d/size %d, want 3/2", store.ages, store.sizes)
	}
	if len(store.ageRows) != 0 || len(store.sizeRows) != 0 {
		t.Fatalf("unconsumed results = age %v/size %v", store.ageRows, store.sizeRows)
	}
}

func TestRetentionJanitorRunOnceReturnsStoreError(t *testing.T) {
	want := errors.New("busy")
	store := &fakeRetentionStore{ageErr: want}
	j := NewRetentionJanitor(store, RetentionConfig{Logger: slog.Default()})
	if err := j.RunOnce(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
