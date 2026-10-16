import { beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({ getAnalyticsOptOut: vi.fn(), setAnalyticsOptOut: vi.fn() }));
vi.mock("../lib/bridge", () => ({ aoBridge: { telemetry: bridge } }));
import { useAnalyticsOptOutStore } from "./analytics-opt-out-store";

describe("analytics opt-out store", () => {
	beforeEach(() => {
		bridge.getAnalyticsOptOut.mockReset().mockResolvedValue(true);
		bridge.setAnalyticsOptOut.mockReset();
		useAnalyticsOptOutStore.setState({ optedOut: false, loaded: false, saving: false, saveError: false });
	});

	it("loads the main-owned switch", async () => {
		await useAnalyticsOptOutStore.getState().load();
		expect(useAnalyticsOptOutStore.getState()).toMatchObject({ optedOut: true, loaded: true });
	});

	it("takes the state main confirms, not the one requested", async () => {
		bridge.setAnalyticsOptOut.mockResolvedValue(true);
		await useAnalyticsOptOutStore.getState().setOptedOut(true);
		expect(bridge.setAnalyticsOptOut).toHaveBeenCalledWith(true);
		expect(useAnalyticsOptOutStore.getState()).toMatchObject({ optedOut: true, saving: false, saveError: false });
	});

	it("surfaces a failed save and keeps the old value", async () => {
		bridge.setAnalyticsOptOut.mockRejectedValue(new Error("disk full"));
		await useAnalyticsOptOutStore.getState().setOptedOut(true);
		expect(useAnalyticsOptOutStore.getState()).toMatchObject({ optedOut: false, saving: false, saveError: true });
	});
});
