import { createHash } from "node:crypto";
import type { IpcMain, IpcMainInvokeEvent, WebContents } from "electron";

const id = "[A-Za-z0-9_-]+";
const workflow = `/api/v1/workflows/${id}`;
/** 每个动作固定 method/path；Renderer 不能选择任意 HTTP 方法或目标。 */
const operations = {
	bind: ["POST", `${workflow}/attempts/${id}/bind`],
	decide: ["POST", `${workflow}/task-requests/${id}/decide`],
	resolve: ["POST", `${workflow}/attempts/${id}/resolve`],
	reconcile: ["POST", `${workflow}/attempts/${id}/reconcile`],
	resume: ["POST", `${workflow}/attempts/${id}/resume`],
	create: ["POST", "/api/v1/workflows"],
	"start-plan": ["POST", "/api/v1/workflows/plan"],
	"start-replan": ["POST", `${workflow}/replan`],
	"prepare-plan": ["POST", `${workflow}/plan-candidates`],
	"confirm-plan": ["POST", `${workflow}/plan-candidates/${id}/confirm`],
	plans: ["GET", `${workflow}/plan-candidates`],
	"read-access": ["POST", `${workflow}/read-access`],
	activate: ["POST", `${workflow}/activate`],
	pause: ["POST", `${workflow}/pause`],
	"resume-scheduling": ["POST", `${workflow}/resume-scheduling`],
	"scheduler-policy": ["PUT", "/api/v1/scheduler/policy"],
	budget: ["PUT", `(?:/api/v1/scheduler/budget|${workflow}/budget|${workflow}/tasks/${id}/budget)`],
	intervention: ["POST", `${workflow}/attempts/${id}/interventions`],
	"intervention-receipts": ["GET", `${workflow}/attempts/${id}/interventions`],
	coordination: ["GET", `${workflow}/coordination-summary`],
	messages: ["GET", `${workflow}/messages`],
	"task-requests": ["GET", `${workflow}/task-requests`],
	"task-request": ["GET", `${workflow}/task-requests/${id}`],
	capabilities: ["GET", "/api/v1/workflows/capabilities"],
	"attempt-capabilities": ["GET", `${workflow}/attempts/${id}/capabilities`],
	binding: ["GET", `${workflow}/attempts/${id}/binding`],
	observe: ["GET", `${workflow}/attempts/${id}/observe`],
	integration: ["GET", `${workflow}/integrations/${id}`],
	"integration-operation": ["GET", `${workflow}/integration-operations/${id}`],
	materialize: ["POST", `${workflow}/task-requests/${id}/materialize`],
	"quality-policy": ["PUT", `${workflow}/quality-policy`],
	snapshot: ["POST", `${workflow}/attempts/${id}/artifacts/snapshot`],
	gate: ["POST", `${workflow}/artifacts/${id}/gates`],
	review: ["POST", `${workflow}/artifacts/${id}/reviews`],
	"finish-review": ["POST", `${workflow}/reviews/${id}/finish`],
	commit: ["POST", `${workflow}/artifacts/${id}/commit`],
	preview: ["POST", `${workflow}/integrations/preview`],
	apply: ["POST", `${workflow}/integrations/${id}/apply`],
	push: ["POST", `${workflow}/integrations/${id}/push`],
	"reconcile-integration": ["POST", `${workflow}/integration-operations/${id}/reconcile`],
} as const;
export type OperatorVerb = keyof typeof operations;

export interface WorkflowReadRequest { workflowId: string; verb: OperatorVerb; operationPath: string }
export interface WorkflowReadAccess { workflowId: string; expiresAt: string }

export interface OperatorVerbRequest {
	verb: OperatorVerb;
	/** 必须是对应操作的完整 /api/v1/workflows/... 路径。 */
	operationPath: string;
	/** 审批与提交使用同一份 UTF-8 原文；缺省表示空正文。 */
	body?: string;
}

export interface OperatorVerbResult {
	status: number;
	body: unknown;
	code?: string;
}

