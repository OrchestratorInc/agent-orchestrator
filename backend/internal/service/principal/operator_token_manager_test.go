package principal_test

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Token adoption: the secret lives on disk only until it is claimed.
//
// The hole these tests close, stated the way the acceptance criterion states it: because a
// same-user process can read <dataDir>/operator.token, and because approval CREATION is
// unauthenticated on loopback, an agent shell could create an approval, read the token, present it
// to the confirm route, and consume the approval it just created. The human gesture would then be
// decorative. Adoption removes the file, so the steady state has no credential to steal.

// startTokenFile mints a token in a fresh data dir and returns it with its path.
func startTokenFile(t *testing.T) (dataDir, token, path string) {
	t.Helper()
	dataDir = t.TempDir()
	// Mint the way the daemon does, then put the file back: Boot deletes it, and these tests need
	// to start from the on-disk state adoption is supposed to remove.
	token, err := principal.LoadOrCreateOperatorToken(dataDir)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return dataDir, token, filepath.Join(dataDir, principal.OperatorTokenFileName)
}

// TestOperatorTokenAdoptionRemovesTheFile is the core security property: after adoption a second
// same-user process gets ENOENT, while the in-memory trust still verifies the token.
func TestOperatorTokenAdoptionRemovesTheFile(t *testing.T) {
	t.Parallel()
	dataDir, token, path := startTokenFile(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the token file must exist before adoption: %v", err)
	}

	manager := principal.NewTokenManager(dataDir)
	fingerprint, err := manager.Boot()
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if fingerprint != principal.OperatorTokenFingerprint(token) {
		t.Fatalf("fingerprint = %q, want the minted token's", fingerprint)
	}
	// The file is GONE, and that is the whole point.
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the token file still exists after adoption: %v", err)
	}

	// The in-memory trust still verifies the token the desktop app holds.
	trust := principal.NewManagerTokenTrust(manager)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/w1/attempts/a1/bind", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set(principal.HeaderOperatorToken, token)
	if !trust.PermitsOperator(req) {
		t.Fatal("the in-memory trust no longer verifies the adopted token")
	}
	// And a wrong token is still refused, so the check did not degrade into "always true".
	req.Header.Set(principal.HeaderOperatorToken, strings.Repeat("f", 64))
	if trust.PermitsOperator(req) {
		t.Fatal("the in-memory trust accepted a wrong token")
	}
}

// TestOperatorTokenAdoptionLeavesNothingForASecondSameUserProcess is the demonstrable property:
// the same re-exec trick that used to succeed now gets ENOENT.
func TestOperatorTokenAdoptionLeavesNothingForASecondSameUserProcess(t *testing.T) {
	if os.Getenv("AO_OPERATOR_TOKEN_PROBE") == "1" {
		// Child half: report what the file system says, without failing the child.
		path := os.Getenv("AO_OPERATOR_TOKEN_PATH")
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			os.Stdout.WriteString("child-enoent")
			return
		}
		if err != nil {
			os.Stdout.WriteString("child-error:" + err.Error())
			return
		}
		os.Stdout.WriteString("child-read:" + strings.TrimSpace(string(raw)))
		return
	}
	t.Parallel()
	dataDir, _, path := startTokenFile(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	probe := func() string {
		cmd := exec.Command(exe, "-test.run=^TestOperatorTokenAdoptionLeavesNothingForASecondSameUserProcess$")
		cmd.Env = append(os.Environ(), "AO_OPERATOR_TOKEN_PROBE=1", "AO_OPERATOR_TOKEN_PATH="+path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("probe failed (%s): %v\n%s", cmd.String(), err, out)
		}
		return string(out)
	}

	// Before adoption the second process reads it — the residual risk, unchanged.
	if got := probe(); !strings.Contains(got, "child-read:") {
		t.Fatalf("before adoption the probe should read the secret; got %q", got)
	}
	manager := principal.NewTokenManager(dataDir)
	if _, err := manager.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	// After adoption it gets ENOENT, while the daemon still verifies.
	if got := probe(); !strings.Contains(got, "child-enoent") {
		t.Fatalf("after adoption the second process must find no secret; got %q", got)
	}
	if !manager.Adopted() || manager.Current() == "" {
		t.Fatal("the daemon lost the secret it adopted")
	}
	t.Logf("probe command: %s -test.run=^TestOperatorTokenAdoptionLeavesNothingForASecondSameUserProcess$ (AO_OPERATOR_TOKEN_PROBE=1 AO_OPERATOR_TOKEN_PATH=%s)", exe, path)
}

