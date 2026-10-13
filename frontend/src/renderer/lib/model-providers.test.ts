import { describe, expect, it } from "vitest";
import { groupModelProviders } from "./model-providers";

describe("groupModelProviders", () => {
	it("keeps native selectors and groups explicit providers, including unconfigured and unknown ones", () => {
		const models = [
			{ id: "router/org/model", label: "Model", provider: "router" },
			{ id: "local/model", label: "Local", provider: "local" },
			{ id: "org/unknown", label: "Unknown" },
		];
		const groups = groupModelProviders(models, ["router", "not-in-catalog"]);
		expect(groups.map(({ id, configured }) => ({ id, configured }))).toEqual([
			{ id: "not-in-catalog", configured: true }, { id: "router", configured: true }, { id: "local", configured: false }, { id: "", configured: false },
		]);
		expect(groups.flatMap((group) => group.models)).toEqual(models);
	});

	it("retains reported providers without models or credentials", () => {
		expect(groupModelProviders([], ["configured"], ["unconfigured", "local", "configured"])).toEqual([
			{ id: "configured", configured: true, models: [] },
			{ id: "local", configured: false, models: [] },
			{ id: "unconfigured", configured: false, models: [] },
		]);
	});

	it("keeps a catalog with no provider metadata usable without guessing from IDs", () => {
		expect(groupModelProviders([{ id: "org/model", label: "Model" }])).toEqual([
			{ id: "", configured: false, models: [{ id: "org/model", label: "Model" }] },
		]);
		expect(groupModelProviders([])).toEqual([]);
	});
});
