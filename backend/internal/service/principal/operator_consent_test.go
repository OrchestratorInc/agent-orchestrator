package principal_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

type consentCheck func(context.Context, principal.Approval) error

func (f consentCheck) Verify(ctx context.Context, approval principal.Approval) error {
	return f(ctx, approval)
}

// TestOperatorConsentRechecksAfterNativeVerification 覆盖 OS 验证失败和弹窗期间失效，删去任何后置门禁会变红。
func TestOperatorConsentRechecksAfterNativeVerification(t *testing.T) {
	for _, scenario := range []string{"denied", "unavailable", "cancelled", "expired", "reset", "verified"} {
		t.Run(scenario, func(t *testing.T) {
			f := newApprovalFixture(t)
			payload := `{"prompt":"逐字核对正文","idempotencyKey":"consent"}`
			digest := principal.PayloadDigest([]byte(payload))
			approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, payload)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			verifier := consentCheck(func(_ context.Context, displayed principal.Approval) error {
				if displayed.Payload != payload || displayed.Path != approvalBindPath || displayed.PayloadDigest != digest {
					t.Fatalf("OS saw a different operation: %+v", displayed)
				}
				switch scenario {
				case "denied":
					return principal.ErrConsentDenied
				case "unavailable":
					return principal.ErrConsentUnavailable
				case "cancelled":
					cancel()
				case "expired":
					f.tick(11 * time.Second)
				case "reset":
					f.store.ResetApprovals()
				}
				return nil
			})
			_, err = f.store.VerifyAndConfirm(ctx, approval.ID, approval.Code, digest, verifier)
			if scenario != "verified" {
				if err == nil {
					t.Fatal("failed or stale OS verification confirmed approval")
				}
				if _, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest); err == nil {
					t.Fatal("failed native verification authorized an operation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			identity, err := f.store.Consume(approval.ID, http.MethodPost, approvalBindPath, digest)
			if err != nil || identity != principal.NativeConsentIdentity {
				t.Fatalf("audit identity=%q err=%v", identity, err)
			}
		})
	}
}

// TestOperatorConsentRejectsConcurrentPrompts 证明原生调用不持有条目锁，且另一次确认不能启动并发弹窗。
func TestOperatorConsentRejectsConcurrentPrompts(t *testing.T) {
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	approval, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	verifier := consentCheck(func(ctx context.Context, _ principal.Approval) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	go func() {
		_, err := f.store.VerifyAndConfirm(ctx, approval.ID, approval.Code, digest, verifier)
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("native verification never started")
	}
	if _, found := f.store.Get(approval.ID); !found {
		t.Fatal("approval unreadable during OS prompt")
	}
	_, err = f.store.VerifyAndConfirm(ctx, approval.ID, approval.Code, digest, verifier)
	if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != "consent_busy" {
		t.Fatalf("concurrent confirmation=%v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

// TestOperatorConsentChecksBindingBeforePrompt 证明伪造正文或配对码不会触发 OS，旧 digest-only fixture 也不能越过生产确认。
func TestOperatorConsentChecksBindingBeforePrompt(t *testing.T) {
	f := newApprovalFixture(t)
	digest := principal.PayloadDigest(nil)
	if _, err := f.store.CreateWithPayload(http.MethodPost, approvalBindPath, digest, "{}"); err == nil {
		t.Fatal("digest did not bind exact payload")
	}
	approval, err := f.store.Create(http.MethodPost, approvalBindPath, digest)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	verifier := consentCheck(func(context.Context, principal.Approval) error { called = true; return errors.New("unexpected call") })
	if _, err := f.store.VerifyAndConfirm(context.Background(), approval.ID, "wrong-code", digest, verifier); err == nil {
		t.Fatal("wrong pairing code accepted")
	}
	if _, err := f.store.VerifyAndConfirm(context.Background(), approval.ID, approval.Code, digest, verifier); err == nil {
		t.Fatal("legacy hash-only approval reached native verification")
	}
	if called {
		t.Fatal("invalid approval opened an OS prompt")
	}
}
