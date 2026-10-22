import { describe, expect, it } from "vitest";
import { conversationActionError, conversationActionUnsupported, editRefusedDefinitively, conversationErrorCode, conversationErrorIsPermanent, ignoreStaleApproval } from "./conversationErrors";

describe("mobile conversation action errors", () => {
	it("keeps reviewer startup errors retryable", () => {
		expect(conversationErrorIsPermanent("CHAT_CONTROLLER_NOT_READY")).toBe(true);
		expect(conversationErrorIsPermanent("CHAT_CONTROLLER_NOT_READY", true)).toBe(false);
		expect(conversationErrorIsPermanent("CHAT_DRIVER_UNAVAILABLE", true)).toBe(true);
	});
	it("turns protocol codes into instructions the user can act on", () => {
		expect(conversationActionError(Object.assign(new Error("conflict"), { code: "CHAT_NO_ACTIVE_TURN" })))
			.toContain("Queue it as a new message");
		expect(conversationActionError(Object.assign(new Error("busy"), { code: "CHAT_COMPACTION_BUSY" })))
			.toBe("Stop the current turn before compacting history.");
		expect(conversationActionError(Object.assign(new Error("nope"), { code: "CHAT_MCP_RELOAD_UNSUPPORTED" })))
			.toBe("This agent cannot reload its MCP servers.");
		expect(conversationActionError(Object.assign(new Error("nope"), { code: "CHAT_STEER_UNSUPPORTED" })))
			.toContain("Queue a new message");
		expect(conversationActionError(Object.assign(new Error("conflict"), { code: "CHAT_TURN_RUNNING" })))
			.toContain("Stop the current turn");
		expect(conversationActionError(Object.assign(new Error("conflict"), { code: "CHAT_REQUEST_NOT_PENDING" })))
			.toContain("already answered");
	});

	it("preserves typed refusal identities so unsupported controls can withdraw", () => {
		const error = Object.assign(new Error("conflict"), { code: "CHAT_STEER_UNSUPPORTED" });
		expect(conversationErrorCode(error)).toBe("CHAT_STEER_UNSUPPORTED");
		expect(conversationActionUnsupported("steer", conversationErrorCode(error))).toBe(true);
		expect(conversationActionUnsupported("compact", conversationErrorCode(error))).toBe(false);
	});

	it("treats only the losing concurrent approval response as settled", async () => {
		const stale = Object.assign(new Error("already answered"), { status: 409, code: "CHAT_REQUEST_NOT_PENDING" });
		await expect(ignoreStaleApproval(async () => { throw stale; })).resolves.toBeUndefined();
		const refusal = Object.assign(new Error("not offered"), { status: 400, code: "CHAT_DECISION_NOT_OFFERED" });
		await expect(ignoreStaleApproval(async () => { throw refusal; })).rejects.toBe(refusal);
	});

	it("tells an uncertain edit apart from a refused one", () => {
		expect(conversationActionError(Object.assign(new Error("conflict"), { code: "CHAT_EDIT_BUSY" })))
			.toContain("Stop the current turn");
		expect(conversationActionError(Object.assign(new Error("conflict"), { code: "CHAT_EDIT_UNCERTAIN" })))
			.toContain("will not be sent twice");
		expect(conversationActionError(Object.assign(new Error("conflict"), { code: "CHAT_EDIT_IDEMPOTENCY_CONFLICT" })))
			.toBe("That send was tied to a different edit. Send again to retry.");
		expect(editRefusedDefinitively("CHAT_EDIT_IDEMPOTENCY_CONFLICT")).toBe(true);
		expect(editRefusedDefinitively("CHAT_EDIT_REJECTED")).toBe(true);
		expect(editRefusedDefinitively("CHAT_EDIT_UNCERTAIN")).toBe(false);
		expect(editRefusedDefinitively(undefined)).toBe(false);
	});
});

describe("message edit failures", () => {
	it("stay in the inline editor instead of also raising the screen banner", async () => {
		const { readFileSync } = await import("node:fs");
		const source = readFileSync(new URL("./useConversation.ts", import.meta.url), "utf8");
		expect(source).toContain("if (banner) setActionError(message);");
		expect(source).toContain('runAction("edit", () => requireConfig(cfg, (c) => editConversationMessage(c, sessionId, turnId, text, clientMessageId)), true, false)');
	});
});
