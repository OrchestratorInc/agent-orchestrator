import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("./CloudTerminalSessionScreen.tsx", import.meta.url), "utf8");

describe("paused Cloud terminal", () => {
	it("refuses a terminal connection when the routed Cloud organization changed", () => {
		expect(source).toContain('orgId !== source.id || !sessionSource');
		expect(source).toContain('source: source.kind, sourceId: source.id');
	});
	it("shows the paused stage and an explicit resume action", () => {
		expect(source).toContain("stageLabel(stage!)");
		expect(source).toContain("sessionSource.resumeSession(session.id)");
	});
});
