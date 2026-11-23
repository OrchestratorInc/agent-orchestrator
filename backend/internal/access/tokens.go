package access

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// TokenPrefix marks an agent token, so it is never mistaken for a device token.
const TokenPrefix = "aoa_"

const (
	maxTokens     = 64
	maxNameLength = 64
	touchInterval = 5 * time.Minute
)

// ErrTooManyTokens is returned by Create when the registry is full.
var ErrTooManyTokens = errors.New("too many agent tokens; revoke one first")

// Token is a named credential for an agent outside the app. Only the SHA-256 of
// its secret is stored.
type Token struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"tokenHash"`
	Grant
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt,omitzero"`
	RevokedAt  time.Time `json:"revokedAt,omitzero"`
}

// Active reports whether the token still authenticates a caller.
func (t Token) Active(now time.Time) bool {
	return t.RevokedAt.IsZero() && (t.ExpiresAt.IsZero() || now.Before(t.ExpiresAt))
}

type tokensFile struct {
	Tokens []Token `json:"tokens"`
	// Sessions maps a session to the token whose grant it started under.
	Sessions map[string]string `json:"sessions,omitempty"`
}

// TokensPath returns the registry's path in dataDir.
func TokensPath(dataDir string) string {
	return filepath.Join(dataDir, "access", "tokens.json")
}

// Registry holds the agent tokens this machine accepts and the sessions that
// carry a token's grant.
type Registry struct {
	mu       sync.RWMutex
	path     string
	writeErr error
	tokens   map[string]Token
	byHash   map[string]string
	sessions map[string]string
}

// LoadRegistry reads the registry. A damaged file is moved aside and reported,
// and the registry then refuses writes only if the move failed.
func LoadRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, tokens: map[string]Token{}, byHash: map[string]string{}, sessions: map[string]string{}}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is the daemon's own data dir.
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	var file tokensFile
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		cause := fmt.Errorf("read agent tokens: %w", err)
		if moveErr := os.Rename(path, path+".bad"); moveErr != nil {
			r.writeErr = fmt.Errorf("%w; moving it aside failed: %w", cause, moveErr)
			return r, r.writeErr
		}
		return r, cause
	}
	for _, token := range file.Tokens {
		if token.ID == "" {
			continue
		}
		r.tokens[token.ID] = token
		if token.TokenHash != "" {
			r.byHash[token.TokenHash] = token.ID
		}
	}
	for session, tokenID := range file.Sessions {
		if _, ok := r.tokens[tokenID]; ok {
			r.sessions[session] = tokenID
		}
	}
	return r, nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func cleanName(name string) string {
	name = strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, name))
	return name
}

// Create registers a token and returns it with its secret, which is shown once.
func (r *Registry) Create(name string, grant Grant, now time.Time) (Token, string, error) {
	name = cleanName(name)
	if name == "" || len(name) > maxNameLength {
		return Token{}, "", fmt.Errorf("a token name is required, up to %d characters", maxNameLength)
	}
	if !grant.ExpiresAt.IsZero() && !now.Before(grant.ExpiresAt) {
		return Token{}, "", errors.New("the expiry is in the past")
	}
	secret, id := make([]byte, 32), make([]byte, 8)
	if _, err := rand.Read(secret); err != nil {
		return Token{}, "", err
	}
	if _, err := rand.Read(id); err != nil {
		return Token{}, "", err
	}
	value := TokenPrefix + hex.EncodeToString(secret)
	token := Token{ID: "tok_" + hex.EncodeToString(id), Name: name, TokenHash: hashSecret(value), Grant: grant, CreatedAt: now.UTC()}

	r.mu.Lock()
	defer r.mu.Unlock()
	live := 0
	for _, existing := range r.tokens {
		if !existing.RevokedAt.IsZero() {
			continue
		}
		live++
		if strings.EqualFold(existing.Name, name) {
			return Token{}, "", fmt.Errorf("a token named %q already exists; revoke it first", existing.Name)
		}
	}
	if live >= maxTokens {
		return Token{}, "", ErrTooManyTokens
	}
	r.tokens[token.ID] = token
	r.byHash[token.TokenHash] = token.ID
	if err := r.persistLocked(); err != nil {
		delete(r.tokens, token.ID)
		delete(r.byHash, token.TokenHash)
		return Token{}, "", err
	}
	return token, value, nil
}

