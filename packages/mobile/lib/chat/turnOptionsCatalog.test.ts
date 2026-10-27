import { describe, expect, it, vi } from "vitest";
import { isEmptyTurnOptionCatalog, loadTurnOptionCatalog, providerCatalogLive } from "./turnOptionsCatalog";

describe("turn option catalog", () => {
	it("uses the provider's controls when it has any, without asking for AO's model list", async () => {
		const loadModels = vi.fn().mockResolvedValue([]);
		const loadConfigOptions = vi.fn().mockResolvedValue([{ id: "model" }]);
		expect(await loadTurnOptionCatalog({ loadModels, loadConfigOptions }))
			.toEqual({ models: [], configOptions: [{ id: "model" }] });
		expect(loadModels).not.toHaveBeenCalled();
	});

	it("falls back to AO's model list for a driver with no provider controls", async () => {
		const loadModels = vi.fn().mockResolvedValue([{ id: "gpt" }]);
		const loadConfigOptions = vi.fn().mockResolvedValue([]);
		expect(await loadTurnOptionCatalog({ loadModels, loadConfigOptions }))
			.toEqual({ models: [{ id: "gpt" }], configOptions: [] });
	});

	it("passes a failure on rather than reading an unavailable provider as having no controls", async () => {
		const loadModels = vi.fn().mockResolvedValue([]);
		const loadConfigOptions = vi.fn().mockRejectedValue(new Error("CHAT_CONTROLLER_NOT_READY"));
		await expect(loadTurnOptionCatalog({ loadModels, loadConfigOptions })).rejects.toThrow("CHAT_CONTROLLER_NOT_READY");
		expect(loadModels).not.toHaveBeenCalled();
	});

	it("names an empty catalog", () => {
		expect(isEmptyTurnOptionCatalog({ models: [], configOptions: [] })).toBe(true);
		expect(isEmptyTurnOptionCatalog({ models: [{ id: "gpt" }], configOptions: [] })).toBe(false);
		expect(isEmptyTurnOptionCatalog({ models: [], configOptions: [{ id: "model" }] })).toBe(false);
	});

	it("treats only a running provider as able to answer for its catalog", () => {
		expect(providerCatalogLive({ controller: { state: "ready" } })).toBe(true);
		expect(providerCatalogLive({ controller: { state: "busy" } })).toBe(true);
		for (const state of ["hibernated", "stopped", "connecting", "recovering"] as const) {
			expect(providerCatalogLive({ controller: { state } })).toBe(false);
		}
	});
});
