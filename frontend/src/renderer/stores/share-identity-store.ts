import { create } from "zustand";
import { aoBridge } from "../lib/bridge";

type ShareIdentityState = {
	share: boolean;
	loaded: boolean;
	saving: boolean;
	loadError: boolean;
	saveError: boolean;
	load(): Promise<void>;
	setShare(share: boolean): Promise<void>;
};

/**
 * "Include my account identity" (Settings > Privacy), off by default. Main owns
 * the marker the daemon reads; the renderer applies a change when main
 * broadcasts it (see main.tsx), so this store only reads and requests.
 */
export const useShareIdentityStore = create<ShareIdentityState>((set, get) => ({
	share: false,
	loaded: false,
	saving: false,
	loadError: false,
	saveError: false,
	load: async () => {
		if (get().loaded) return;
		try {
			set({ share: await aoBridge.telemetry.getShareIdentity(), loaded: true, loadError: false });
		} catch {
			set({ loaded: true, loadError: true });
		}
	},
	setShare: async (share) => {
		if (get().saving) return;
		set({ saving: true, saveError: false });
		try {
			set({ share: await aoBridge.telemetry.setShareIdentity(share), saving: false, loadError: false });
		} catch {
			set({ saving: false, saveError: true });
		}
	},
}));
