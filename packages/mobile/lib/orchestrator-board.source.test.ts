import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Wiring fences for "talk to the project orchestrator from the board": the
// behaviors that make the orchestrator row a real chat entry point rather than
// decoration. Source assertions, the same split the chat screens use — the
// components need a renderer and a daemon to test end to end, and these
// invariants are about which wire is connected to which.
const apiSource = readFileSync(new URL("./api.ts", import.meta.url), "utf8");
const sessionRouteSource = readFileSync(new URL("../app/session/[id].tsx", import.meta.url), "utf8");

describe("orchestrator board wiring", () => {
	it("lists the live per-project orchestrator among the board's sessions", () => {
		expect(apiSource).toContain('filter((s) => !s.isTerminated).map(mapSession)');
		expect(apiSource).toContain('s.kind !== "orchestrator"');
	});

	it("keeps mobile spawning workers only — the phone talks to the existing orchestrator, it does not spawn one", () => {
		expect(apiSource).toContain('kind: "worker"');
		expect(apiSource).not.toContain('kind: "orchestrator"');
	});

	it("resolves the chat route as an orchestrator first, so the chat keeps its orchestrator guards", () => {
		expect(sessionRouteSource).toContain("orchestrators.find((item) => item.id === id) ?? sessions.find((item) => item.id === id)");
	});
});
