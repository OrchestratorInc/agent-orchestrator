import { describe, expect, it } from "vitest";
import { approvalSymbol } from "./approvalSymbol";

describe("approvalSymbol", () => {
	// Codex's default is full access, so it reads as never asking; an agent
	// tier that maps to no mode falls back to a plain shield.
	it("draws each mode from asking first to asking for nothing", () => {
		expect(approvalSymbol("default", "claude-code")).toBe("hand.raised");
		expect(approvalSymbol("auto")).toBe("checkmark.shield");
		expect(approvalSymbol("bypass-permissions")).toBe("exclamationmark.shield");
		expect(approvalSymbol("default", "codex")).toBe("exclamationmark.shield");
		expect(approvalSymbol(undefined)).toBe("shield");
	});
});
