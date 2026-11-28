import { describe, expect, it } from "vitest";
import { cloudTurnFailureMessage } from "../../../../packages/shared/chat/cloud-turn-failure";

describe("a Cloud turn's failure", () => {
	it("keeps the agent's words from a failed ACP prompt, without its envelope or stderr", () => {
		const raw = 'ACP prompt: {"code":-32603,"message":"Internal error: model \\"x\\" not found {see docs}","data":{"errorKind":"invalid_request"}}: [session/query] sessionId=1\n(node:1) Warning: noise';
		expect(cloudTurnFailureMessage(raw)).toBe('model "x" not found {see docs}');
	});

	it("falls back to the error's details when its message is only the generic lead", () => {
		expect(cloudTurnFailureMessage('ACP prompt: {"code":-32603,"message":"Internal error","data":{"details":"Context too long"}}: ')).toBe("Context too long");
	});

	it("keeps an exited agent's stderr, less the runtime's warnings", () => {
		expect(cloudTurnFailureMessage("ACP prompt: peer disconnected: Error: spawn claude ENOENT\n(node:7) Warning: x\n(Use `node --trace-warnings ...` to show where)\n"))
			.toBe("peer disconnected: Error: spawn claude ENOENT");
	});

	it("leaves any other failure as written", () => {
		expect(cloudTurnFailureMessage("Codex turn failed: boom")).toBe("Codex turn failed: boom");
	});
});
