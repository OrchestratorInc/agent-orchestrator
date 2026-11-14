package observe

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

func StartPollLoop(ctx context.Context, tick time.Duration, poll func(context.Context) error, logger *slog.Logger, name string) <-chan struct{} {
	return StartTriggeredPollLoop(ctx, tick, nil, poll, logger, name)
}

func StartTriggeredPollLoop(ctx context.Context, tick time.Duration, trigger <-chan struct{}, poll func(context.Context) error, logger *slog.Logger, name string) <-chan struct{} {
	if logger == nil {
		logger = slog.Default()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error(name+": initial poll failed", "err", err)
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-trigger:
			}
			if err := poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error(name+": poll failed", "err", err)
			}
		}
	}()
	return done
}

type CredentialProbe func(ctx context.Context) (available bool, err error)

func CheckCredentialsOnce(ctx context.Context, probe CredentialProbe, checked, disabled *bool, logger *slog.Logger, name string) (bool, error) {
	if *checked && !*disabled {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	if probe == nil {
		*checked = true
		*disabled = false
		return true, nil
	}
	available, err := probe(ctx)
	if err != nil {
		if !*checked {
			logger.Warn(name+" credentials check failed; will retry", "err", err)
		}
		return false, nil
	}
	*checked = true
	if !available {
		if !*disabled {
			logger.Warn(name + " disabled: provider credentials unavailable")
		}
		*disabled = true
		return false, nil
	}
	*disabled = false
	return true, nil
}

func CacheSet[V any](m map[string]V, order *[]string, maxEntries int, key string, value V) {
	if _, ok := m[key]; !ok {
		*order = append(*order, key)
	}
	m[key] = value
	if maxEntries <= 0 {
		return
	}
	for len(*order) > maxEntries {
		evict := (*order)[0]
		*order = (*order)[1:]
		delete(m, evict)
	}
}

func CacheDelete[V any](m map[string]V, order *[]string, key string) {
	if _, ok := m[key]; !ok {
		return
	}
	delete(m, key)
	dst := (*order)[:0]
	for _, cachedKey := range *order {
		if cachedKey != key {
			dst = append(dst, cachedKey)
		}
	}
	*order = dst
}
