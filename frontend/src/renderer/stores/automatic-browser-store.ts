import { create } from "zustand";
import type { AutomaticBrowserMode } from "../../shared/ui-locale";
import { aoBridge } from "../lib/bridge";

type State = {
	mode: AutomaticBrowserMode;
	loaded: boolean;
	saving: boolean;
	error: boolean;
	loadError: boolean;
	load: () => Promise<void>;
	setMode: (mode: AutomaticBrowserMode) => Promise<void>;
};

let revision = 0;
let loading: Promise<void> | undefined;
export const useAutomaticBrowserStore = create<State>((set, get) => ({
	mode: "internal", loaded: false, saving: false, error: false, loadError: false,
	load: async () => {
		if (get().loaded) return;
		if (loading) return loading;
		const started = revision;
		loading = (async () => {
			try {
				const settings = await aoBridge.uiSettings.get();
				if (started === revision) set({ mode: settings.automaticBrowser ?? "internal", loaded: true, loadError: false });
			} catch { if (started === revision) set({ loadError: true }); }
		})();
		try { await loading; } finally { loading = undefined; }
	},
	setMode: async (mode) => {
		const current = ++revision;
		set({ saving: true, error: false });
		try {
			const result = await aoBridge.uiSettings.set({ automaticBrowser: mode });
			if (current === revision) set({ mode: result.automaticBrowser, loaded: true, saving: false, error: false, loadError: false });
		} catch {
			if (current !== revision) return;
			set({ saving: false, error: true });
			if (!get().loaded) {
				const pendingLoad = loading;
				if (pendingLoad) await pendingLoad;
				if (current === revision && !get().loaded) await get().load();
			}
		}
	},
}));
