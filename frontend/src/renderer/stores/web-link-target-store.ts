import { create } from "zustand";
import { DEFAULT_UI_SETTINGS, type WebLinkTarget } from "../../shared/ui-locale";
import { aoBridge } from "../lib/bridge";

type WebLinkTargetState = {
	target: WebLinkTarget;
	loaded: boolean;
	saving: boolean;
	saveError: boolean;
	load: () => Promise<void>;
	setTarget: (target: WebLinkTarget) => Promise<void>;
};

let settingRevision = 0;
let pendingLoad: Promise<void> | undefined;

export const useWebLinkTargetStore = create<WebLinkTargetState>((set, get) => ({
	target: DEFAULT_UI_SETTINGS.webLinkTarget,
	loaded: false,
	saving: false,
	saveError: false,
	load: async () => {
		if (get().loaded) return;
		if (pendingLoad) return pendingLoad;
		const revisionAtStart = settingRevision;
		pendingLoad = (async () => {
			let target = DEFAULT_UI_SETTINGS.webLinkTarget;
			try {
				target = (await aoBridge.uiSettings.get()).webLinkTarget;
			} catch {
				// A missing bridge or unreadable setting must not prevent the UI from starting.
			}
			if (revisionAtStart === settingRevision) set({ target, loaded: true });
		})();
		try {
			await pendingLoad;
		} finally {
			pendingLoad = undefined;
		}
	},
	setTarget: async (target) => {
		const revision = ++settingRevision;
		set({ saving: true, saveError: false });
		try {
			await aoBridge.uiSettings.set({ webLinkTarget: target });
			if (revision === settingRevision) set({ target, loaded: true, saving: false });
		} catch {
			if (revision === settingRevision) set({ saving: false, saveError: true });
		}
	},
}));
