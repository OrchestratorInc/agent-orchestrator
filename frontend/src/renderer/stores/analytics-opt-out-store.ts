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
