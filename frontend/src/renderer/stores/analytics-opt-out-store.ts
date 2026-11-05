import { create } from "zustand";
import { aoBridge } from "../lib/bridge";

type AnalyticsOptOutState = {
	optedOut: boolean;
	loaded: boolean;
	saving: boolean;
	loadError: boolean;
	saveError: boolean;
	load(): Promise<void>;
	setOptedOut(optedOut: boolean): Promise<void>;
};

/**
 * The in-app PostHog opt-out (Settings > Privacy). Main owns the switch; the
 * renderer applies it to its own client when main broadcasts the change (see
 * main.tsx), so this store only reads and requests.
 */
export const useAnalyticsOptOutStore = create<AnalyticsOptOutState>((set, get) => ({
	optedOut: false,
	loaded: false,
	saving: false,
	loadError: false,
	saveError: false,
	load: async () => {
		if (get().loaded) return;
		try {
			set({ optedOut: await aoBridge.telemetry.getAnalyticsOptOut(), loaded: true, loadError: false });
		} catch {
			set({ loaded: true, loadError: true });
		}
	},
	setOptedOut: async (optedOut) => {
		if (get().saving) return;
		set({ saving: true, saveError: false });
		try {
			set({ optedOut: await aoBridge.telemetry.setAnalyticsOptOut(optedOut), saving: false, loadError: false });
		} catch {
			set({ saving: false, saveError: true });
		}
	},
}));
