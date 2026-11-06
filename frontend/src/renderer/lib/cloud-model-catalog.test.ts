import { describe, expect, it } from "vitest";
import { claudeModelName, cloudCatalogOffers, cloudModelVerdict } from "./cloud-model-catalog";

// The same cases as the control plane's modelcatalog TestOffersMatchesWhatTheHarnessWouldRun,
// so the desktop and the server agree on what a list offers.
describe("cloudCatalogOffers", () => {
	const claude = [{ id: "default" }, { id: "opus" }, { id: "sonnet" }, { id: "claude-opus-5" }, { id: "claude-opus-4-5-20251101" }];
	const codex = [{ id: "gpt-6-luna" }, { id: "gpt-5.6-terra" }];
	it.each([
		["the harness default", claude, "", true],
		["a listed alias", claude, "opus", true],
		["a listed full ID", claude, "claude-opus-5", true],
		["another case", claude, "Claude-Opus-5", true],
		["a bracketed variant of a listed model", claude, "sonnet[1m]", true],
		["a dated release named without its date", claude, "claude-opus-4-5", true],
		["a model the account does not offer", claude, "claude-opus-5-5", false],
		["a newer model sharing a prefix", claude, "claude-opus-5-1", false],
		["not a date suffix", claude, "claude-opus-4", false],
		["another harness, listed", codex, "gpt-5.6-terra", true],
		["another harness, not listed", codex, "gpt-6.1-sol", false],
		["an empty list offers only the default", [], "opus", false],
	] as const)("%s", (_name, models, id, want) => {
		expect(cloudCatalogOffers(models, id)).toBe(want);
	});
});

// The control plane's TestOffersOnTheRealStagingLists: the lists a staging
// account's workers reported (Claude Code 2.1.287 and Codex 0.165.0).
describe("cloudCatalogOffers on the real staging lists", () => {
	const newer = (id: string, alias: string) => ({ id, description: `Newer version available · select ${alias}` });
	const claude = [
		{ id: "default", description: "Sonnet" },
		{ id: "sonnet", description: "Sonnet 5.5 · Efficient for routine tasks" },
		{ id: "fable", description: "Fable 5.1 · Most capable for your hardest and longest-running tasks" },
		{ id: "opus", description: "Opus 5.5 · Best for everyday, complex tasks" },
		{ id: "haiku", description: "Haiku 4.5 · Fastest for quick answers" },
		newer("claude-opus-5", "Opus for Opus 5.5"),
		newer("claude-sonnet-5", "Sonnet for Sonnet 5.5"),
		newer("claude-opus-4-8", "Opus for Opus 5.5"),
		newer("claude-opus-4-7", "Opus for Opus 5.5"),
		newer("claude-sonnet-4-6", "Sonnet for Sonnet 5.5"),
		newer("claude-opus-4-6", "Opus for Opus 5.5"),
		newer("claude-opus-4-5-20251101", "Opus for Opus 5.5"),
		newer("claude-sonnet-4-5-20250929", "Sonnet for Sonnet 5.5"),
	];
	const codex = [{ id: "gpt-6-luna" }, { id: "gpt-5.6-terra" }, { id: "gpt-5.6-luna" }];
	it.each([
		[claude, "", true], [claude, "default", true], [claude, "opus", true], [claude, "sonnet[1m]", true],
		[claude, "opusplan", true], [claude, "claude-opus-5-5", true], [claude, "claude-opus-5-5[1m]", true],
		[claude, "Claude-Opus-5-5", true], [claude, "claude-sonnet-5-5", true], [claude, "claude-fable-5-1", true],
		[claude, "claude-haiku-4-5-20251001", true], [claude, "claude-opus-4-6-20260101", true],
		[claude, "claude-opus-4-5", true], [claude, "claude-opus-4-5-20251101", true],
		[claude, "claude-not-a-model-9", false], [claude, "claude-opus-6-0", false], [claude, "claude-mythos-1", false],
		[claude, "claude-opus-5-1", false], [claude, "gpt-6-luna", false],
		[codex, "gpt-6-luna", true], [codex, "gpt-5.6-terra", true], [codex, "gpt-5.6-luna", true],
		[codex, "gpt-6.1-sol", false], [codex, "claude-opus-5-5", false],
		[[{ id: "sonnet", description: "Sonnet 5.5 · Efficient" }], "opusplan", false],
	] as const)("%#: %s offers %s: %s", (models, id, want) => {
		expect(cloudCatalogOffers(models, id)).toBe(want);
	});

	it.each([
		["claude-opus-5-5", "Opus 5.5"], ["claude-opus-5", "Opus 5"], ["claude-haiku-4-5-20251001", "Haiku 4.5"],
		["claude-3-7-sonnet-20250219", "Sonnet 3.7"], ["CLAUDE-FABLE-5-1", "Fable 5.1"],
		["claude-not-a-model-9", undefined], ["claude-opus", undefined], ["claude-", undefined], ["opus", undefined], ["gpt-5.6-terra", undefined],
	] as const)("claudeModelName(%s) = %s", (id, want) => {
		expect(claudeModelName(id)).toBe(want);
	});
});

describe("cloudModelVerdict", () => {
	const listed = [{ id: "opus", displayName: "Opus", default: true }];
	it("only the account's own list says a model is unavailable", () => {
		expect(cloudModelVerdict({ source: "worker", models: listed }, "claude-opus-5-5")).toBe("unavailable");
		expect(cloudModelVerdict({ source: "release", models: listed }, "claude-opus-5-5")).toBe("unverified");
		expect(cloudModelVerdict({ source: "worker", models: listed }, "opus")).toBe("offered");
		expect(cloudModelVerdict(undefined, "claude-opus-5-5")).toBe("offered");
		// The agent's own default ("" or "default") is always offered.
		expect(cloudModelVerdict({ source: "worker", models: listed }, "")).toBe("offered");
	});
});
