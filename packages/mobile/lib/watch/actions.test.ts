import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "../config";
import type { ConversationActivity, ConversationSnapshot } from "../chat/types";
import { actionContext, createWatchActionHandler } from "./actions";

const cfg = { hostId: "alpha", password: "SYNTHETIC_ONLY" } as ServerConfig;
const approval = (over: Partial<ConversationActivity> = {}): ConversationActivity => ({
	kind: "activity", id: "activity-1", requestId: "request-1", revision: 1, sequence: 1, createdAt: "2026-10-09T00:00:00Z", status: "pending", activityKind: "approval", summary: "Run status",
	detail: { method: "item/commandExecution/requestApproval", command: "git status", rawCommand: "git status", cwd: "/demo", reason: "Check worktree", scopeComplete: true },
	decisions: [{ id: "opaque-allow", label: "Allow once", kind: "allow_once" }, { id: "opaque-deny", label: "Deny", kind: "reject_once" }, { id: "broad", label: "Allow session", kind: "allow_always" }], ...over,
});
const snapshot = (items: ConversationActivity[] = [approval()], over: Partial<ConversationSnapshot> = {}): ConversationSnapshot => ({ conversationId: "c-1", sessionId: "worker", harness: "codex", mode: "chat", controller: { state: "ready" }, latestSequence: 1, oldestSequence: 1, hasMoreBefore: false, turns: [], items, settings: {}, ...over });
const inspect = { kind: "inspect", hostId: "alpha", sessionId: "worker" };
function setup(initial = snapshot()) {
	let live = initial;
	let time = 1000;
	let active = true;
	const deps = {
		configForHost: vi.fn((id: string) => id === "alpha" ? cfg : null),
		read: vi.fn(async () => live), approve: vi.fn(async () => {}), input: vi.fn(async () => {}), message: vi.fn(async () => {}),
		now: () => time, newId: () => "token", isActive: async () => active,
	};
	const handle = createWatchActionHandler(deps);
	const run = (data: unknown) => handle(data, "native-request", 30_000);
	return { deps, run, setLive: (s: ConversationSnapshot) => { live = s; }, setTime: (t: number) => { time = t; }, setActive: (a: boolean) => { active = a; } };
}

describe("Watch action scope", () => {
	it("offers only provider-semantic one-time decisions and shows the complete scope", () => {
		const result = actionContext(snapshot());
		expect(result).toMatchObject({ kind: "approval", requestId: "request-1", scope: "git status\nIn: /demo\nCheck worktree", decisions: [{ id: "opaque-allow", label: "Allow once", kind: "allow_once" }, { id: "opaque-deny", label: "Deny", kind: "reject_once" }] });
	});
	it("fails closed for long, truncated, ambiguous, destructive, broad-only and TUI requests", () => {
		for (const detail of [{ command: "x".repeat(300) }, { textTruncated: true }, { rawCommand: "git status; rm -rf /demo" }, { command: "rm -rf /demo", rawCommand: "rm -rf /demo" }, { networkPolicyAmendment: {} }]) {
			expect(actionContext(snapshot([approval({ detail: { ...approval().detail, ...detail } })]))).toBeNull();
		}
		expect(actionContext(snapshot([approval({ decisions: [{ id: "accept", label: "Allow once" }] })]))).toBeNull();
		// Partial adapter projection (e.g. networkApprovalContext dropped) or an older daemon: no completeness proof.
		const { scopeComplete: _, ...partial } = approval().detail!;
		for (const detail of [partial, { ...partial, scopeComplete: false }]) {
			expect(actionContext(snapshot([approval({ detail })]))).toBeNull();
		}
		expect(actionContext(snapshot([approval({ decisions: [{ id: "all", label: "Allow", kind: "allow_always" }] })]))).toBeNull();
		expect(actionContext(snapshot([], { mode: "tui" }))).toBeNull();
	});
	it("supports only short single-string input, not forms/URLs/secrets or hidden pending history", () => {
		const input = approval({ activityKind: "user_input", detail: { inputMode: "form", url: "", message: "What should I check?", schema: { type: "object", properties: { answer: { type: "string", maxLength: 100 } }, required: ["answer"] } } });
		expect(actionContext(snapshot([input]))).toMatchObject({ kind: "input", field: "answer", maxLength: 100 });
		for (const property of [{ type: "string", enum: ["one"] }, { type: "string", format: "password" }, { type: "boolean" }]) {
			expect(actionContext(snapshot([approval({ activityKind: "user_input", detail: { ...input.detail, schema: { properties: { answer: property as never } } } })]))).toBeNull();
		}
		expect(actionContext(snapshot([], { hasMoreBefore: true }))).toBeNull();
	});
});

