package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

type operatorConsentAccepted struct {
	calls []principal.Approval
}

func (c *operatorConsentAccepted) Verify(_ context.Context, approval principal.Approval) error {
	c.calls = append(c.calls, approval)
	return nil
}

// TestM2OperatorAuthWiring 证明正式装配不读写 token 文件，且重启使 OS 确认过的审批失效。
//
// 同用户进程可以读任意文件——所以这里断言的不是"文件读不到"，而是**没有可读的操作员凭证文件**：
// <dataDir>/operator.token 在一次完整审批（创建 → OS 确认 → 消费）前后都必须是 ENOENT。
// 真正决定授权的是 OS 验证结果，而不是任何可被同用户进程复制的值。
func TestM2OperatorAuthWiring(t *testing.T) {
	t.Parallel()
	store := openDaemonTestStore(t)
	dataDir := t.TempDir()
	tokenPath := filepath.Join(dataDir, principal.OperatorTokenFileName)
	// 同用户进程完全可以在 dataDir 里放一个旧形状的 token 文件；装配必须既不读它也不删它。
	legacy := strings.Repeat("a", 64)
	if err := os.WriteFile(tokenPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	first := newWorkflowOperatorAuth(dataDir, store, nil, log)
	if first.Principals == nil || first.Consent == nil || first.Approvals == nil {
		t.Fatal("missing native operator wiring")
	}

	// 1. A legacy token authorises NOTHING. It is not read, so it cannot become a caller.
	path := "/api/v1/workflows/w1/attempts/a1/bind"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set(principal.HeaderOperatorToken, legacy)
	if _, err := first.Principals.Resolve(req.Context(), principal.RequestFacts{Request: req}); err == nil {
		t.Fatal("legacy token bypassed native consent")
	}
	// Nor does a loopback peer with no proof at all.
	req.Header.Del(principal.HeaderOperatorToken)
	if _, err := first.Principals.Resolve(req.Context(), principal.RequestFacts{Request: req}); err == nil {
		t.Fatal("loopback alone proved operator identity")
	}

	// 2. The wiring neither read nor removed the planted file.
	raw, err := os.ReadFile(tokenPath)
	if err != nil || string(raw) != legacy {
		t.Fatalf("daemon touched the legacy token file: data=%q err=%v", raw, err)
	}
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}

	// 3. A full cycle through the OS boundary authorises exactly one operation...
	digest := principal.PayloadDigest(nil)
	approval, err := first.Approvals.CreateWithPayload(http.MethodPost, path, digest, "")
	if err != nil {
		t.Fatal(err)
	}
	consent := &operatorConsentAccepted{}
	confirmed, err := first.Approvals.VerifyAndConfirm(
		context.Background(), approval.ID, approval.Code, digest, consent)
	if err != nil {
		t.Fatalf("native consent: %v", err)
	}
	if len(consent.calls) != 1 || consent.calls[0].ID != approval.ID ||
		consent.calls[0].Path != path || consent.calls[0].PayloadDigest != digest {
		t.Fatalf("the OS verifier was asked about %+v, want the approved operation", consent.calls)
	}
	// The audit subject names the OS gesture and is not a credential.
	if confirmed.ConfirmedByFingerprint != principal.NativeConsentIdentity {
		t.Fatalf("confirming identity = %q, want %q",
			confirmed.ConfirmedByFingerprint, principal.NativeConsentIdentity)
	}
	if _, err := first.Approvals.Consume(approval.ID, http.MethodPost, path, digest); err != nil {
		t.Fatalf("consume the confirmed approval: %v", err)
	}

	// 4. ...and the cycle minted no operator credential file at all.
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("%s exists after a full approval cycle: err=%v", principal.OperatorTokenFileName, err)
	}

	// 5. A restart builds a new approval table: the confirmed approval is gone.
	second := newWorkflowOperatorAuth(dataDir, store, nil, log)
	if _, err := second.Approvals.Consume(approval.ID, http.MethodPost, path, digest); err == nil {
		t.Fatal("confirmed approval survived daemon restart")
	} else if refusal, ok := principal.AsApprovalRefusal(err); !ok || refusal.Reason != principal.ReasonApprovalUnknown {
		t.Fatalf("restart refusal: %v", err)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("%s exists after a restart: err=%v", principal.OperatorTokenFileName, err)
	}

	// 6. A virgin data dir stays empty: the daemon mints nothing.
	cleanDir := t.TempDir()
	clean := newWorkflowOperatorAuth(cleanDir, store, nil, log)
	files, err := os.ReadDir(cleanDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("daemon minted credential files: %v %v", files, err)
	}
	logWorkflowOperatorAuth(log, clean)
	if strings.Contains(logs.String(), legacy) || strings.Contains(logs.String(), principal.OperatorTokenFingerprint(legacy)) {
		t.Fatal("auth log exposed token material")
	}
}
