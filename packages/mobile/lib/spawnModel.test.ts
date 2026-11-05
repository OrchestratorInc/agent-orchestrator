import { describe, expect, it } from "vitest";
import { modelOverride, resolveSpawnAgent, spawnModelSourceChanged } from "./spawnModel";

describe("spawn agent resolution", () => {
	it("seeds from the first selectable agent in the ranked default order", () => {
		expect(resolveSpawnAgent(["claude-code", "codex", "cursor", "opencode"])).toBe("claude-code");
		expect(resolveSpawnAgent(["codex", "cursor"])).toBe("codex");
		expect(resolveSpawnAgent(["cursor", "opencode"])).toBe("cursor");
	});

	it("is empty when no agent is selectable", () => {
		expect(resolveSpawnAgent([])).toBe("");
	});
});

describe("spawn model", () => {
	// Sending the pick even when it matches the catalog default is the point:
	// the old rule dropped it, the session stored no model, and the chat then had
	// nothing to resolve and showed whatever the thread was running.
	it("sends a touched value, including one the catalog marks as default", () => {
		expect(modelOverride("opus", true)).toBe("opus");
		expect(modelOverride("sonnet", true)).toBe("sonnet");
		expect(modelOverride("  opus  ", true)).toBe("opus");
	});

	it("sends nothing for the untouched Automatic state", () => {
		expect(modelOverride("opus", false)).toBeUndefined();
		expect(modelOverride("", true)).toBeUndefined();
		expect(modelOverride("__auto__", true)).toBeUndefined();
	});

	it("reloads the model catalog only when its project or agent source changes", () => {
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-1", agentId: "codex" },
		)).toBe(false);
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-2", agentId: "codex" },
		)).toBe(true);
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-1", agentId: "claude-code" },
		)).toBe(true);
	});
});
