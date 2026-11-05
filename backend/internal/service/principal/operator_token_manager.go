package principal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Operator token adoption: NOT WIRED IN PRODUCTION ANY MORE.
//
// # Status: retained, unreferenced, deliberately not deleted
//
// Production wiring (daemon/workflow_auth_wiring.go) no longer constructs a TokenManager, mints a
// token, reads one, or offers an adopt route: a same-user process can read the file, so a token
// cannot prove a human. This file and its test survive because they are the executable record of
// WHY that is true — they still show that a token file exists on disk, is readable by a second
// same-user process, and is the thing adoption had to remove. Deleting them would delete the
// evidence, not the risk.
//
// Nothing below is on any request path. If it ever becomes reachable again, that is a security
// regression, not a restoration.
//
// # The hole it used to close, stated plainly
//
// operator_token.go already says the token file is not isolation: on Windows any same-user
// process can read it. Combined with an approval whose creation required no human (`POST
// /operator/approvals` grants nothing and was loopback-only), that was a complete bypass — an
// agent shell could create an approval, read the token, present it to the confirm route, and
// consume the approval it just created. The human gesture would be decorative.
//
// # What replaced it
//
// Native OS consent: the confirm route asks the operating system to verify the user, and the
// resulting approval is per-boot, single-use, and bound to the exact operation and payload bytes.
// There is no credential file to read because there is no credential.

// TokenManager owns this process's operator secret in memory.
//
// The value is swapped under a mutex and read under it, so adoption is safe against concurrent
// verification. Nothing here is durable: the process has no copy on disk, which is the point.
type TokenManager struct {
	mu      sync.RWMutex
	token   string
	dataDir string
	// adopted records that a token was ever loaded into memory, so Boot can tell "we adopted" from
	// "we are about to mint".
	adopted bool
}

// NewTokenManager builds a manager for one data dir. It reads nothing yet.
func NewTokenManager(dataDir string) *TokenManager {
	return &TokenManager{dataDir: filepath.Clean(strings.TrimSpace(dataDir))}
}

// Verifier returns the token comparison bound to this manager's CURRENT value.
//
// It resolves the token on every call rather than capturing it, so adopting a new value takes
// effect immediately for every trust built earlier and for every request in flight. A manager with
// no token in memory verifies nothing, which is the fail-closed answer.
func (m *TokenManager) Verifier() TokenVerifier {
	if m == nil {
		return nil
	}
	return func(presented string) bool {
		current := m.Current()
		if current == "" {
			return false
		}
		return constantTimeEqual(strings.TrimSpace(presented), current)
	}
}

// Current returns the in-memory token, or "" when none has been adopted.
func (m *TokenManager) Current() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.token
}

// Present reports whether this process holds a token in memory.
func (m *TokenManager) Present() bool {
	return m.Current() != ""
}

// set stores a token, which must be well formed.
func (m *TokenManager) set(token string) error {
	token = strings.TrimSpace(token)
	if len(token) != operatorTokenBytes*2 {
		return fmt.Errorf("%w: malformed token", ErrOperatorTokenUnavailable)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = token
	m.adopted = true
	return nil
}

// Boot loads (or mints) the data dir's token, keeps it in memory, and REMOVES the file.
//
// Minting when the file is absent is kept deliberately: a daemon that starts before any desktop app
// exists must still be able to serve its own operator surfaces, and the token it mints is exactly
// the one its own desktop app will adopt on its next start.
//
// The returned fingerprint is audit-safe and is the only thing about the token that may be logged.
func (m *TokenManager) Boot() (fingerprint string, err error) {
	if m == nil || m.dataDir == "" || m.dataDir == "." {
		return "", fmt.Errorf("%w: no data dir", ErrOperatorTokenUnavailable)
	}
	token, err := LoadOrCreateOperatorToken(m.dataDir)
	if err != nil {
		return "", err
	}
	if err := m.set(token); err != nil {
		return "", err
	}
	if err := removeTokenFile(m.dataDir); err != nil {
		return "", err
	}
	return OperatorTokenFingerprint(token), nil
}

// Adopt reads the data dir's token file into memory and deletes it.
//
// removed reports whether a file was actually deleted. A missing file is not an error — another
// process (or an earlier start) may already have adopted it — and in that case the in-memory token
// is left untouched, because the caller can only reach this after proving the token it already
// holds.
func (m *TokenManager) Adopt() (fingerprint string, removed bool, err error) {
	if m == nil || m.dataDir == "" || m.dataDir == "." {
		return "", false, fmt.Errorf("%w: no data dir", ErrOperatorTokenUnavailable)
	}
	token, ok := readOperatorToken(filepath.Join(m.dataDir, OperatorTokenFileName))
	if ok {
		if err := m.set(token); err != nil {
			return "", false, err
		}
		if err := removeTokenFile(m.dataDir); err != nil {
			return "", false, err
		}
		return OperatorTokenFingerprint(token), true, nil
	}
	current := m.Current()
	if current == "" {
		return "", false, fmt.Errorf("%w: %s is absent and nothing was adopted",
			ErrOperatorTokenUnavailable, OperatorTokenFileName)
	}
	return OperatorTokenFingerprint(current), false, nil
}

// Adopted reports whether this process ever loaded a token into memory. A manager that never
// adopted has nothing to verify against, and must fail closed.
func (m *TokenManager) Adopted() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adopted
}

// AdoptOperatorToken is the one-shot adoption form: it loads <dataDir>/operator.token into this
// process, deletes the file, and reports the audit-safe fingerprint.
//
// It is for a caller that ALREADY holds the token value itself (the desktop main process reads it
// before calling the adopt route, so it needs nothing back). The daemon uses TokenManager
// instead, because the daemon must keep the value to verify against.
//
// It never returns the token: the only thing about the secret that may leave this function is its
// fingerprint.
func AdoptOperatorToken(dataDir string) (fingerprint string, removed bool, err error) {
	manager := NewTokenManager(dataDir)
	return manager.Adopt()
}

// removeTokenFile deletes <dataDir>/operator.token, tolerating an already-absent file.
//
// A deletion failure is reported rather than swallowed: leaving the secret on disk after claiming
// to have adopted it would be a silent security regression, and the caller (startup, or the adopt
// route) must be able to say so.
func removeTokenFile(dataDir string) error {
	path := filepath.Join(dataDir, OperatorTokenFileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: remove %s: %w", ErrOperatorTokenUnavailable, OperatorTokenFileName, err)
	}
	return nil
}

// NewManagerTokenTrust builds the token proof over a manager's in-memory value.
//
// It is the production constructor for a daemon that adopts: the trust resolves the token on every
// request, so a value adopted later is accepted without rebuilding the resolver.
func NewManagerTokenTrust(manager *TokenManager) TokenOperatorTrust {
	if manager == nil {
		return TokenOperatorTrust{}
	}
	return TokenOperatorTrust{Verifier: manager.Verifier()}
}
