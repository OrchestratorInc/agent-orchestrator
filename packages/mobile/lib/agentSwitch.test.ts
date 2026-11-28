import { describe, expect, it } from "vitest";
import { rankAgents } from "./agentPicker";
import { AUTOMATIC_MODEL, agentSwitchTargets, pendingSwitchFor, repickedSwitch, requestedSwitchModel, switchOffersFast } from "./agentSwitch";

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

describe("switchOffersFast", () => {
	const fast = { codex: { models: ["gpt-5.5"], byDefault: true }, cursor: { models: [], byDefault: false } };

	it("offers Fast for a picked model with a fast tier, and for the default when it has one", () => {
		expect(switchOffersFast({ harness: "codex", model: "gpt-5.5", modelLabel: "GPT-5.5" }, fast)).toBe(true);
		expect(switchOffersFast({ harness: "codex", model: AUTOMATIC_MODEL, modelLabel: "Default" }, fast)).toBe(true);
	});

	it("offers none for a model without one, an agent without any, or before the catalog arrives", () => {
		expect(switchOffersFast({ harness: "codex", model: "gpt-5.1-mini", modelLabel: "mini" }, fast)).toBe(false);
		expect(switchOffersFast({ harness: "cursor", modelLabel: "Default" }, fast)).toBe(false);
		expect(switchOffersFast({ harness: "codex", model: "gpt-5.5", modelLabel: "GPT-5.5" }, undefined)).toBe(false);
	});
});

describe("repickedSwitch", () => {
	const fast = { codex: { models: ["gpt-5.5", "gpt-5.5-codex"], byDefault: true } };
	const codex = (model: string, more: object = {}) => ({ harness: "codex", model, modelLabel: model, ...more });

	// Desktop's otherAgentSource keeps Fast for a model that also has it.
	it("keeps Fast mode on another model of the same agent that has it, and drops it on one that does not", () => {
		const held = codex("gpt-5.5", { fastMode: true });
		expect(repickedSwitch(codex("gpt-5.5-codex"), held, fast)).toEqual(codex("gpt-5.5-codex", { fastMode: true }));
		expect(repickedSwitch({ harness: "codex", modelLabel: "Default" }, held, fast)).toEqual({ harness: "codex", modelLabel: "Default", fastMode: true });
		expect(repickedSwitch(codex("gpt-5.1-mini"), held, fast)).toStrictEqual(codex("gpt-5.1-mini"));
		// Not turned on, it stays off.
		expect(repickedSwitch(codex("gpt-5.5-codex"), codex("gpt-5.5"), fast)).toStrictEqual(codex("gpt-5.5-codex"));
	});

	it("keeps Ultracode on another Claude Code model", () => {
		const held = { harness: "claude-code", model: "opus", modelLabel: "Opus", ultracode: true };
		expect(repickedSwitch({ harness: "claude-code", model: "haiku", modelLabel: "Haiku" }, held, fast)).toEqual({ harness: "claude-code", model: "haiku", modelLabel: "Haiku", ultracode: true });
	});

	it("starts a pick of another agent, or the first pick, with neither", () => {
		const held = { harness: "claude-code", model: "opus", modelLabel: "Opus", ultracode: true, fastMode: true };
		expect(repickedSwitch(codex("gpt-5.5"), held, fast)).toStrictEqual(codex("gpt-5.5"));
		expect(repickedSwitch(codex("gpt-5.5"), undefined, fast)).toStrictEqual(codex("gpt-5.5"));
	});
});
