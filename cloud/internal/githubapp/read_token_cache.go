package githubapp

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// readTokenReuseMargin is how much lifetime a cached read token must have left
// to be handed out again. GitHub installation tokens live for an hour, so a
// token is reused for about fifty minutes, and a caller never receives one that
// expires mid-clone.
const readTokenReuseMargin = 10 * time.Minute

// readTokenCache reuses contents:read installation tokens per installation and
// repository set. Minting one is a GitHub API round trip on every session start
// (checkout) and every git operation a worker's credential helper makes;
// within a token's lifetime the same scope yields an interchangeable token.
//
// Only read tokens are cached. Write tokens are minted just before a push or a
// pull-request call and never kept, as before. Authorization is unaffected:
// every caller still resolves and checks the session's repository authority
// before asking for a token, so the cache only ever answers a request that was
// already allowed to mint the same scope.
//
// Concurrent misses for one scope share a single mint. Failures are never
// cached.
type readTokenCache struct {
	mu       sync.Mutex
	tokens   map[string]installationAccessToken
	inflight map[string]*readTokenCall
}

type readTokenCall struct {
	done  chan struct{}
	token installationAccessToken
	err   error
}

func readTokenKey(installationID int64, repositoryIDs []int64) string {
	ids := slices.Clone(repositoryIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var key strings.Builder
	key.WriteString(strconv.FormatInt(installationID, 10))
	for _, id := range ids {
		key.WriteByte(':')
		key.WriteString(strconv.FormatInt(id, 10))
	}
	return key.String()
}

// get returns a cached token for key with at least readTokenReuseMargin left,
// or mints one with mint. now is the client's clock.
func (c *readTokenCache) get(
	ctx context.Context,
	key string,
	now func() time.Time,
	mint func(context.Context) (installationAccessToken, error),
) (installationAccessToken, error) {
	c.mu.Lock()
	if c.tokens == nil {
		c.tokens = map[string]installationAccessToken{}
		c.inflight = map[string]*readTokenCall{}
	}
	if token, ok := c.tokens[key]; ok && token.ExpiresAt.After(now().Add(readTokenReuseMargin)) {
		c.mu.Unlock()
		return token, nil
	}
	call, joined := c.inflight[key]
	if !joined {
		call = &readTokenCall{done: make(chan struct{})}
		c.inflight[key] = call
	}
	c.mu.Unlock()

	if joined {
		select {
		case <-call.done:
			return call.token, call.err
		case <-ctx.Done():
			return installationAccessToken{}, ctx.Err()
		}
	}

	// The mint runs on its own context so one caller's cancellation does not
	// fail the others waiting on it; it is bounded by the HTTP client timeout.
	call.token, call.err = mint(context.WithoutCancel(ctx))
	c.mu.Lock()
	delete(c.inflight, key)
	if call.err == nil {
		c.tokens[key] = call.token
		c.pruneLocked(now())
	}
	c.mu.Unlock()
	close(call.done)
	return call.token, call.err
}

// pruneLocked drops tokens that can no longer be handed out, keeping the cache
// bounded by the scopes used within the last hour.
func (c *readTokenCache) pruneLocked(now time.Time) {
	for key, token := range c.tokens {
		if !token.ExpiresAt.After(now.Add(readTokenReuseMargin)) {
			delete(c.tokens, key)
		}
	}
}
