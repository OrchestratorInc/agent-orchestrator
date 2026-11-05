import { describe, expect, it } from "vitest";
import { cloudCatalogOffers, cloudModelVerdict } from "./cloud-model-catalog";

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
