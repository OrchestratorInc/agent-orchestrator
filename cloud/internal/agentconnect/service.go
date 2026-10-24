// Package agentconnect coordinates short-lived native coding-agent login jobs.
// Jobs carry an attempt bearer and have no authority over projects or workers.
package agentconnect

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/google/uuid"
)

var (
	ErrUnavailable         = errors.New("native agent login is unavailable")
	ErrInvalid             = errors.New("invalid agent login request")
	ErrNotFound            = errors.New("agent login not found")
	ErrConflict            = errors.New("agent login is no longer active")
	ErrInvalidCredential   = errors.New("agent login credential was rejected")
	ErrProviderUnavailable = errors.New("agent credential provider is unavailable")
)

const Lifetime = 10 * time.Minute

var deviceCodePattern = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4,6}$`)

type Login struct {
	ID               string    `json:"id"`
	Harness          string    `json:"harness"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expiresAt"`
	AuthorizationURL string    `json:"authorizationUrl,omitempty"`
	UserCode         string    `json:"userCode,omitempty"`
	RequiresCode     bool      `json:"requiresCode,omitempty"`
	Message          string    `json:"message,omitempty"`
}

// Attempt is a private persistence record. Never serialize it at an HTTP edge.
type Attempt struct {
	ID, UserID, OrgID, Harness, Status, IdempotencyKey string
	ExpiresAt                                          time.Time
	AuthorizationURL, UserCode                         string
	RequiresCode                                       bool
	TokenHash, InputCiphertext, InputNonce             []byte
	JobID                                              string
	LaunchFinished                                     bool
}

type Update struct {
	Status           string `json:"status"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	UserCode         string `json:"userCode,omitempty"`
	RequiresCode     bool   `json:"requiresCode,omitempty"`
	CredentialType   string `json:"credentialType,omitempty"`
	Secret           string `json:"secret,omitempty"`
}

type RunnerState struct {
	Harness   string    `json:"harness"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expiresAt"`
	Code      string    `json:"code,omitempty"`
}

type Launch struct {
	ID, OrgID, Harness, Token string
	ExpiresAt                 time.Time
}

type Runner interface {
	Start(context.Context, Launch) (string, error)
	Stop(context.Context, string) error
}

// CleanupRunner can distinguish a confirmed deletion from an absent or not-yet
// visible VM. An unfinished provision must retain its handle on absence.
type CleanupRunner interface {
	Cleanup(context.Context, string) (found bool, err error)
}

type Validator interface {
	Validate(context.Context, string, string, []byte) error
}

type Store interface {
	CreateAgentLogin(context.Context, domain.Principal, Attempt, int) (Attempt, bool, error)
	GetAgentLogin(context.Context, string, string) (Attempt, error)
	FindAgentLoginRunner(context.Context, string, []byte) (Attempt, error)
	ConsumeAgentLoginCode(context.Context, string, string, []byte) (Attempt, error)
	SetAgentLoginCode(context.Context, string, string, []byte, []byte) (Attempt, error)
	CancelAgentLogin(context.Context, string, string) (Attempt, error)
	UpdateAgentLogin(context.Context, Attempt, Update, []byte, []byte) error
	SetAgentLoginJob(context.Context, string, string, string) (bool, error)
	FinishAgentLoginJob(context.Context, string, string, string) error
	RetryAgentLoginJob(context.Context, string, string, string) error
	AgentLoginsNeedingCleanup(context.Context) ([]Attempt, error)
}

type Config struct {
	Store        Store
	Cipher       *secrets.Cipher
	Runner       Runner
	MaxSandboxes int
	// DisableNewLogins preserves existing-attempt reads, cancellation and
	// recovery while disabling new attempts, input and credential promotion.
	DisableNewLogins bool
}

type Service struct{ config Config }

func New(config Config) *Service {
	if config.MaxSandboxes <= 0 {
		config.MaxSandboxes = 1000
	}
	return &Service{config: config}
}

func (s *Service) Capabilities() []string {
	if s == nil || s.config.DisableNewLogins || s.config.Runner == nil || s.config.Store == nil || s.config.Cipher == nil {
		return []string{}
	}
	return []string{"claude-code", "codex"}
}

func Active(status string) bool {
	return status == "starting" || status == "waiting" || status == "connecting"
}

func view(a Attempt) Login {
	l := Login{ID: a.ID, Harness: a.Harness, Status: a.Status, ExpiresAt: a.ExpiresAt}
	if a.Status == "waiting" || a.Status == "connecting" {
		l.AuthorizationURL = a.AuthorizationURL
		l.UserCode = a.UserCode
		l.RequiresCode = a.RequiresCode
	}
	switch a.Status {
	case "failed":
		l.Message = "The agent could not connect. Start a new login to try again."
	case "expired":
		l.Message = "This login expired. Start a new login to try again."
	case "cancelled":
		l.Message = "This login was cancelled."
	}
	return l
}

