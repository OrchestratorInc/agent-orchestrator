package agentconnect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
)

type memoryStore struct {
	Store
	attempt                         Attempt
	committed                       bool
	connectedSecret, connectedNonce []byte
	commitErr                       error
	cleanup                         []Attempt
	retry                           bool
}

func (m *memoryStore) AgentLoginsNeedingCleanup(context.Context) ([]Attempt, error) {
	return m.cleanup, nil
}
func (m *memoryStore) FinishAgentLoginJob(_ context.Context, _, id, _ string) error {
	for i, a := range m.cleanup {
		if a.ID == id {
			m.cleanup = append(m.cleanup[:i], m.cleanup[i+1:]...)
			break
		}
	}
	return nil
}
func (m *memoryStore) RetryAgentLoginJob(context.Context, string, string, string) error {
	m.retry = true
	return nil
}

type observedCleanupRunner struct {
	runnerStub
	found bool
}

func (r *observedCleanupRunner) Cleanup(context.Context, string) (bool, error) { return r.found, nil }

func TestCleanupNeverDropsAnUnfinishedLaunchAfterOneAbsentLookup(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		finished, found, kept bool
	}{
		{"unknown create still absent", false, false, true},
		{"known completed launch absent", true, false, false},
		{"unknown create found and deleted", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, _, _ := testService(t)
			store.cleanup = []Attempt{{ID: "login", UserID: "user", JobID: "login", Status: "failed", LaunchFinished: tc.finished}}
			s.config.Runner = &observedCleanupRunner{found: tc.found}
			if err := s.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if kept := len(store.cleanup) > 0; kept != tc.kept {
				t.Fatalf("durable handle retained=%v want=%v", kept, tc.kept)
			}
			if tc.kept && !store.retry {
				t.Fatal("ambiguous cleanup missing retry backoff")
			}
		})
	}
}

func (m *memoryStore) CreateAgentLogin(_ context.Context, _ domain.Principal, a Attempt, _ int) (Attempt, bool, error) {
	m.attempt = a
	return a, true, nil
}

func (m *memoryStore) GetAgentLogin(_ context.Context, user, id string) (Attempt, error) {
	if m.attempt.UserID != user || m.attempt.ID != id {
		return Attempt{}, ErrNotFound
	}
	return m.attempt, nil
}
func (m *memoryStore) SetAgentLoginJob(_ context.Context, _, _, job string) (bool, error) {
	m.attempt.JobID = job
	m.attempt.LaunchFinished = true
	return true, nil
}

func (m *memoryStore) CancelAgentLogin(ctx context.Context, user, id string) (Attempt, error) {
	if _, err := m.GetAgentLogin(ctx, user, id); err != nil {
		return Attempt{}, err
	}
	m.attempt.Status = "cancelled"
	return m.attempt, nil
}
func (m *memoryStore) FindAgentLoginRunner(_ context.Context, id string, hash []byte) (Attempt, error) {
	if id != m.attempt.ID || !bytes.Equal(hash, m.attempt.TokenHash) {
		return Attempt{}, ErrNotFound
	}
	return m.attempt, nil
}
func (m *memoryStore) UpdateAgentLogin(_ context.Context, _ Attempt, update Update, encrypted, nonce []byte) error {
	if m.commitErr != nil {
		return m.commitErr
	}
	if !Active(m.attempt.Status) || !m.attempt.ExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	m.attempt.Status = update.Status
	m.committed = true
	m.connectedSecret, m.connectedNonce = encrypted, nonce
	return nil
}
func (m *memoryStore) SetAgentLoginCode(_ context.Context, _, _ string, encrypted, nonce []byte) (Attempt, error) {
	m.attempt.InputCiphertext, m.attempt.InputNonce = encrypted, nonce
	return m.attempt, nil
}
func (m *memoryStore) ConsumeAgentLoginCode(_ context.Context, _, _ string, _ []byte) (Attempt, error) {
	a := m.attempt
	m.attempt.InputCiphertext, m.attempt.InputNonce = nil, nil
	return a, nil
}

type runnerStub struct {
	launch   Launch
	stopped  string
	startErr error
}

func (r *runnerStub) Start(_ context.Context, l Launch) (string, error) {
	r.launch = l
	return "isolated-job", r.startErr
}
func (r *runnerStub) Stop(_ context.Context, id string) error { r.stopped = id; return nil }

type validatorFunc func(context.Context, string, string, []byte) error

func (v validatorFunc) Validate(ctx context.Context, h, c string, s []byte) error {
	return v(ctx, h, c, s)
}

func testService(t *testing.T) (*Service, *memoryStore, *runnerStub, *secrets.Cipher) {
	t.Helper()
	cipher, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{}
	runner := &runnerStub{}
	return New(Config{Store: store, Cipher: cipher, Runner: runner}), store, runner, cipher
}

