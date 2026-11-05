package cli

import (
	"net/http"
	"testing"
)

// TestWorkflowIdentityDoesNotReadLegacyLease 删除旧 getenv 调用是必要边界，
// 即使不发送 header，也不能继续从 MCP 进程环境读取 daemon 内部租约。
func TestWorkflowIdentityDoesNotReadLegacyLease(t *testing.T) {
	env := map[string]string{
		"AO_SESSION_ID": testSessionID, envMCPWorkflowID: testWorkflowID,
		envMCPAttemptID: testAttemptID, envMCPCapability: testCapability,
	}
	_, ok := workflowIdentityFromEnv(func(key string) string {
		if key == envMCPLeaseToken {
			t.Fatal("workflow identity read the legacy lease environment variable")
		}
		return env[key]
	})
	if !ok {
		t.Fatal("complete session capability identity was rejected")
	}
}

// TestMCPWorkflowRequiresSessionCapability 去掉 Session/capability 准入条件，
// 不完整身份就会进入真实 HTTP 调用，本用例通过捕获请求发现该退化。
func TestMCPWorkflowRequiresSessionCapability(t *testing.T) {
	for _, missing := range []string{"AO_SESSION_ID", envMCPCapability} {
		t.Run(missing, func(t *testing.T) {
			setWorkflowMCPEnv(t)
			t.Setenv(missing, "")
			capture, deps := newWorkflowMCPDaemon(t, func(w http.ResponseWriter, _ *http.Request, _ *mcpDaemonCapture) {
				http.Error(w, "unexpected request", http.StatusTeapot)
			})
			reply := runWorkflowMCP(t, deps, mcpCall("workflow_messages_list", nil))[0]
			if !reply.Result.IsError || capture.count() != 0 {
				t.Fatalf("incomplete identity: isError=%v HTTP requests=%d", reply.Result.IsError, capture.count())
			}
		})
	}
}
