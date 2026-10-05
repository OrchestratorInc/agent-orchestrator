import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("./CloudTerminalSessionScreen.tsx", import.meta.url), "utf8");

describe("paused Cloud terminal", () => {
	it("refuses a terminal connection when the routed Cloud organization changed", () => {
		expect(source).toContain('orgId !== source.id || !sessionSource');
		expect(source).toContain('source: source.kind, sourceId: source.id');
	});
	it("does not connect the terminal while Coder has paused the sandbox", () => {
		expect(source).toContain('const pausedByCoder = cloudLifecycleStage(session) === "paused_by_coder"');
		expect(source).toContain("if (pausedByCoder) return;");
	});

	it("shows the paused stage and an explicit resume action", () => {
		expect(source).toContain("Paused by Coder");
		expect(source).toContain("sessionSource.resumeSession(session.id)");
	});
});
