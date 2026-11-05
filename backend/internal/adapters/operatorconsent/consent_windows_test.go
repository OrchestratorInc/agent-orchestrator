//go:build windows

package operatorconsent

import (
	"context"
	"flag"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

var nativeConsentTest = flag.Bool("operator-native-consent", false, "explicitly run interactive Windows Hello verification")

// TestOperatorNativeConsentManual 仅显式选择时弹窗，不访问用户数据、不执行审批中的操作。
func TestOperatorNativeConsentManual(t *testing.T) {
	if !*nativeConsentTest {
		t.Skip("interactive OS verification requires -operator-native-consent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	payload := `{"idempotencyKey":"manual-validation-only","prompt":"人工验证：这不会执行任何工作流或调用 Provider"}`
	err := New().Verify(ctx, principal.Approval{
		ID: "manual-validation", Code: "MANUAL", Method: "POST", Path: "/api/v1/workflows/manual/attempts/manual/bind",
		Payload: payload, PayloadDigest: principal.PayloadDigest([]byte(payload)),
	})
	if err != nil {
		t.Fatalf("native consent did not verify: %v", err)
	}
}
