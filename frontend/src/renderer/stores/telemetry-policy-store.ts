import { create } from "zustand";
import type { TelemetryPolicyView } from "../../shared/telemetry-policy";
import { aoBridge } from "../lib/bridge";

type TelemetryPolicyState = {
	view: TelemetryPolicyView | null;
	loaded: boolean;
	saving: boolean;
	saveError: boolean;
	load(): Promise<void>;
	setEnabled(enabled: boolean): Promise<void>;
};

let pendingLoad: Promise<void> | null = null;
let subscribed = false;

export const useTelemetryPolicyStore = create<TelemetryPolicyState>((set, get) => ({
	view: null, loaded: false, saving: false, saveError: false,
	load: async () => {
		if (get().loaded) return;
		if (pendingLoad) return pendingLoad;
		pendingLoad = (async () => {
			if (!subscribed) {
				subscribed = true;
				// This policy is the failure-reporting consent. PostHog product
				// analytics keep their own once-a-day gates in localStorage and
				// are not governed by it (see applyRendererTelemetryPolicy).
				aoBridge.telemetry.onPolicy((view) => set({ view, loaded: true, saving: false }));
			}
			try { set({ view: await aoBridge.telemetry.getPolicy(), loaded: true }); }
			catch { set({ loaded: true, saveError: true }); }
		})();
		try { await pendingLoad; } finally { pendingLoad = null; }
	},
	setEnabled: async (enabled) => {
		if (get().saving) return;
		set({ saving: true, saveError: false });
		try { set({ view: await aoBridge.telemetry.setEventsEnabled(enabled), loaded: true, saving: false }); }
		catch { set({ saving: false, saveError: true }); }
	},
}));
