import { beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_UI_SETTINGS } from "../../shared/ui-locale";

const bridge = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));
vi.mock("../lib/bridge", () => ({ aoBridge: { uiSettings: bridge } }));
import { useAutomaticBrowserStore } from "./automatic-browser-store";

describe("automatic browser store", () => {
	beforeEach(() => {
		bridge.get.mockReset().mockResolvedValue({ ...DEFAULT_UI_SETTINGS });
		bridge.set.mockReset();
		useAutomaticBrowserStore.setState({
			mode: "internal",
			loaded: false,
			saving: false,
			error: false,
			loadError: false,
		});
	});

	it("keeps a failed preference read retryable", async () => {
		bridge.get.mockRejectedValueOnce(new Error("unavailable")).mockResolvedValueOnce({
			...DEFAULT_UI_SETTINGS,
			automaticBrowser: "blocked",
		});

		await useAutomaticBrowserStore.getState().load();
		expect(useAutomaticBrowserStore.getState()).toMatchObject({ loaded: false, loadError: true });

		await useAutomaticBrowserStore.getState().load();
		expect(useAutomaticBrowserStore.getState()).toMatchObject({ loaded: true, loadError: false, mode: "blocked" });
	});

	it("reloads persisted preferences when a save fails during startup loading", async () => {
		let resolveInitial!: (settings: typeof DEFAULT_UI_SETTINGS) => void;
		bridge.get
			.mockImplementationOnce(() => new Promise((resolve) => { resolveInitial = resolve; }))
			.mockResolvedValueOnce({ ...DEFAULT_UI_SETTINGS, automaticBrowser: "external" });
		bridge.set.mockRejectedValue(new Error("unavailable"));

		const initialLoad = useAutomaticBrowserStore.getState().load();
		const failedSave = useAutomaticBrowserStore.getState().setMode("blocked");
		await Promise.resolve();
		resolveInitial({ ...DEFAULT_UI_SETTINGS, automaticBrowser: "internal" });
		await Promise.all([initialLoad, failedSave]);

		expect(bridge.get).toHaveBeenCalledTimes(2);
		expect(useAutomaticBrowserStore.getState()).toMatchObject({ loaded: true, mode: "external", saving: false, error: true });
	});
});
