import { create } from "zustand";
import type { TelemetryPolicyView } from "../../shared/telemetry-policy";
import { aoBridge } from "../lib/bridge";

type TelemetryPolicyState = {
	view: TelemetryPolicyView | null;
	loaded: boolean;
	saving: boolean;
	loadError: boolean;
	saveError: boolean;
	saveErrorView: TelemetryPolicyView | null;
	load(): Promise<void>;
	setEnabled(enabled: boolean): Promise<void>;
};

let pendingLoad: Promise<void> | null = null;
let subscribed = false;

// A save failure belongs to the view it was rejected against. The retry loop
// republishes that view while cleanup is pending, which says nothing new about
// the failure; a new consent generation or apply state replaces the attempt.
function supersedesSaveError(next: TelemetryPolicyView, failedAgainst: TelemetryPolicyView | null): boolean {
	return !failedAgainst || next.consentGeneration !== failedAgainst.consentGeneration || next.state !== failedAgainst.state;
}

export const useTelemetryPolicyStore = create<TelemetryPolicyState>((set, get) => ({
	view: null, loaded: false, saving: false, loadError: false, saveError: false, saveErrorView: null,
	load: async () => {
		if (get().loaded) return;
		if (pendingLoad) return pendingLoad;
		pendingLoad = (async () => {
			if (!subscribed) {
				subscribed = true;
				// This policy is the failure-reporting consent. PostHog product
				// analytics keep their own once-a-day gates in localStorage and
				// are not governed by it (see applyRendererTelemetryPolicy).
				// Leaves `saving` alone: main publishes while a write is still in
				// flight, and only that write's own settlement may end the attempt.
				aoBridge.telemetry.onPolicy((view) => set((state) => ({
					view,
					loaded: true,
					loadError: false,
					...(supersedesSaveError(view, state.saveErrorView) && { saveError: false, saveErrorView: null }),
				})));
			}
			try { set({ view: await aoBridge.telemetry.getPolicy(), loaded: true }); }
			catch { set((state) => ({ loaded: true, loadError: !state.view })); }
		})();
		try { await pendingLoad; } finally { pendingLoad = null; }
	},
	setEnabled: async (enabled) => {
		if (get().saving) return;
		set({ saving: true, saveError: false, saveErrorView: null });
		try { set({ view: await aoBridge.telemetry.setEventsEnabled(enabled), loaded: true, saving: false }); }
		catch { set((state) => ({ saving: false, saveError: true, saveErrorView: state.view })); }
	},
}));