func (s *Service) Start(ctx context.Context, p domain.Principal, orgID, harness, key string) (Login, error) {
	if len(s.Capabilities()) == 0 {
		return Login{}, ErrUnavailable
	}
	if (harness != "claude-code" && harness != "codex") || p.UserID == "" {
		return Login{}, ErrInvalid
	}
	if _, err := uuid.Parse(orgID); err != nil {
		return Login{}, ErrInvalid
	}
	if _, err := uuid.Parse(key); err != nil {
		return Login{}, ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Login{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	hash := sha256.Sum256([]byte(token))
	id := uuid.NewString()
	a, created, err := s.config.Store.CreateAgentLogin(ctx, p, Attempt{ID: id, UserID: p.UserID, OrgID: orgID, Harness: harness, Status: "starting", IdempotencyKey: key, ExpiresAt: time.Now().UTC().Add(Lifetime), TokenHash: hash[:], JobID: id}, s.config.MaxSandboxes)
	if err != nil {
		return Login{}, err
	}
	if !created {
		return view(a), nil
	}
	// The durable attempt is the one-shot launch lease. An HTTP disconnect does
	// not orphan provisioning, and retries never provision this attempt twice.
	launchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	jobID, launchErr := s.config.Runner.Start(launchCtx, Launch{ID: a.ID, OrgID: a.OrgID, Harness: a.Harness, Token: token, ExpiresAt: a.ExpiresAt})
	if launchErr != nil {
		failureCtx, failureCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer failureCancel()
		// A failed Create can still produce a VM after its request timed out.
		// Retain the unfinished reservation until expiry so a premature cleanup
		// lookup cannot clear the handle before that VM appears.
		_ = s.config.Store.UpdateAgentLogin(failureCtx, a, Update{Status: "failed"}, nil, nil)
		return s.Get(failureCtx, a.UserID, a.ID)
	}
	active, err := s.config.Store.SetAgentLoginJob(launchCtx, a.UserID, a.ID, jobID)
	if err != nil || !active {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cleanupCancel()
		_ = s.config.Runner.Stop(cleanupCtx, jobID)
		if err != nil {
			return Login{}, err
		}
		return s.Get(ctx, p.UserID, a.ID)
	}
	return view(a), nil
}

func (s *Service) Get(ctx context.Context, userID, id string) (Login, error) {
	if s == nil || s.config.Store == nil {
		return Login{}, ErrUnavailable
	}
	a, err := s.config.Store.GetAgentLogin(ctx, userID, id)
	return view(a), err
}

func inputAAD(a Attempt) string { return "agent-login:" + a.UserID + ":" + a.ID }

func (s *Service) SubmitCode(ctx context.Context, userID, id, code string) (Login, error) {
	if len(s.Capabilities()) == 0 {
		return Login{}, ErrUnavailable
	}
	code = strings.TrimSpace(code)
	if len(code) == 0 || len(code) > 4096 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return Login{}, ErrInvalid
	}
	a, err := s.config.Store.GetAgentLogin(ctx, userID, id)
	if err != nil {
		return Login{}, err
	}
	if !Active(a.Status) || !a.RequiresCode {
		return Login{}, ErrConflict
	}
	plaintext := []byte(code)
	defer clear(plaintext)
	encrypted, nonce, err := s.config.Cipher.Encrypt(plaintext, inputAAD(a))
	if err != nil {
		return Login{}, err
	}
	a, err = s.config.Store.SetAgentLoginCode(ctx, userID, id, encrypted, nonce)
	return view(a), err
}

func (s *Service) Cancel(ctx context.Context, userID, id string) error {
	if s == nil || s.config.Store == nil {
		return ErrUnavailable
	}
	_, err := s.config.Store.CancelAgentLogin(ctx, userID, id)
	return err
}

func (s *Service) PollRunner(ctx context.Context, id, token string) (RunnerState, error) {
	if s == nil || s.config.Store == nil || s.config.Cipher == nil || len(token) < 1 || len(token) > 256 {
		return RunnerState{}, ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	a, err := s.config.Store.FindAgentLoginRunner(ctx, id, hash[:])
	if err != nil {
		return RunnerState{}, err
	}
	a, err = s.config.Store.ConsumeAgentLoginCode(ctx, a.UserID, id, hash[:])
	if err != nil {
		return RunnerState{}, err
	}
	state := RunnerState{Harness: a.Harness, Status: a.Status, ExpiresAt: a.ExpiresAt}
	if len(a.InputCiphertext) > 0 && Active(a.Status) {
		code, err := s.config.Cipher.Decrypt(a.InputCiphertext, a.InputNonce, inputAAD(a))
		if err != nil {
			return RunnerState{}, err
		}
		defer clear(code)
		state.Code = string(code)
	}
	return state, nil
}

// Report validates outside the database transaction. UpdateAgentLogin must
// check the attempt and expiry again while atomically promoting the credential.
func (s *Service) Report(ctx context.Context, id, token string, u Update, validator Validator) error {
	if len(s.Capabilities()) == 0 {
		return ErrUnavailable
	}
	if len(token) < 1 || len(token) > 256 {
		return ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	a, err := s.config.Store.FindAgentLoginRunner(ctx, id, hash[:])
	if err != nil {
		return err
	}
	if !Active(a.Status) || !a.ExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	if u.Status != "waiting" && u.Status != "connecting" && u.Status != "connected" && u.Status != "failed" {
		return ErrInvalid
	}
	if u.AuthorizationURL != "" && ValidateAuthorizationURL(a.Harness, u.AuthorizationURL) != nil {
		return ErrInvalid
	}
	if u.UserCode != "" && (a.Harness != "codex" || !deviceCodePattern.MatchString(u.UserCode)) {
		return ErrInvalid
	}
	if u.RequiresCode && a.Harness != "claude-code" {
		return ErrInvalid
	}
	if u.Status != "connected" {
		if u.Secret != "" || u.CredentialType != "" {
			return ErrInvalid
		}
		return s.config.Store.UpdateAgentLogin(ctx, a, u, nil, nil)
	}
	if validator == nil || len(u.Secret) == 0 || len(u.Secret) > 64<<10 {
		return ErrInvalid
	}
	if a.Harness == "claude-code" && u.CredentialType != "oauth_token" || a.Harness == "codex" && u.CredentialType != "auth_json" {
		return ErrInvalid
	}
	if a.Harness == "codex" {
		var credential struct {
			Tokens struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"tokens"`
		}
		if json.Unmarshal([]byte(u.Secret), &credential) != nil || credential.Tokens.AccessToken == "" || credential.Tokens.RefreshToken == "" {
			return ErrInvalid
		}
	}
	secret := []byte(u.Secret)
	defer clear(secret)
	u.Secret = ""
	if err := validator.Validate(ctx, a.Harness, u.CredentialType, secret); err != nil {
		if errors.Is(err, ErrInvalidCredential) {
			return ErrInvalidCredential
		}
		return ErrProviderUnavailable
	}
	encrypted, nonce, err := s.config.Cipher.Encrypt(secret, secrets.ProviderConnectionAssociatedData("user:"+a.UserID, a.Harness, "default"))
	if err != nil {
		return err
	}
	return s.config.Store.UpdateAgentLogin(ctx, a, u, encrypted, nonce)
}

func ValidateAuthorizationURL(harness, raw string) error {
	if len(raw) > 8192 {
		return ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" || u.RawPath != "" {
		return ErrInvalid
	}
	if harness == "codex" {
		if u.Host != "auth.openai.com" || u.Path != "/codex/device" || u.RawQuery != "" {
			return ErrInvalid
		}
		return nil
	}
	if harness != "claude-code" {
		return ErrInvalid
	}
	if !(u.Host == "claude.com" && u.Path == "/cai/oauth/authorize" || (u.Host == "claude.ai" || u.Host == "console.anthropic.com") && u.Path == "/oauth/authorize") {
		return ErrInvalid
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || q.Get("code") != "true" {
		return ErrInvalid
	}
	redirect := q.Get("redirect_uri")
	if redirect != "https://platform.claude.com/oauth/code/callback" && redirect != "https://console.anthropic.com/oauth/code/callback" {
		return ErrInvalid
	}
	for key, values := range q {
		if len(values) != 1 {
			return ErrInvalid
		}
		switch key {
		case "client_id", "code", "code_challenge", "code_challenge_method", "redirect_uri", "response_type", "scope", "state":
		default:
			return ErrInvalid
		}
	}
	return nil
}

// Sweep recovers cleanup after restarts, cancellations and expired attempts.
// Stop must be idempotent; failures retain the durable handle for the next pass.
func (s *Service) Sweep(ctx context.Context) error {
	if s == nil || s.config.Store == nil || s.config.Runner == nil {
		return nil
	}
	attempts, err := s.config.Store.AgentLoginsNeedingCleanup(ctx)
	if err != nil {
		return err
	}
	for _, a := range attempts {
		stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		found := false
		var err error
		if cleaner, ok := s.config.Runner.(CleanupRunner); ok {
			found, err = cleaner.Cleanup(stopCtx, a.JobID)
		} else {
			err = s.config.Runner.Stop(stopCtx, a.JobID)
		}
		cancel()
		if err != nil || (!a.LaunchFinished && !found) {
			// A provider can accept Create despite its caller timing out. One
			// absent lookup never proves that unfinished launch cannot appear.
			if err := s.config.Store.RetryAgentLoginJob(ctx, a.UserID, a.ID, a.JobID); err != nil {
				return err
			}
			continue
		}
		if err := s.config.Store.FinishAgentLoginJob(ctx, a.UserID, a.ID, a.JobID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Run(ctx context.Context) {
	_ = s.Sweep(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Sweep(ctx)
		}
	}
}
