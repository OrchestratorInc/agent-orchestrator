import { create } from "zustand";
import {
	effectiveShortcutBindings,
	type AppShortcutId,
	type KeybindingOverrides,
	type ShortcutBinding,
} from "../../shared/shortcuts";
import { aoBridge } from "../lib/bridge";
import { isMacPlatform } from "../lib/platform";

type KeybindingsState = {
	overrides: KeybindingOverrides;
	loaded: boolean;
	load: () => Promise<void>;
	setOverrides: (overrides: KeybindingOverrides) => Promise<void>;
	setBindings: (id: AppShortcutId, bindings: readonly ShortcutBinding[]) => Promise<void>;
	resetBinding: (id: AppShortcutId) => Promise<void>;
	resetAll: () => Promise<void>;
};

async function persist(overrides: KeybindingOverrides): Promise<KeybindingOverrides> {
	return aoBridge.keybindings.set(overrides);
}

export const useKeybindingsStore = create<KeybindingsState>((set, get) => ({
	overrides: {},
	loaded: false,
	load: async () => {
		if (get().loaded) return;
		const overrides = await aoBridge.keybindings.get();
		set({ overrides, loaded: true });
	},
	setOverrides: async (candidate) => {
		const overrides = await persist(candidate);
		set({ overrides, loaded: true });
	},
	setBindings: async (id, bindings) => {
		const overrides = await persist({ ...get().overrides, [id]: bindings });
		set({ overrides, loaded: true });
	},
	resetBinding: async (id) => {
		const next = { ...get().overrides };
		delete next[id];
		const overrides = await persist(next);
		set({ overrides, loaded: true });
	},
	resetAll: async () => {
		const overrides = await persist({});
		set({ overrides, loaded: true });
	},
}));

export function currentShortcutBindings(id: AppShortcutId): readonly ShortcutBinding[] {
	return effectiveShortcutBindings(id, isMacPlatform(), useKeybindingsStore.getState().overrides);
}
