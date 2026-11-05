// @vitest-environment node
import { createHash } from "node:crypto";
import type { IpcMainInvokeEvent } from "electron";
import { describe, expect, it, vi } from "vitest";
import { registerOperatorIpc } from "./operator-ipc";

type Handler = (event: IpcMainInvokeEvent, input: unknown) => Promise<unknown>;
const base = "http://127.0.0.1:41321";
const operationPath = "/api/v1/workflows/wf-1/attempts/attempt-1/bind";
const rawBody = '{\r\n  "amount": 1.00, "label": "审批", "escaped": "\\u0061", "note": "operator.token 和 X-AO-Operator-Token 已停用"\r\n}\n';
const digest = (body: string) => `sha256:${createHash("sha256").update(body).digest("hex")}`;

/** 驱动真实 IPC handler；对话框与 HTTP 使用假实现，不访问用户状态。 */
function setup() {
	const handlers = new Map<string, Handler>();
	const frame = { url: "app://renderer/index.html", parent: null };
	const sender = { mainFrame: frame, isDestroyed: () => false };
	const event = { sender, senderFrame: frame } as unknown as IpcMainInvokeEvent;
	const confirmOperation = vi.fn(async (_input: unknown) => true);
	let approval: Record<string, unknown>;
	const request = vi.fn(async (_url: string | URL | Request, init?: RequestInit) => {
		if (request.mock.calls.length === 1) {
			const created = JSON.parse(init!.body as string);
			approval = {
				approvalId: "approval-1", code: "12345678", state: "pending",
				operation: { method: "POST", path: created.operationPath },
				payloadDigest: created.payloadDigest, expiresAt: "2099-01-01T00:00:00Z",
			};
			return Response.json({ approval }, { status: 201 });
		}
		if (request.mock.calls.length === 2) return Response.json({ approval: { ...approval, code: undefined, state: "confirmed" } });
		return Response.json({ applied: true });
	});
	const deps = {
		ipcMain: { handle: (channel: string, handler: Handler) => handlers.set(channel, handler) },
		baseURL: () => base, trustedWebContents: () => sender,
		rendererURL: () => "app://renderer/index.html", confirmOperation, request,
	};
	registerOperatorIpc(deps as never);
	const invoke = (input: unknown = { verb: "bind", operationPath, body: rawBody }, source = event) =>
		handlers.get("operator:runVerb")!(source, input);
	return { handlers, frame, sender, event, deps, confirmOperation, request, invoke };
}

