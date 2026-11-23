import { create } from "zustand";
import { aoBridge } from "../lib/bridge";
import type { NotificationKindId, NotificationSource } from "../lib/notification-preferences";
import { DEFAULT_UI_SETTINGS, type NotificationFilters } from "../../shared/ui-locale";

type NotificationPreferencesState = {
	enabled: boolean;
	filters: NotificationFilters;
	loaded: boolean;
	saving: boolean;
	saveError: boolean;
	load: () => Promise<void>;
	setEnabled: (enabled: boolean) => Promise<void>;
	setKindMuted: (kind: NotificationKindId, muted: boolean) => Promise<void>;
	setSourceMuted: (source: NotificationSource, muted: boolean) => Promise<void>;
};

type Preferences = Pick<NotificationPreferencesState, "enabled" | "filters">;

let settingRevision = 0;
let pendingLoad: Promise<void> | undefined;

function toggled(list: string[], value: string, include: boolean): string[] {
	const without = list.filter((item) => item !== value);
	return include ? [...without, value] : without;
}

export const useNotificationPreferencesStore = create<NotificationPreferencesState>((set, get) => {
	// Shows the change at once and rolls it back if it cannot be saved, so a
	// toggle never lags behind the click.
	const save = async (patch: Preferences) => {
		const revision = ++settingRevision;
		const previous = { enabled: get().enabled, filters: get().filters };
		set({ ...patch, saving: true, saveError: false });
		try {
			await aoBridge.uiSettings.set({ notificationsEnabled: patch.enabled, notificationFilters: patch.filters });
			if (revision === settingRevision) set({ saving: false });
		} catch {
			if (revision === settingRevision) set({ ...previous, saving: false, saveError: true });
		}
	};
	// Every save writes the whole preferences, so an edit waits for the saved
	// ones and changes only its own field. Built from the defaults instead, an
	// edit made while loading would wipe the saved mutes.
	const edit = async (change: (current: Preferences) => Preferences) => {
		if (!get().loaded) await get().load();
		return save(change(get()));
	};
	return {
		enabled: DEFAULT_UI_SETTINGS.notificationsEnabled,
		filters: DEFAULT_UI_SETTINGS.notificationFilters,
		loaded: false,
		saving: false,
		saveError: false,
		load: async () => {
			if (get().loaded) return;
			if (pendingLoad) return pendingLoad;
			pendingLoad = (async () => {
				let enabled = DEFAULT_UI_SETTINGS.notificationsEnabled;
				let filters = DEFAULT_UI_SETTINGS.notificationFilters;
				try {
					const settings = await aoBridge.uiSettings.get();
					enabled = settings.notificationsEnabled ?? enabled;
					filters = settings.notificationFilters ?? filters;
				} catch {
					// A missing bridge or unreadable setting must not prevent the UI from starting.
				}
				set({ enabled, filters, loaded: true });
			})();
			try {
				await pendingLoad;
			} finally {
				pendingLoad = undefined;
			}
		},
		setEnabled: (enabled) => edit(({ filters }) => ({ enabled, filters })),
		setKindMuted: (kind, muted) =>
			edit(({ enabled, filters }) => ({ enabled, filters: { ...filters, mutedKinds: toggled(filters.mutedKinds, kind, muted) } })),
		setSourceMuted: (source, muted) =>
			edit(({ enabled, filters }) => ({ enabled, filters: { ...filters, mutedSources: toggled(filters.mutedSources, source, muted) } })),
	};
});
