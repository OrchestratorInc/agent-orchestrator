import { describe, expect, it, vi } from "vitest";
import { isEmptyTurnOptionCatalog, loadTurnOptionCatalog, providerCatalogLive, turnOptionsLoadFor } from "./turnOptionsCatalog";
import type { ControllerState } from "./types";

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

	/** Each snapshot state a screen renders with, in order, to the catalog loads it makes. */
	function loadsFor(states: Array<ControllerState | undefined>, sessionId = "chat") {
		let previous: string | undefined;
		const loads: Array<"load" | "refresh"> = [];
		for (const state of states) {
			const load = turnOptionsLoadFor(previous, sessionId, state && providerCatalogLive({ controller: { state } }));
			if (!load) continue;
			previous = load.key;
			loads.push(load.refresh ? "refresh" : "load");
		}
		return loads;
	}

	it("loads once for a chat whose first snapshot is already live", () => {
		expect(loadsFor([undefined, "ready", "ready", "busy", "ready"])).toEqual(["load"]);
	});

	it("loads again past the cache when a hibernated chat wakes", () => {
		expect(loadsFor(["hibernated", "hibernated", "ready", "ready"])).toEqual(["load", "refresh"]);
		expect(loadsFor(["connecting", "busy"])).toEqual(["load", "refresh"]);
	});

	it("follows a provider that drops out while busy and comes back", () => {
		expect(loadsFor(["busy", "recovering", "ready"])).toEqual(["load", "load", "refresh"]);
	});

	it("waits for the first snapshot and starts over for another chat", () => {
		expect(loadsFor([undefined, undefined])).toEqual([]);
		const first = turnOptionsLoadFor(undefined, "a", true);
		expect(turnOptionsLoadFor(first?.key, "b", true)).toEqual({ key: "b:true", refresh: false });
		expect(turnOptionsLoadFor("a:false", "b", true)).toEqual({ key: "b:true", refresh: false });
	});

	it("treats only a running provider as able to answer for its catalog", () => {
		expect(providerCatalogLive({ controller: { state: "ready" } })).toBe(true);
		expect(providerCatalogLive({ controller: { state: "busy" } })).toBe(true);
		for (const state of ["hibernated", "stopped", "connecting", "recovering"] as const) {
			expect(providerCatalogLive({ controller: { state } })).toBe(false);
		}
	});
});
