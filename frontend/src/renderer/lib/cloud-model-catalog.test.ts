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
		// Matching errs towards keeping a model: a mis-cased ID is kept.
		["another harness, another case", codex, "GPT-5.6-Terra", true],
		// The effort is kept apart from the model, so a suffixed ID is not a listed one.
		["a suffixed form of a listed model", codex, "gpt-5.6-terra/high", false],
		["a provider-qualified ID, listed", [{ id: "opencode-go/model-1" }], "opencode-go/model-1", true],
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

// The control plane's TestOffersResolvesAnAliasOnlyThroughTheProvidersModel.
describe("cloudCatalogOffers on a list carrying the provider's models", () => {
	const providers: Record<string, string> = {
		sonnet: "claude-sonnet-5-5", fable: "claude-fable-5-1", opus: "claude-opus-5-5", haiku: "claude-haiku-4-5-20251001",
	};
	const staging = [
		{ id: "default", description: "Sonnet" },
		{ id: "sonnet", description: "Sonnet 5.5 · Efficient for routine tasks" },
		{ id: "fable", description: "Fable 5.1 · Most capable for your hardest and longest-running tasks" },
		{ id: "opus", description: "Opus 5.5 · Best for everyday, complex tasks" },
		{ id: "haiku", description: "Haiku 4.5 · Fastest for quick answers" },
		{ id: "claude-opus-5", description: "Newer version available · select Opus for Opus 5.5" },
		{ id: "claude-opus-4-6", description: "Newer version available · select Opus for Opus 5.5" },
		{ id: "claude-opus-4-5-20251101", description: "Newer version available · select Opus for Opus 5.5" },
	];
	const withProviders = (...unconfirmed: string[]) => staging.map((model) => {
		if (providers[model.id] && !unconfirmed.includes(model.id)) return { ...model, providerId: providers[model.id] };
		return model.id.startsWith("claude-") ? { ...model, providerId: model.id } : model;
	});
	const confirmed = withProviders();
	const noOpus = withProviders("opus");
	it.each([
		["confirmed alias", confirmed, "claude-opus-5-5", true],
		["confirmed alias, bracketed", confirmed, "claude-opus-5-5[1m]", true],
		["confirmed alias, dated request", confirmed, "claude-opus-5-5-20260101", true],
		["confirmed dated provider model, undated request", confirmed, "claude-haiku-4-5", true],
		["confirmed dated provider model, same date", confirmed, "claude-haiku-4-5-20251001", true],
		["confirmed alias, another family", confirmed, "claude-sonnet-5-5", true],
		["listed ID", confirmed, "claude-opus-4-6", true],
		["not listed or confirmed", confirmed, "claude-opus-6-0", false],
		["prod case: described but unconfirmed", noOpus, "claude-opus-5-5", false],
		["prod case, bracketed", noOpus, "claude-opus-5-5[1m]", false],
		["prod case, dated request", noOpus, "claude-opus-5-5-20260101", false],
		["prod case, another confirmed alias", noOpus, "claude-sonnet-5-5", true],
		["prod case, the alias itself is listed by ID", noOpus, "opus", true],
		["prod case, opusplan through the listed opus", noOpus, "opusplan", true],
		["prod case, an older listed ID", noOpus, "claude-opus-4-6-20260101", true],
		["legacy list", staging, "claude-opus-5-5", true],
		["legacy list, unknown model", staging, "claude-opus-6-0", false],
	] as const)("%s: %s", (_name, models, id, want) => {
		expect(cloudCatalogOffers(models, id)).toBe(want);
	});

	it("says the prod case's model is unavailable on the account's own list", () => {
		const models = noOpus.map((model) => ({ ...model, displayName: model.id, default: model.id === "default" }));
		expect(cloudModelVerdict({ source: "worker", models }, "claude-opus-5-5")).toBe("unavailable");
		expect(cloudModelVerdict({ source: "worker", models: confirmed.map((model) => ({ ...model, displayName: model.id, default: false })) }, "claude-opus-5-5")).toBe("offered");
	});
});

describe("cloudModelVerdict", () => {
	const listed = [{ id: "opus", displayName: "Opus", default: true }];
	it("only the account's own list says a model is unavailable", () => {
		expect(cloudModelVerdict({ source: "worker", models: listed }, "claude-opus-5-5")).toBe("unavailable");
		expect(cloudModelVerdict({ source: "release", models: listed }, "claude-opus-5-5")).toBe("unverified");
		expect(cloudModelVerdict({ source: "worker", models: listed }, "opus")).toBe("offered");
		expect(cloudModelVerdict(undefined, "claude-opus-5-5")).toBe("offered");
		// The agent's own default (no model) is always offered.
		expect(cloudModelVerdict({ source: "worker", models: listed }, "")).toBe("offered");
	});

	it("treats the literal default as the control plane does: offered only by a list that names it", () => {
		// Claude Code lists "default"; Codex does not, and Cloud refuses it there.
		expect(cloudModelVerdict({ source: "worker", models: [...listed, { id: "default", displayName: "Default", default: false }] }, "default")).toBe("offered");
		expect(cloudModelVerdict({ source: "worker", models: [{ id: "gpt-6-luna", displayName: "Luna", default: true }] }, "default")).toBe("unavailable");
		expect(cloudModelVerdict({ source: "release", models: [{ id: "gpt-6-luna", displayName: "Luna", default: true }] }, "default")).toBe("unverified");
	});
});
