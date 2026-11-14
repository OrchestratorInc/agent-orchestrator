import { describe, expect, it } from "vitest";
import { providerSwitchLabel, readProviderSwitch, switchSideExtras, switchSideName } from "./providerSwitchModel";

const marker = { event: "provider_switch", sourceHarness: "claude-code", targetHarness: "codex", sourceModel: "opus", targetModel: "gpt-6-sol", targetEffort: "high", targetAutoCompactWindow: "1m" };

describe("readProviderSwitch", () => {
	it("reads both sides of the daemon's marker", () => {
		expect(readProviderSwitch(marker)).toEqual({
			source: { harness: "claude-code", model: "opus" },
			target: { harness: "codex", model: "gpt-6-sol", effort: "high", compactWindow: "1m" },
		});
	});

	it("draws the plain line for a switch recorded without harness names", () => {
		expect(readProviderSwitch({ event: "provider_switch" })).toBeUndefined();
		expect(readProviderSwitch({ event: "compaction", sourceHarness: "a", targetHarness: "b" })).toBeUndefined();
		expect(providerSwitchLabel(undefined)).toBe("Switched");
	});
});

describe("switchSideName", () => {
	it("names the model from the agent's catalog, as the menus do", () => {
		expect(switchSideName({ harness: "codex", model: "gpt-6-sol" }, [{ id: "gpt-6-sol", label: "GPT-6-Sol" }])).toBe("GPT-6-Sol");
		expect(switchSideName({ harness: "claude-code", model: "opus" }, [{ id: "claude-opus-5-5-x", label: "Claude Opus 5.5" }])).toBe("Opus 5.5");
	});

	it("falls back to the id, then to the agent's name when no model was recorded", () => {
		expect(switchSideName({ harness: "codex", model: "gpt-6-sol[high]" })).toBe("gpt-6-sol");
		expect(switchSideName({ harness: "codex" })).toBe("Codex");
		expect(switchSideName({ harness: "claude-code", model: "opus[1m]" }, [{ id: "claude-opus-5-5-x", label: "Claude Opus 5.5 1M" }])).toBe("Opus 5.5");
	});
});

it("puts the target's effort and auto-compact window after its name", () => {
	expect(switchSideExtras({ harness: "codex", effort: "high", compactWindow: "1m" })).toEqual(["High", "· 1M"]);
	expect(providerSwitchLabel(readProviderSwitch(marker))).toBe("Switched from Claude Code to Codex");
});