// Match returns the active token a presented secret belongs to.
func (r *Registry) Match(secret string, now time.Time) (Token, bool) {
	if r == nil || !strings.HasPrefix(secret, TokenPrefix) {
		return Token{}, false
	}
	hash := hashSecret(secret)
	r.mu.RLock()
	defer r.mu.RUnlock()
	token, ok := r.tokens[r.byHash[hash]]
	return token, ok && token.Active(now)
}

// Get returns a token by ID, active or not.
func (r *Registry) Get(id string) (Token, bool) {
	if r == nil {
		return Token{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	token, ok := r.tokens[id]
	return token, ok
}

// Touch records that a token authenticated a request.
func (r *Registry) Touch(id string, now time.Time) {
	r.mu.RLock()
	token, ok := r.tokens[id]
	r.mu.RUnlock()
	if !ok || now.Sub(token.LastUsedAt) < touchInterval {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if token, ok = r.tokens[id]; !ok {
		return
	}
	token.LastUsedAt = now.UTC()
	r.tokens[id] = token
	_ = r.persistLocked()
}

// Revoke ends a token, found by ID or name. Sessions it started stay bound to
// it, so they are refused from then on.
func (r *Registry) Revoke(idOrName string, now time.Time) (Token, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var token Token
	found := false
	for _, candidate := range r.tokens {
		if candidate.RevokedAt.IsZero() && (candidate.ID == idOrName || strings.EqualFold(candidate.Name, idOrName)) {
			token, found = candidate, true
			break
		}
	}
	if !found {
		return Token{}, false, nil
	}
	previous := token
	token.RevokedAt = now.UTC()
	bound := false
	for _, tokenID := range r.sessions {
		if tokenID == token.ID {
			bound = true
			break
		}
	}
	delete(r.byHash, previous.TokenHash)
	if bound {
		token.TokenHash = ""
		r.tokens[token.ID] = token
	} else {
		delete(r.tokens, token.ID)
	}
	if err := r.persistLocked(); err != nil {
		r.tokens[previous.ID] = previous
		r.byHash[previous.TokenHash] = previous.ID
		return Token{}, true, err
	}
	return token, true, nil
}

// List returns the tokens, oldest first, including ended ones that sessions still carry.
func (r *Registry) List() []Token {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Token, 0, len(r.tokens))
	for _, token := range r.tokens {
		out = append(out, token)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Bind records that a session started under a token's grant.
func (r *Registry) Bind(sessionID, tokenID string) error {
	if sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tokens[tokenID]; !ok || r.sessions[sessionID] == tokenID {
		return nil
	}
	previous, had := r.sessions[sessionID]
	r.sessions[sessionID] = tokenID
	if err := r.persistLocked(); err != nil {
		if had {
			r.sessions[sessionID] = previous
		} else {
			delete(r.sessions, sessionID)
		}
		return err
	}
	return nil
}

// ForSession returns the token whose grant a session carries.
func (r *Registry) ForSession(sessionID string) (Token, bool) {
	if r == nil || sessionID == "" {
		return Token{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	token, ok := r.tokens[r.sessions[sessionID]]
	return token, ok
}

// ponytail: bindings are kept for every session a token ever started, about 60
// bytes each. Prune by live session IDs if the file grows past a few thousand rows.
func (r *Registry) persistLocked() error {
	if r.writeErr != nil {
		return r.writeErr
	}
	file := tokensFile{Tokens: make([]Token, 0, len(r.tokens)), Sessions: r.sessions}
	for _, token := range r.tokens {
		file.Tokens = append(file.Tokens, token)
	}
	sort.Slice(file.Tokens, func(i, j int) bool { return file.Tokens[i].ID < file.Tokens[j].ID })
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), "."+filepath.Base(r.path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}
