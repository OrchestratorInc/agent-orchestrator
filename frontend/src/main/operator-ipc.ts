import { createHash } from "node:crypto";
import type { IpcMain, IpcMainInvokeEvent, WebContents } from "electron";

export type OperatorVerb = "bind" | "decide" | "resolve" | "reconcile" | "resume";

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
	/** 只能由 Main 提供当前主窗口的 shell，不能从 IPC 参数推导。 */
	trustedWebContents: () => WebContents | null;
	rendererURL: () => string;
	confirmOperation: (input: Required<OperatorVerbRequest>) => Promise<boolean>;
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
		if (typeof verb === "string" && ["bind", "decide", "resolve", "reconcile", "resume"].includes(verb)
			&& typeof operationPath === "string" && operationPath.length <= 1024
			&& typeof body === "string" && Buffer.byteLength(body, "utf8") <= 64 * 1024
			&& Object.keys(input).every((key) => ["verb", "operationPath", "body"].includes(key))) {
			const collection = verb === "decide" ? "task-requests" : "attempts";
			const route = new RegExp(`^/api/v1/workflows/[A-Za-z0-9_-]+/${collection}/[A-Za-z0-9_-]+/${verb}$`);
			if (route.exec(operationPath)?.[0] === operationPath) return { verb: verb as OperatorVerb, operationPath, body };
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
function approvalOf(body: unknown, state: "pending" | "confirmed", operationPath: string, payloadDigest: string) {
	const approval = (body as { approval?: {
		approvalId?: unknown; code?: unknown; state?: unknown; payloadDigest?: unknown;
		operation?: { method?: unknown; path?: unknown };
	} } | null)?.approval;
	if (!approval || typeof approval.approvalId !== "string" || !/^[A-Za-z0-9_-]+$/.test(approval.approvalId)
		|| approval.state !== state || approval.payloadDigest !== payloadDigest
		|| approval.operation?.method !== "POST" || approval.operation.path !== operationPath
		|| (state === "pending" && (typeof approval.code !== "string" || !approval.code))) {
		throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "daemon 返回的审批与当前操作不匹配");
	}
	return { approvalId: approval.approvalId, code: approval.code };
}

/** 拦截响应顶层已知 operator 凭据字段；普通正文和错误说明不做关键词扫描。 */
function assertNoOperatorCredential(value: unknown): void {
	if (value && typeof value === "object"
		&& (Object.hasOwn(value, "operatorToken") || Object.hasOwn(value, "X-AO-Operator-Token"))) {
		throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "daemon 响应携带了 operator 凭据字段");
	}
}

/**
 * 单个特权入口：原生对话框展示操作，daemon 完成 Windows Hello 后才提交原文。
 * Main 不读取或缓存 token；单次审批的 TTL、OS 验证与消费由 daemon 强制执行。
 */
export function registerOperatorIpc(deps: OperatorDeps): void {
	const request = deps.request ?? fetch;
	deps.ipcMain.handle("operator:runVerb", async (event, value: unknown): Promise<OperatorVerbResult> => {
		requireTrustedSender(deps, event);
		const input = validateRequest(value);
		const base = deps.baseURL();
		if (!base || /^http:\/\/127\.0\.0\.1:[0-9]{1,5}$/.exec(base)?.[0] !== base || !URL.canParse(base)) {
			throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "本机 daemon 未就绪");
		}
		if (!await deps.confirmOperation(input)) throw new OperatorIpcError("OPERATOR_CANCELLED", "操作已取消");
		const { operationPath, body } = input;
		const payloadDigest = `sha256:${createHash("sha256").update(body, "utf8").digest("hex")}`;

		/** 每个异步步骤后复核来源及 daemon，禁止重定向携带审批。 */
		const post = async (route: string, payload: string, approvalId?: string) => {
			requireTrustedSender(deps, event);
			if (deps.baseURL() !== base) throw new OperatorIpcError("OPERATOR_DAEMON_UNAVAILABLE", "daemon 已变更，请重新发起操作");
			const headers: Record<string, string> = { "Content-Type": "application/json" };
			if (approvalId) headers["X-AO-Operator-Approval"] = approvalId;
			try {
				const response = await request(`${base}${route}`, { method: "POST", headers, body: payload, redirect: "error" });
				const result = await resultOf(response);
				assertNoOperatorCredential(result.body);
				return result;
			} catch (error) {
				if (error instanceof OperatorIpcError) throw error;
				throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "无法完成本机 daemon 请求");
			}
		};
		const created = await post("/api/v1/operator/approvals", JSON.stringify({ operationMethod: "POST", operationPath, payloadDigest, payload: body }));
		if (created.status < 200 || created.status >= 300) return created;
		const approval = approvalOf(created.body, "pending", operationPath, payloadDigest);
		const confirmed = await post(`/api/v1/operator/approvals/${approval.approvalId}/confirm`, JSON.stringify({ code: approval.code, payloadDigest }));
		if (confirmed.status < 200 || confirmed.status >= 300) return confirmed;
		const verified = approvalOf(confirmed.body, "confirmed", operationPath, payloadDigest);
		if (verified.approvalId !== approval.approvalId) throw new OperatorIpcError("OPERATOR_REQUEST_FAILED", "daemon 确认了其他审批");
		return post(operationPath, body, approval.approvalId);
	});
}
