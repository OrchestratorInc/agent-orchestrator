import { describe, expect, it } from "vitest";
import { approvalSymbol } from "./approvalSymbol";

describe("approvalSymbol", () => {
	it("draws each mode from asking first to asking for nothing", () => {
		expect(approvalSymbol("default", "claude-code")).toBe("hand.raised");
		expect(approvalSymbol("accept-edits")).toBe("pencil");
		expect(approvalSymbol("auto")).toBe("checkmark.shield");
		expect(approvalSymbol("bypass-permissions")).toBe("exclamationmark.shield");
	});

	// Codex's default is full access, so it reads as never asking.
	it("draws Codex's default as never asking", () => {
		expect(approvalSymbol("default", "codex")).toBe("exclamationmark.shield");
	});

	it("falls back to a plain shield for an agent tier that maps to no mode", () => {
		expect(approvalSymbol(undefined)).toBe("shield");
	});
});