// TestOperatorTokenAdoptionIsSafeUnderConcurrency exercises the manager's lock: verification and
// adoption run together, which is the case -race exists to catch.
func TestOperatorTokenAdoptionIsSafeUnderConcurrency(t *testing.T) {
	t.Parallel()
	dataDir, token, _ := startTokenFile(t)
	manager := principal.NewTokenManager(dataDir)
	trust := principal.NewManagerTokenTrust(manager)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/w1/attempts/a1/bind", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set(principal.HeaderOperatorToken, token)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = trust.PermitsOperator(req)
				_ = manager.Adopted()
				_ = manager.Current()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := manager.Boot(); err != nil {
			t.Errorf("boot: %v", err)
		}
		if _, _, err := manager.Adopt(); err != nil {
			t.Errorf("adopt: %v", err)
		}
	}()
	wg.Wait()
	if !trust.PermitsOperator(req) {
		t.Fatal("the adopted token was not verifiable after a concurrent boot")
	}
}

// TestOperatorTokenAdoptionIsIdempotent proves a second adoption is a harmless no-op that reports
// removed=false and keeps the in-memory value.
func TestOperatorTokenAdoptionIsIdempotent(t *testing.T) {
	t.Parallel()
	dataDir, token, _ := startTokenFile(t)
	manager := principal.NewTokenManager(dataDir)
	if _, err := manager.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
	fingerprint, removed, err := manager.Adopt()
	if err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	if removed {
		t.Fatal("adopt claimed it removed a file that was already gone")
	}
	if fingerprint != principal.OperatorTokenFingerprint(token) {
		t.Fatalf("fingerprint = %q, want the in-memory token's", fingerprint)
	}
}

// TestOperatorTokenFileIsReplacedAfterARestart proves a stolen pre-restart token is worthless: a
// new boot mints a DIFFERENT secret, so the old value is refused.
func TestOperatorTokenFileIsReplacedAfterARestart(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	first := principal.NewTokenManager(dataDir)
	if _, err := first.Boot(); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	stolen := first.Current()

	// A restart is a new manager. The file is absent (the first boot removed it), so this boot
	// MINTS a new secret, which is what invalidates the stolen one.
	second := principal.NewTokenManager(dataDir)
	if _, err := second.Boot(); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if second.Current() == stolen {
		t.Fatalf("a restart reused the previous secret")
	}
	trust := principal.NewManagerTokenTrust(second)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/w1/attempts/a1/bind", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set(principal.HeaderOperatorToken, stolen)
	if trust.PermitsOperator(req) {
		t.Fatal("a token stolen before the restart was accepted after it")
	}
}

// TestOperatorTokenManagerFailsClosedWithNoSecret proves an empty data dir yields a manager that
// verifies nothing rather than one that accepts everything.
func TestOperatorTokenManagerFailsClosedWithNoSecret(t *testing.T) {
	t.Parallel()
	manager := principal.NewTokenManager(t.TempDir())
	// Boot failed in this scenario's production analogue; the manager holds nothing.
	trust := principal.NewManagerTokenTrust(manager)
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set(principal.HeaderOperatorToken, "")
	if trust.PermitsOperator(req) {
		t.Fatal("a manager with no secret permitted the operator")
	}
	if manager.Adopted() {
		t.Fatal("a manager that adopted nothing reported an adoption")
	}
}

// TestOperatorTokenAdoptionIsWellFormedOnly proves a hand-edited file is not adopted as a weaker
// credential.
func TestOperatorTokenAdoptionIsWellFormedOnly(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, principal.OperatorTokenFileName)
	if err := os.WriteFile(path, []byte(hex.EncodeToString([]byte("short"))), 0o600); err != nil {
		t.Fatalf("write bad token: %v", err)
	}
	manager := principal.NewTokenManager(dataDir)
	if _, _, err := manager.Adopt(); err == nil {
		t.Fatal("a malformed token file was adopted")
	}
	if manager.Adopted() {
		t.Fatal("a malformed token file produced an adopted value")
	}
}