export interface OperatorDeps {
	ipcMain: Pick<IpcMain, "handle">;
	baseURL: () => string | null;
	/** 同端口 daemon 重启也会改变此身份，旧授权及迟到响应必须失效。 */
	daemonIdentity?: () => string;
	/** 只能由 Main 提供当前主窗口的 shell，不能从 IPC 参数推导。 */
	trustedWebContents: () => WebContents | null;
	rendererURL: () => string;
	confirmOperation: (input: Required<OperatorVerbRequest> & { method: "GET" | "POST" | "PUT" }) => Promise<boolean>;
	request?: typeof fetch;
}

/** IPC 在协议校验、来源校验或用户取消时闭锁，不把本地确认当作 OS 授权。 */
export class OperatorIpcError extends Error {
	/** 保留稳定错误码供桌面调用方处理。 */
	constructor(
		readonly code: "OPERATOR_UNTRUSTED_SENDER" | "OPERATOR_INVALID_REQUEST" | "OPERATOR_CANCELLED"
			| "OPERATOR_DAEMON_UNAVAILABLE" | "OPERATOR_REQUEST_FAILED",
		message: string,
	) {
		super(message);
		this.name = "OperatorIpcError";
	}
}

/** 仅允许主窗口顶层 frame 的固定 app 来源或本地 Vite 来源。 */
function requireTrustedSender(deps: OperatorDeps, event: IpcMainInvokeEvent): void {
	const shell = deps.trustedWebContents();
	const frame = event.senderFrame;
	let trusted = false;
	if (shell && !shell.isDestroyed() && event.sender === shell && frame === shell.mainFrame && frame && !frame.parent) {
		try {
			const expected = new URL(deps.rendererURL());
			const actual = new URL(frame.url);
			const local = (expected.protocol === "app:" && expected.host === "renderer")
				|| (expected.protocol === "http:" && ["127.0.0.1", "localhost"].includes(expected.hostname));
			// 自定义 app:// 的 URL.origin 为 null，必须分别比较协议与 host。
			trusted = local && actual.protocol === expected.protocol && actual.host === expected.host
				&& !actual.username && !actual.password;
		} catch {
			trusted = false;
		}
	}
	if (!trusted) throw new OperatorIpcError("OPERATOR_UNTRUSTED_SENDER", "操作只能由桌面主窗口发起");
}

/** 在信任边界校验 verb、路径与原文大小，不规范化可能改变目标的 URL。 */
function validateRequest(value: unknown): Required<OperatorVerbRequest> {
	if (value && typeof value === "object" && !Array.isArray(value)) {
		const input = value as Record<string, unknown>;
		const { verb, operationPath, body = "" } = input;
		if (typeof verb === "string" && Object.hasOwn(operations, verb)
			&& typeof operationPath === "string" && operationPath.length <= 1024
			&& typeof body === "string" && Buffer.byteLength(body, "utf8") <= 64 * 1024
			&& Object.keys(input).every((key) => ["verb", "operationPath", "body"].includes(key))) {
			const route = new RegExp(`^${operations[verb as OperatorVerb][1]}$`);
			if (route.exec(operationPath)?.[0] === operationPath && (operations[verb as OperatorVerb][0] !== "GET" || body === "")) return { verb: verb as OperatorVerb, operationPath, body };
		}
	}
	throw new OperatorIpcError("OPERATOR_INVALID_REQUEST", "操作、路径或正文无效");
}

/** 保留 daemon 原始错误 envelope；无 JSON 的响应仍可交给调用方展示。 */
async function resultOf(response: Response): Promise<OperatorVerbResult> {
	const raw = await response.text();
	let body: unknown = raw || null;
	try {
		body = JSON.parse(raw) as unknown;
	} catch {
		// 无正文和非 JSON 响应保留原值。
	}
	const code = body && typeof body === "object" && "code" in body && typeof body.code === "string" ? body.code : undefined;
	return { status: response.status, body, code };
}

/** 验证 daemon 的审批响应确实关联当前请求，不让无关的成功响应推进授权。 */
function approvalOf(body: unknown, state: "pending" | "confirmed", operationMethod: string, operationPath: string, payloadDigest: string) {
	const approval = (body as { approval?: {
		approvalId?: unknown; code?: unknown; state?: unknown; payloadDigest?: unknown;
		operation?: { method?: unknown; path?: unknown };
	} } | null)?.approval;
	if (!approval || typeof approval.approvalId !== "string" || !/^[A-Za-z0-9_-]+$/.test(approval.approvalId)
		|| approval.state !== state || approval.payloadDigest !== payloadDigest
		|| approval.operation?.method !== operationMethod || approval.operation.path !== operationPath
		|| (state === "pending" && (typeof approval.code !== "string" || !approval.code))) {
		throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "daemon 返回的审批与当前操作不匹配");
	}
	return { approvalId: approval.approvalId, code: approval.code };
}