func TestStartKeepsRunnerBearerOutOfPublicLoginAndStoresOnlyHash(t *testing.T) {
	s, store, runner, _ := testService(t)
	login, err := s.Start(context.Background(), domain.Principal{UserID: "a9fb8297-822e-4e5d-b7f9-cf217e44eedc"}, "bdb9cd01-302d-4350-8063-e7852cfb9b6c", "codex", "58519b65-a474-4981-b05e-88806115942b")
	if err != nil {
		t.Fatal(err)
	}
	if runner.launch.Token == "" || runner.launch.Harness != "codex" || runner.launch.ID != login.ID {
		t.Fatal("runner did not receive scoped launch")
	}
	hash := sha256.Sum256([]byte(runner.launch.Token))
	if !bytes.Equal(store.attempt.TokenHash, hash[:]) {
		t.Fatal("attempt does not store token digest")
	}
	if d := login.ExpiresAt.Sub(time.Now()); d < 9*time.Minute || d > 10*time.Minute {
		t.Fatalf("expiry = %v", d)
	}
	data, _ := json.Marshal(login)
	if strings.Contains(string(data), runner.launch.Token) || strings.Contains(string(data), "Token") || strings.Contains(string(data), "job") {
		t.Fatalf("public login exposes private launch: %s", data)
	}
}

func TestCompletionRechecksCancellationAfterCredentialValidation(t *testing.T) {
	s, store, _, _ := testService(t)
	store.attempt = Attempt{ID: "login", UserID: "user", Harness: "codex", Status: "waiting", ExpiresAt: time.Now().Add(time.Minute)}
	hash := sha256.Sum256([]byte("runner-token"))
	store.attempt.TokenHash = hash[:]
	validator := validatorFunc(func(context.Context, string, string, []byte) error { store.attempt.Status = "cancelled"; return nil })
	err := s.Report(context.Background(), "login", "runner-token", Update{Status: "connected", CredentialType: "auth_json", Secret: `{"tokens":{"access_token":"access","refresh_token":"refresh"}}`}, validator)
	if !errors.Is(err, ErrConflict) || store.committed || len(store.connectedSecret) != 0 {
		t.Fatalf("late completion committed=%v err=%v", store.committed, err)
	}
}

func TestCompletionEncryptsCredentialInExistingPersonalScope(t *testing.T) {
	s, store, _, cipher := testService(t)
	hash := sha256.Sum256([]byte("runner-token"))
	store.attempt = Attempt{ID: "login", UserID: "user", Harness: "codex", Status: "connecting", TokenHash: hash[:], ExpiresAt: time.Now().Add(time.Minute)}
	secret := `{"tokens":{"access_token":"access","refresh_token":"refresh"}}`
	var validated string
	err := s.Report(context.Background(), "login", "runner-token", Update{Status: "connected", CredentialType: "auth_json", Secret: secret}, validatorFunc(func(_ context.Context, h, c string, b []byte) error { validated = h + "/" + c; return nil }))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(store.connectedSecret, store.connectedNonce, secrets.ProviderConnectionAssociatedData("user:user", "codex", "default"))
	if err != nil || string(plain) != secret || validated != "codex/auth_json" {
		t.Fatalf("personal credential promotion invalid: validated=%s err=%v", validated, err)
	}
}

func TestRunnerCannotUseAnotherAttemptOrUserBearer(t *testing.T) {
	s, store, _, _ := testService(t)
	hash := sha256.Sum256([]byte("runner-token"))
	store.attempt = Attempt{ID: "attempt", TokenHash: hash[:]}
	for _, tc := range []struct{ id, token string }{{"other-attempt", "runner-token"}, {"attempt", "user-token"}} {
		if _, err := s.PollRunner(context.Background(), tc.id, tc.token); !errors.Is(err, ErrNotFound) {
			t.Fatalf("wrong runner access err=%v", err)
		}
	}
}

func TestAuthorizationURLRejectsCredentialBearingOrUntrustedDestinations(t *testing.T) {
	for _, tc := range []struct {
		h, url string
		want   bool
	}{
		{"codex", "https://auth.openai.com/codex/device", true},
		{"codex", "https://auth.openai.com/codex/device?access_token=secret", false},
		{"codex", "https://auth.openai.com.attacker.test/codex/device", false},
		{"codex", "http://auth.openai.com/codex/device", false},
		{"codex", "https://secret@auth.openai.com/codex/device", false},
		{"codex", "https://auth.openai.com/codex/device#secret", false},
		{"claude-code", "https://claude.com/cai/oauth/authorize?code=true&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&state=nonce", true},
		{"claude-code", "https://claude.ai/oauth/authorize?code=true&redirect_uri=http%3A%2F%2Flocalhost%3A8080%2Fcallback", false},
		{"claude-code", "https://claude.ai/oauth/authorize?code=secret", false},
		{"claude-code", "https://claude.ai/oauth/authorize?code=true&token=secret", false},
	} {
		if got := ValidateAuthorizationURL(tc.h, tc.url) == nil; got != tc.want {
			t.Errorf("URL %s accepted=%v want=%v", tc.url, got, tc.want)
		}
	}
}