describe("operator IPC", () => {
	// 删除来源检查会允许非主窗口触发特权操作。
	it("拒绝其他窗口、子 frame、伪装 app 来源和远程来源", async () => {
		const h = setup();
		for (const source of [
			{ ...h.event, sender: { ...h.sender } },
			{ ...h.event, senderFrame: { ...h.frame, parent: h.frame } },
			{ ...h.event, senderFrame: null },
		]) await expect(h.invoke(undefined, source as IpcMainInvokeEvent)).rejects.toMatchObject({ code: "OPERATOR_UNTRUSTED_SENDER" });
		for (const url of ["app://other/index.html", "https://renderer/index.html", "https://evil.test", "app://user@renderer/index.html"]) {
			h.frame.url = url;
			await expect(h.invoke()).rejects.toMatchObject({ code: "OPERATOR_UNTRUSTED_SENDER" });
		}
		expect(h.confirmOperation).not.toHaveBeenCalled();
		expect(h.request).not.toHaveBeenCalled();
	});

	// 删除模板校验会恢复任意路由代理或允许 renderer 传入审批凭证。
	it("只接受匹配 verb 的五个路由以及有限原文正文", async () => {
		const h = setup();
		for (const path of [
			`${base}${operationPath}`, `${operationPath}?x=1`, `${operationPath}#x`, `${operationPath}\n`,
			"/api/v1/workflows/../attempts/a/bind", "/api/v1/workflows/w%2fa/attempts/a/bind",
			"/api/v1/workflows/w%5ca/attempts/a/bind", "/api/v1/workflows/w/attempts/a/resume",
			"/api/v1/shutdown", "/api/v1/workflows/w/attempts/a/bind/",
		]) await expect(h.invoke({ verb: "bind", operationPath: path })).rejects.toMatchObject({ code: "OPERATOR_INVALID_REQUEST" });
		for (const input of [
			{ verb: "delete", operationPath }, { verb: "bind", operationPath, body: {} },
			{ verb: "bind", operationPath, approvalId: "forged" },
			{ verb: "bind", operationPath, body: "中".repeat(22000) },
		]) await expect(h.invoke(input)).rejects.toMatchObject({ code: "OPERATOR_INVALID_REQUEST" });
		expect(h.confirmOperation).not.toHaveBeenCalled();
		expect(h.request).not.toHaveBeenCalled();
	});

	// 删除确认结果检查会让取消仍创建审批。
	it("取消时不请求 daemon", async () => {
		const h = setup();
		h.confirmOperation.mockResolvedValue(false);
		await expect(h.invoke()).rejects.toMatchObject({ code: "OPERATOR_CANCELLED" });
		expect(h.confirmOperation).toHaveBeenCalledWith({ verb: "bind", operationPath, body: rawBody });
		expect(h.request).not.toHaveBeenCalled();
	});

	// 删除等待后的复核会允许导航或 daemon 切换继承之前的确认。
	it("等待确认期间窗口或 daemon 变化时闭锁", async () => {
		for (const change of ["frame", "daemon"] as const) {
			const h = setup();
			h.confirmOperation.mockImplementation(async () => {
				if (change === "frame") h.frame.url = "https://evil.test";
				else h.deps.baseURL = () => "http://127.0.0.1:41322";
				return true;
			});
			await expect(h.invoke()).rejects.toMatchObject({ code: change === "frame" ? "OPERATOR_UNTRUSTED_SENDER" : "OPERATOR_DAEMON_UNAVAILABLE" });
			expect(h.request).not.toHaveBeenCalled();
		}
	});

	// 删除顺序/摘要/原文传送中的任何一步都会破坏单次审批协议。
	it("五路由依次创建、OS 确认、提交，保留原文且不暴露审批凭证", async () => {
		for (const verb of ["bind", "decide", "resolve", "reconcile", "resume"]) {
			const h = setup();
			const route = `/api/v1/workflows/wf-1/${verb === "decide" ? "task-requests/request-1" : "attempts/attempt-1"}/${verb}`;
			const body = verb === "resume" ? "" : rawBody;
			const result = await h.invoke({ verb, operationPath: route, ...(body ? { body } : {}) });
			expect([...h.handlers.keys()]).toEqual(["operator:runVerb"]);
			expect(h.request.mock.calls.map(([url]) => url)).toEqual([
				`${base}/api/v1/operator/approvals`, `${base}/api/v1/operator/approvals/approval-1/confirm`, `${base}${route}`,
			]);
			const calls = h.request.mock.calls.map(([, init]) => init!);
			expect(JSON.parse(calls[0]!.body as string)).toEqual({ operationMethod: "POST", operationPath: route, payloadDigest: digest(body), payload: body });
			expect(JSON.parse(calls[1]!.body as string)).toEqual({ code: "12345678", payloadDigest: digest(body) });
			expect(calls[2]!.body).toBe(body);
			expect(calls.map((call) => new Headers(call.headers).get("X-AO-Operator-Approval"))).toEqual([null, null, "approval-1"]);
			for (const call of calls) {
				expect(call.method).toBe("POST");
				expect(call.redirect).toBe("error");
				expect(new Headers(call.headers).has("X-AO-Operator-Token")).toBe(false);
			}
			expect(result).toEqual({ status: 200, body: { applied: true }, code: undefined });
		}
	});

	// 删除失败或关联校验会提交未经成功确认的操作；同一失败门禁合并验证。
	it("创建或 OS 确认失败、不匹配的成功响应均不提交操作", async () => {
		for (const stage of [0, 1]) {
			const h = setup();
			if (stage === 1) h.request.mockImplementationOnce(async () => Response.json({ approval: {
				approvalId: "approval-1", code: "12345678", state: "pending",
				operation: { method: "POST", path: operationPath }, payloadDigest: digest(rawBody), expiresAt: "2099-01-01T00:00:00Z",
			} }));
			h.request.mockResolvedValueOnce(Response.json({ code: "OPERATOR_CONSENT_DENIED", requestId: "request-1" }, { status: 403 }));
			await expect(h.invoke()).resolves.toEqual({ status: 403, code: "OPERATOR_CONSENT_DENIED", body: { code: "OPERATOR_CONSENT_DENIED", requestId: "request-1" } });
			expect(h.request).toHaveBeenCalledTimes(stage + 1);
		}
		const h = setup();
		h.request.mockResolvedValueOnce(Response.json({ approval: { approvalId: "approval-1", code: "12345678", state: "pending", payloadDigest: "wrong" } }));
		await expect(h.invoke()).rejects.toMatchObject({ code: "OPERATOR_REQUEST_FAILED" });
		expect(h.request).toHaveBeenCalledTimes(1);
	});
});

describe("operator IPC 响应凭据保护", () => {
	// 删除响应字段检查后，操作结果会把已知顶层 operator 凭据字段传给 renderer。
	it("最终操作响应包含凭据字段时闭锁", async () => {
		for (const field of ["operatorToken", "X-AO-Operator-Token"]) {
			const h = setup();
			const respond = h.request.getMockImplementation()!;
			h.request.mockImplementation(async (url, init) => h.request.mock.calls.length === 3
				? Response.json({ [field]: "leaked" }) : respond(url, init));
			await expect(h.invoke()).rejects.toMatchObject({ code: "OPERATOR_REQUEST_FAILED" });
			expect(h.request).toHaveBeenCalledTimes(3);
		}
	});
});