/** 拦截响应顶层已知 operator 凭据字段；普通正文和错误说明不做关键词扫描。 */
function assertNoOperatorCredential(value: unknown): void {
	if (value && typeof value === "object"
		&& (Object.hasOwn(value, "operatorToken") || Object.hasOwn(value, "X-AO-Operator-Token") || Object.hasOwn(value, "readToken"))) {
		throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "daemon 响应携带了 operator 凭据字段");
	}
}

/**
 * 单个特权入口：原生对话框展示操作，daemon 完成 Windows Hello 后才提交原文。
 * Main 不读取或缓存 token；单次审批的 TTL、OS 验证与消费由 daemon 强制执行。
 */
export function registerOperatorIpc(deps: OperatorDeps): void {
	const request = deps.request ?? fetch;
	const grants = new Map<string, { token: string; expiresAt: string; base: string; identity: string }>();
	const pending = new Map<string, Promise<WorkflowReadAccess>>();
	let authorizationQueue: Promise<unknown> = Promise.resolve();
	const identity = () => deps.daemonIdentity?.() ?? deps.baseURL() ?? "";
	/** 所有审批仍走同一精确原文链；只读凭据仅由私有调用接收。 */
	const run = async (event: IpcMainInvokeEvent, input: Required<OperatorVerbRequest>, readAccess = false): Promise<OperatorVerbResult> => {
		requireTrustedSender(deps, event);
		const base = deps.baseURL();
		const epoch = identity();
		if (!base || /^http:\/\/127\.0\.0\.1:[0-9]{1,5}$/.exec(base)?.[0] !== base || !URL.canParse(base)) {
			throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "本机 daemon 未就绪");
		}
		const { operationPath, body } = input;
		const operationMethod = operations[input.verb][0];
		if (!await deps.confirmOperation({ ...input, method: operationMethod })) throw new OperatorIpcError("OPERATOR_CANCELLED", "操作已取消");
		const payloadDigest = `sha256:${createHash("sha256").update(body, "utf8").digest("hex")}`;

		/** 每个异步步骤后复核来源及 daemon，禁止重定向携带审批。 */
		const post = async (route: string, payload: string, approvalId?: string, method = "POST") => {
			requireTrustedSender(deps, event);
			if (deps.baseURL() !== base || identity() !== epoch) throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "daemon 已变更，请重新发起操作");
			const headers: Record<string, string> = { "Content-Type": "application/json" };
			if (approvalId) headers["X-AO-Operator-Approval"] = approvalId;
			try {
				const response = await request(`${base}${route}`, { method, headers, body: method === "GET" ? undefined : payload, redirect: "error" });
				const result = await resultOf(response);
				requireTrustedSender(deps, event);
				if (deps.baseURL() !== base || identity() !== epoch) throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "daemon 已变更，请重新读取");
				if (!(readAccess && route === operationPath)) assertNoOperatorCredential(result.body);
				return result;
			} catch (error) {
				if (error instanceof OperatorIpcError) throw error;
				throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "无法完成本机 daemon 请求");
			}
		};
		const created = await post("/api/v1/operator/approvals", JSON.stringify({ operationMethod, operationPath, payloadDigest, payload: body }));
		if (created.status < 200 || created.status >= 300) return created;
		const approval = approvalOf(created.body, "pending", operationMethod, operationPath, payloadDigest);
		const confirmed = await post(`/api/v1/operator/approvals/${approval.approvalId}/confirm`, JSON.stringify({ code: approval.code, payloadDigest }));
		if (confirmed.status < 200 || confirmed.status >= 300) return confirmed;
		const verified = approvalOf(confirmed.body, "confirmed", operationMethod, operationPath, payloadDigest);
		if (verified.approvalId !== approval.approvalId) throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "daemon 确认了其他审批");
		return post(operationPath, body, approval.approvalId, operationMethod);
	};
	deps.ipcMain.handle("operator:runVerb", async (event, value: unknown) => {
		requireTrustedSender(deps, event);
		const input = validateRequest(value);
		if (input.verb === "read-access") throw new OperatorIpcError("OPERATOR_INVALID_REQUEST", "只读授权必须通过专用入口");
		try {
			return await run(event, input);
		} catch (error) {
			// Electron invoke 不保证保留 Error.code；只有确定尚未发送的原生取消转为可序列化结果。
			if (error instanceof OperatorIpcError && error.code === "OPERATOR_CANCELLED") {
				return { status: 499, code: error.code, body: { code: error.code, message: error.message } };
			}
			throw error;
		}
	});
	/** 只有明确的用户授权动作可进入原生确认；并发点击共用一次待完成授权。 */
	deps.ipcMain.handle("operator:authorizeWorkflow", async (event, value: unknown): Promise<WorkflowReadAccess> => {
		requireTrustedSender(deps, event);
		if (typeof value !== "string" || !new RegExp(`^${id}$`).test(value)) throw new OperatorIpcError("OPERATOR_INVALID_REQUEST", "工作流 ID 无效");
		const key = `${identity()}:${value}`;
		const existing = pending.get(key);
		if (existing) return existing;
		const base = deps.baseURL(); const epoch = identity();
		const authorize = authorizationQueue.then(async () => {
			if (deps.baseURL() !== base || identity() !== epoch) throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "daemon 已变更");
			const result = await run(event, { verb: "read-access", operationPath: `/api/v1/workflows/${value}/read-access`, body: "" }, true);
			const grant = result.body as { readToken?: unknown; expiresAt?: unknown } | null;
			if (result.status !== 200 || !base || typeof grant?.readToken !== "string" || !/^[a-f0-9]{64}$/.test(grant.readToken) || typeof grant.expiresAt !== "string" || !(Date.parse(grant.expiresAt) > Date.now())) throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "只读授权失败");
			grants.set(value, { token: grant.readToken, expiresAt: grant.expiresAt, base, identity: epoch });
			return { workflowId: value, expiresAt: grant.expiresAt };
		});
		pending.set(key, authorize); authorizationQueue = authorize.catch(() => undefined);
		try { return await authorize; } finally { pending.delete(key); }
	});
	/** 查询只使用 Main 内的已有授权，挂载、失效或重试均不能弹出授权。 */
	deps.ipcMain.handle("operator:readWorkflow", async (event, value: unknown): Promise<OperatorVerbResult> => {
		requireTrustedSender(deps, event);
		const input = value as WorkflowReadRequest | null;
		if (!input || typeof input.workflowId !== "string" || !new RegExp(`^${id}$`).test(input.workflowId) || Object.keys(input).some(key => !["workflowId", "verb", "operationPath"].includes(key))) throw new OperatorIpcError("OPERATOR_INVALID_REQUEST", "读取目标无效");
		const validated = validateRequest({ verb: input.verb, operationPath: input.operationPath });
		if (operations[validated.verb][0] !== "GET" || (validated.verb !== "capabilities" && !validated.operationPath.startsWith(`/api/v1/workflows/${input.workflowId}/`))) throw new OperatorIpcError("OPERATOR_INVALID_REQUEST", "只读范围不匹配");
		const grant = grants.get(input.workflowId);
		if (!grant || grant.base !== deps.baseURL() || grant.identity !== identity() || Date.parse(grant.expiresAt) <= Date.now()) {
			grants.delete(input.workflowId);
			return { status: 403, code: "WORKFLOW_READ_ACCESS_REQUIRED", body: { message: "请授权读取此工作流", code: "WORKFLOW_READ_ACCESS_REQUIRED" } };
		}
		const response = await request(`${grant.base}${validated.operationPath}`, { method: "GET", headers: { "X-AO-Workflow-Read-Access": grant.token }, redirect: "error" });
		const result = await resultOf(response);
		requireTrustedSender(deps, event);
		if (deps.baseURL() !== grant.base || identity() !== grant.identity) throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "daemon 已变更");
		assertNoOperatorCredential(result.body);
		if (result.status === 403) grants.delete(input.workflowId);
		return result;
	});
}