func TestManualCodeIsEncryptedAndConsumedOnlyByTheMatchingAttempt(t *testing.T) {
	s, store, _, cipher := testService(t)
	hash := sha256.Sum256([]byte("runner-token"))
	store.attempt = Attempt{ID: "login", UserID: "user", Harness: "claude-code", Status: "waiting", RequiresCode: true, TokenHash: hash[:], ExpiresAt: time.Now().Add(time.Minute)}
	login, err := s.SubmitCode(context.Background(), "user", "login", "one-time-code#state")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(store.attempt.InputCiphertext, []byte("one-time-code")) {
		t.Fatal("code persisted as plaintext")
	}
	if _, err := cipher.Decrypt(store.attempt.InputCiphertext, store.attempt.InputNonce, "agent-login:user:another-login"); err == nil {
		t.Fatal("code can be decrypted for another attempt")
	}
	encoded, _ := json.Marshal(login)
	if strings.Contains(string(encoded), "one-time-code") {
		t.Fatal("input code leaked into public login")
	}
	first, err := s.PollRunner(context.Background(), "login", "runner-token")
	if err != nil || first.Code != "one-time-code#state" {
		t.Fatalf("first poll %+v err=%v", first, err)
	}
	second, err := s.PollRunner(context.Background(), "login", "runner-token")
	if err != nil || second.Code != "" {
		t.Fatalf("code replayed %+v err=%v", second, err)
	}
}

func TestRunnerPublicDeviceCodeCannotContainArbitraryOutput(t *testing.T) {
	s, store, _, _ := testService(t)
	hash := sha256.Sum256([]byte("runner-token"))
	store.attempt = Attempt{ID: "login", UserID: "user", Harness: "codex", Status: "waiting", TokenHash: hash[:], ExpiresAt: time.Now().Add(time.Minute)}
	for _, code := range []string{"secret-token", "ABCD-EFGH-IJKL", "abc123", "refresh token"} {
		if err := s.Report(context.Background(), "login", "runner-token", Update{Status: "waiting", UserCode: code}, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("unsafe userCode %q accepted: %v", code, err)
		}
	}
	if err := s.Report(context.Background(), "login", "runner-token", Update{Status: "waiting", UserCode: "ABCD-12345"}, nil); err != nil {
		t.Fatalf("stock device code rejected: %v", err)
	}
}

func TestProvisioningFailureReturnsOnlyPersistedSanitizedStatus(t *testing.T) {
	s, store, runner, _ := testService(t)
	runner.startErr = errors.New("provider error contains runner-token-secret")
	login, err := s.Start(context.Background(), domain.Principal{UserID: "a9fb8297-822e-4e5d-b7f9-cf217e44eedc"}, "bdb9cd01-302d-4350-8063-e7852cfb9b6c", "codex", "58519b65-a474-4981-b05e-88806115942b")
	if err != nil || login.Status != "failed" || store.attempt.Status != "failed" {
		t.Fatalf("failed launch: %+v err=%v", login, err)
	}
	data, _ := json.Marshal(login)
	if strings.Contains(string(data), "runner-token-secret") {
		t.Fatal("provider error leaked")
	}
}

func TestDisablingNewLoginsPreservesCancellationAndCleanup(t *testing.T) {
	s, store, runner, _ := testService(t)
	p := domain.Principal{UserID: "a9fb8297-822e-4e5d-b7f9-cf217e44eedc"}
	login, err := s.Start(context.Background(), p, "bdb9cd01-302d-4350-8063-e7852cfb9b6c", "codex", "58519b65-a474-4981-b05e-88806115942b")
	if err != nil {
		t.Fatal(err)
	}
	s.config.DisableNewLogins = true
	if len(s.Capabilities()) != 0 {
		t.Fatal("disabled feature still advertises new logins")
	}
	if _, err := s.Start(context.Background(), p, "bdb9cd01-302d-4350-8063-e7852cfb9b6c", "codex", "93daf24c-68c0-443c-b0c2-1abbc6d1b0cf"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled start: %v", err)
	}
	if _, err := s.SubmitCode(context.Background(), p.UserID, login.ID, "code"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled input: %v", err)
	}
	if err := s.Report(context.Background(), login.ID, runner.launch.Token, Update{Status: "connected"}, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled completion: %v", err)
	}
	if err := s.Cancel(context.Background(), p.UserID, login.ID); err != nil {
		t.Fatalf("disabled cancellation: %v", err)
	}
	if got, err := s.Get(context.Background(), p.UserID, login.ID); err != nil || got.Status != "cancelled" {
		t.Fatalf("disabled read: %+v err=%v", got, err)
	}
	store.cleanup = []Attempt{store.attempt}
	if err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.stopped != "isolated-job" || len(store.cleanup) != 0 {
		t.Fatalf("disabled feature stranded cleanup: stopped=%s remaining=%d", runner.stopped, len(store.cleanup))
	}
}
