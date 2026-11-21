import { describe, expect, it } from "vitest";
import { rankAgents } from "./agentPicker";
import { AUTOMATIC_MODEL, agentSwitchTargets, pendingSwitchFor, requestedSwitchModel } from "./agentSwitch";

const installed = (ids: string[]) => ids.map((id) => ({ id, label: id === "claude-code" ? "Claude Code" : id === "codex" ? "Codex" : id, authStatus: "authorized" as const }));

describe("agentSwitchTargets", () => {
	const agents = rankAgents({
		supported: installed(["claude-code", "codex", "fx", "cursor", "opencode"]),
		installed: [...installed(["claude-code", "codex", "fx", "cursor"]), { id: "opencode", label: "opencode", authStatus: "unauthorized" }],
		authorized: [],
	});
	const chat = ["claude-code", "codex", "cursor", "opencode"];

	it("lists the usable Chat agents other than the running one", () => {
		expect(agentSwitchTargets(agents, "claude-code", chat)).toEqual([
			{ id: "codex", label: "Codex" },
			{ id: "cursor", label: "cursor" },
		]);
	});

	it("leaves out agents without a Chat driver and ones that need signing in", () => {
		const ids = agentSwitchTargets(agents, "codex", chat).map((agent) => agent.id);
		expect(ids).not.toContain("fx");
		expect(ids).not.toContain("opencode");
		expect(ids).toContain("claude-code");
	});
});

describe("requestedSwitchModel", () => {
	it("sends no model for the default row", () => {
		expect(requestedSwitchModel(AUTOMATIC_MODEL)).toBeUndefined();
		expect(requestedSwitchModel(undefined)).toBeUndefined();
		expect(requestedSwitchModel(" gpt-5.5 ")).toBe("gpt-5.5");
	});
});

describe("pendingSwitchFor", () => {
	const pick = { harness: "codex", model: "gpt-5.5", modelLabel: "GPT-5.5" };

	it("keeps a pick for another agent until the chat runs it", () => {
		expect(pendingSwitchFor(pick, "claude-code")).toBe(pick);
		expect(pendingSwitchFor(pick, "codex")).toBeUndefined();
	});

	it("holds nothing before the chat reports its agent", () => {
		expect(pendingSwitchFor(pick, undefined)).toBeUndefined();
		expect(pendingSwitchFor(undefined, "claude-code")).toBeUndefined();
	});
});
