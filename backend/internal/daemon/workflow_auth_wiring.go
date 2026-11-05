package daemon

import (
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/operatorconsent"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// workflowOperatorAuth 将每次启动的审批表、OS 验证器和 Agent 身份解析器一起装配。
type workflowOperatorAuth struct {
	Principals *principal.Resolver
	Approvals  *principal.ApprovalStore
	Consent    principal.ConsentVerifier
}

// newWorkflowOperatorAuth 装配本次启动的唯一 Operator 身份边界。
//
// # 为什么不再铸造或读取 token 文件
//
// 旧实现把 32 字节随机 secret 写进 <dataDir>/operator.token（0600），再要求请求在
// X-AO-Operator-Token 里出示它。这个证明不足：POSIX 的 0600 能限制其他用户，却不区分同用户
// 的不同进程；Windows 也不能据此推导进程隔离。于是"出示 token"实际证明的是
// "我是这台机器上可以读文件的某个进程"，而不是"有人在场"。再叠加"审批创建在回环上无需认证"，
// 就构成完整的绕过链：Agent 的 shell 可以创建审批、读取 token、用 token 确认自己刚创建的审批，
// 人工手势变成装饰。
//
// 因此现在既不铸造也不读取该文件：
//   - 没有任何可被复制的凭证文件，"同用户进程能读到它"这个事实就不再是提权路径；
//   - 唯一的 Operator 证明是本次启动内存中的一次性审批，且必须经过 OS 原生人工验证
//     （operatorconsent.New()：Windows Hello / 其它平台 ErrConsentUnavailable）；
//   - 审批随进程消失，重启即全部失效，无需清理代码。
//
// 剩下的残余边界必须说清楚：与本进程同用户的进程仍能直接调用本机 API 创建审批，但它无法完成
// OS 人工验证，所以仍拿不到任何 Operator 授权。OS 用户边界之外的隔离不在此函数的能力范围内。
func newWorkflowOperatorAuth(
	_ string,
	store *sqlite.Store,
	authority principal.CapabilityAuthority,
	_ *slog.Logger,
) workflowOperatorAuth {
	approvals := principal.NewApprovalStore(principal.ApprovalStoreOptions{})
	trust := principal.ApprovalOperatorTrust{Approvals: approvals}
	return workflowOperatorAuth{
		Principals: principal.StoreResolver(store, store, authority).WithOperatorTrust(trust),
		Approvals:  approvals,
		Consent:    operatorconsent.New(),
	}
}

// newWorkflowPrincipals 保留现有调用入口，所有正式解析器共享相同授权规则。
func newWorkflowPrincipals(dataDir string, store *sqlite.Store, authority principal.CapabilityAuthority, log *slog.Logger) *principal.Resolver {
	return newWorkflowOperatorAuth(dataDir, store, authority, log).Principals
}

// logWorkflowOperatorAuth 只记录验证器装配状态，不将人工授权或凭据写入日志。
func logWorkflowOperatorAuth(log *slog.Logger, auth workflowOperatorAuth) {
	if log == nil {
		return
	}
	log.Info("workflow operator auth wiring",
		"nativeConsent", auth.Consent != nil,
		"approvals", auth.Approvals != nil,
		"approvalTTLSeconds", int(auth.Approvals.TTL().Seconds()),
		"principals", auth.Principals != nil)
}
