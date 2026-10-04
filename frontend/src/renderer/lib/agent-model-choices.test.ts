import { describe, expect, it } from "vitest";
import { supportsModelEffortAtLaunch } from "./agent-model-choices";

describe("supportsModelEffortAtLaunch", () => {
	it.each(["codex", "claude-code"])("supports %s in chat and terminal mode", (agent) => {
		expect(supportsModelEffortAtLaunch(agent, "chat", [])).toBe(true);
		expect(supportsModelEffortAtLaunch(agent, "tui", [])).toBe(true);
	});

	it.each(["pi", "opencode", "opencode-v2", "deepseek-harness"])("requires actual native chat support for %s", (agent) => {
		expect(supportsModelEffortAtLaunch(agent, "chat", [agent])).toBe(true);
		expect(supportsModelEffortAtLaunch(agent, "chat", [])).toBe(false);
		expect(supportsModelEffortAtLaunch(agent, "tui", [agent])).toBe(false);
		expect(supportsModelEffortAtLaunch(agent, undefined, [agent])).toBe(false);
	});

	it("supports Unreal's forced native chat posture", () => {
		expect(supportsModelEffortAtLaunch("unreal-agent", "tui", [])).toBe(true);
	});

	it.each(["cursor", "cline", "unknown"])("does not invent a launch setter for %s", (agent) => {
		expect(supportsModelEffortAtLaunch(agent, "chat", [agent])).toBe(false);
	});
});
