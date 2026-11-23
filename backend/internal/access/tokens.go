package access

import (
	"context"
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
	"unicode/utf8"
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

// ErrNotSaved is returned by Create when the token file could not be written.
var ErrNotSaved = errors.New("the agent token could not be saved")

// Token is a named credential for an agent outside the app.
type Token struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TokenHash string `json:"tokenHash"`
	Grant
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt,omitzero"`
}

// Active reports whether the token still authenticates a caller.
func (t Token) Active(now time.Time) bool {
	return t.ExpiresAt.IsZero() || now.Before(t.ExpiresAt)
}

type tokensFile struct {
	Tokens []Token `json:"tokens"`
}

// TokensPath returns the registry's path in dataDir.
func TokensPath(dataDir string) string {
	return filepath.Join(dataDir, "access", "tokens.json")
}

// Registry holds the agent tokens this machine accepts and the sessions that started under one.
type Registry struct {
	mu           sync.RWMutex
	path         string
	writeErr     error
	tokens       map[string]Token
	byHash       map[string]string
	sessions     map[string]string
	sessionsLost bool
	unbound      map[string]bool
}

// LoadRegistry reads the registry from path.
func LoadRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, tokens: map[string]Token{}, byHash: map[string]string{}, sessions: map[string]string{}, unbound: map[string]bool{}}
	bindingsErr := r.loadBindings()
	raw, err := os.ReadFile(path) // #nosec G304 -- path is the daemon's own data dir.
	if errors.Is(err, fs.ErrNotExist) {
		return r, bindingsErr
	}
	if err != nil {
		r.writeErr = fmt.Errorf("read agent tokens: %w", err)
		return r, errors.Join(r.writeErr, bindingsErr)
	}
	var file tokensFile
	if err := json.Unmarshal(raw, &file); err != nil {
		cause := fmt.Errorf("read agent tokens: %w", err)
		if moveErr := os.Rename(path, path+".bad"); moveErr != nil {
			r.writeErr = fmt.Errorf("%w; moving it aside failed: %w", cause, moveErr)
			cause = r.writeErr
		}
		return r, errors.Join(cause, bindingsErr)
	}
	for _, token := range file.Tokens {
		if token.ID == "" || token.TokenHash == "" {
			continue
		}
		r.tokens[token.ID] = token
		r.byHash[token.TokenHash] = token.ID
	}
	return r, bindingsErr
}

func (r *Registry) bindingsDir() string {
	return filepath.Join(filepath.Dir(r.path), "sessions")
}

func (r *Registry) bindingPath(sessionID string) string {
	return filepath.Join(r.bindingsDir(), hex.EncodeToString([]byte(sessionID)))
}

func (r *Registry) loadBindings() error {
	dir := r.bindingsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.sessionsLost = true
		return fmt.Errorf("read session grants: %w", err)
	}
	for _, entry := range entries {
		sessionID, err := hex.DecodeString(entry.Name())
		if err != nil || entry.IsDir() {
			continue
		}
		tokenID, _ := os.ReadFile(filepath.Join(dir, entry.Name())) // #nosec G304 -- inside the daemon's own data dir.
		r.sessions[string(sessionID)] = strings.TrimSpace(string(tokenID))
	}
	return nil
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

// Unreadable returns why the token file could not be read at start, or nil.
func (r *Registry) Unreadable() error {
	return r.writeErr
}

// Create registers a token and returns it with its secret, which is shown once.
func (r *Registry) Create(name string, grant Grant, now time.Time) (Token, string, error) {
	name = cleanName(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return Token{}, "", fmt.Errorf("a token name is required, up to %d characters", maxNameLength)
	}
	if strings.HasPrefix(strings.ToLower(name), "tok_") {
		return Token{}, "", errors.New("a token name cannot start with tok_, which marks a token id")
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
	for id, expired := range r.tokens {
		if !expired.Active(now) {
			delete(r.tokens, id)
			delete(r.byHash, expired.TokenHash)
		}
	}
	if len(r.tokens) >= maxTokens {
		return Token{}, "", ErrTooManyTokens
	}
	for _, existing := range r.tokens {
		if strings.EqualFold(existing.Name, name) {
			return Token{}, "", fmt.Errorf("a token named %q already exists; revoke it first", existing.Name)
		}
	}
	r.tokens[token.ID] = token
	r.byHash[token.TokenHash] = token.ID
	if err := r.persistLocked(); err != nil {
		delete(r.tokens, token.ID)
		delete(r.byHash, token.TokenHash)
		return Token{}, "", fmt.Errorf("%w: %w", ErrNotSaved, err)
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

// Get returns a token by ID, expired or not.
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

// Revoke removes a token, found by ID or by name.
func (r *Registry) Revoke(idOrName string) (Token, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, token := range r.tokens {
		if token.ID != idOrName && !strings.EqualFold(token.Name, idOrName) {
			continue
		}
		delete(r.tokens, token.ID)
		delete(r.byHash, token.TokenHash)
		if err := r.persistLocked(); err != nil {
			r.tokens[token.ID] = token
			r.byHash[token.TokenHash] = token.ID
			return Token{}, true, err
		}
		return token, true, nil
	}
	return Token{}, false, nil
}

// List returns the tokens, oldest first.
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

// SessionCreated binds a new session to its caller's token, or clears a leftover binding.
func (r *Registry) SessionCreated(ctx context.Context, sessionID string) error {
	if who := CallerFrom(ctx); who.Grant != nil {
		return r.Bind(sessionID, who.TokenID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, bound := r.sessions[sessionID]; !bound {
		if r.sessionsLost {
			r.unbound[sessionID] = true
			_ = os.Remove(r.bindingPath(sessionID))
		}
		return nil
	}
	if err := os.Remove(r.bindingPath(sessionID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(r.sessions, sessionID)
	return nil
}

// Bind records that a session started under a token.
func (r *Registry) Bind(sessionID, tokenID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = tokenID
	delete(r.unbound, sessionID)
	if err := os.MkdirAll(r.bindingsDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.bindingPath(sessionID), []byte(tokenID), 0o600)
}

// ForSession returns the ID of the token a session started under, which may be gone.
func (r *Registry) ForSession(sessionID string) (tokenID string, bound bool) {
	if r == nil || sessionID == "" {
		return "", false
	}
	r.mu.RLock()
	tokenID, bound = r.sessions[sessionID]
	lost := r.sessionsLost && !r.unbound[sessionID]
	r.mu.RUnlock()
	if bound || !lost {
		return tokenID, bound
	}
	raw, err := os.ReadFile(r.bindingPath(sessionID)) // #nosec G304 -- inside the daemon's own data dir.
	if errors.Is(err, fs.ErrNotExist) {
		r.mu.Lock()
		r.unbound[sessionID] = true
		r.mu.Unlock()
		return "", false
	}
	if err != nil {
		return "", true
	}
	tokenID = strings.TrimSpace(string(raw))
	r.mu.Lock()
	r.sessions[sessionID] = tokenID
	r.mu.Unlock()
	return tokenID, true
}

func (r *Registry) persistLocked() error {
	if r.writeErr != nil {
		return r.writeErr
	}
	file := tokensFile{Tokens: make([]Token, 0, len(r.tokens))}
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