describe("Watch action dispatcher", () => {
	it("binds exact host/session/token, refreshes pending context and never trusts a watch scope", async () => {
		const { run, deps } = setup();
		expect(await run(inspect)).toMatchObject({ status: "context", context: { token: "token" } });
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "sent" });
		expect(deps.read).toHaveBeenCalledTimes(2);
		expect(deps.approve).toHaveBeenCalledWith(cfg, "worker", "request-1", "opaque-allow");
		expect(await run({ ...inspect, hostId: "beta" })).toMatchObject({ status: "open_phone" });
		expect(await run({ ...inspect, sessionId: "other", kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "open_phone" });
	});
	it("rejects unoffered decisions and stale/changed confirmation without writes", async () => {
		const { run, deps, setLive, setTime } = setup();
		await run(inspect);
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "broad" })).toMatchObject({ status: "error", code: "CHAT_DECISION_NOT_OFFERED", httpStatus: 400 });
		setLive(snapshot([approval({ detail: { ...approval().detail, cwd: "/other" } })]));
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "open_phone" });
		setTime(70_000);
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "open_phone" });
		expect(deps.approve).not.toHaveBeenCalled();
	});
	it("never starts a late or offline approval and does not queue it for recovery", async () => {
		const { run, deps, setActive } = setup();
		await run(inspect);
		setActive(false);
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "open_phone" });
		setActive(true);
		expect(deps.approve).not.toHaveBeenCalled();
	});
	it("rechecks phone readiness after the network read", async () => {
		const { run, deps, setActive } = setup();
		await run(inspect);
		deps.read.mockImplementation(async () => { setActive(false); return snapshot(); });
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-deny" })).toMatchObject({ status: "open_phone" });
		expect(deps.approve).not.toHaveBeenCalled();
	});
	it("shows expired/resolved as already handled, not as Watch approval success", async () => {
		const { run, deps, setLive } = setup();
		await run(inspect);
		setLive(snapshot([]));
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "already_handled" });
		setLive(snapshot());
		deps.approve.mockRejectedValue({ status: 409, code: "CHAT_REQUEST_NOT_PENDING" });
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "already_handled", httpStatus: 409 });
	});
	it("does not write after a late native readiness answer or swapped phone credentials", async () => {
		const { run, deps, setTime } = setup();
		await run(inspect);
		deps.isActive = async () => { setTime(31_000); return true; };
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "open_phone" });
		expect(deps.approve).not.toHaveBeenCalled();
	});
	it("rejects an ambiguous duplicate decision ID before filtering out broad options", () => {
		const options = approval().decisions!;
		expect(actionContext(snapshot([approval({ decisions: [...options, { id: options[0].id, label: "Always", kind: "allow_always" }] })]))).toBeNull();
	});

	it("preserves 400 CHAT_DECISION_NOT_OFFERED and treats a lost send response as uncertain", async () => {
		const { run, deps } = setup();
		await run(inspect);
		deps.approve.mockRejectedValueOnce({ status: 400, code: "CHAT_DECISION_NOT_OFFERED" });
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "error", code: "CHAT_DECISION_NOT_OFFERED", httpStatus: 400 });
		deps.approve.mockRejectedValueOnce(new Error("offline"));
		expect(await run({ ...inspect, kind: "decide", token: "token", decisionId: "opaque-allow" })).toMatchObject({ status: "uncertain" });
	});
	it("uses messages with the same clientMessageId for repeated sends, never converts approvals to replies", async () => {
		const { run, deps, setLive } = setup(snapshot([]));
		await run(inspect);
		const reply = { ...inspect, kind: "reply", token: "token", text: "Please summarize.", clientMessageId: "watch-draft-1" };
		await run(reply); await run(reply);
		expect(deps.message).toHaveBeenNthCalledWith(2, cfg, "worker", { text: reply.text, clientMessageId: reply.clientMessageId });
		setLive(snapshot());
		expect(await run(reply)).toMatchObject({ status: "open_phone" });
		expect(deps.message).toHaveBeenCalledTimes(2);
	});
	it("routes structured input to its request endpoint, preserving field/length validation", async () => {
		const input = approval({ activityKind: "user_input", detail: { inputMode: "form", url: "", message: "What should I check?", schema: { type: "object", properties: { answer: { type: "string", minLength: 3, maxLength: 40 } }, required: ["answer"] } } });
		const { run, deps } = setup(snapshot([input])); await run(inspect);
		const reply = { ...inspect, kind: "reply", token: "token", text: "The tests, please.", clientMessageId: "watch-draft-2" };
		expect(await run(reply)).toMatchObject({ status: "sent" });
		expect(deps.input).toHaveBeenCalledWith(cfg, "worker", "request-1", "accept", { answer: reply.text });
		expect(await run({ ...reply, text: "x" })).toMatchObject({ status: "error" });
		expect(deps.message).not.toHaveBeenCalled();
	});
	it("rejects malformed payloads, credentials changes and foreign conversation identity", async () => {
		const { run, deps, setLive } = setup();
		for (const data of [null, [], {}, { ...inspect, kind: "merge" }]) expect(await run(data)).toMatchObject({ status: "open_phone" });
		setLive(snapshot([], { sessionId: "different" }));
		expect(await run(inspect)).toMatchObject({ status: "open_phone" });
		deps.configForHost.mockReturnValue({ ...cfg, hostId: "beta" });
		expect(await run(inspect)).toMatchObject({ status: "open_phone" });
	});
});
