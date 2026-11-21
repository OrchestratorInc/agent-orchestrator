import { beforeEach, describe, expect, it, vi } from "vitest";

const getUiSettings = vi.fn();
const setUiSettings = vi.fn();

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		uiSettings: {
			get: (...args: unknown[]) => getUiSettings(...args),
			set: (...args: unknown[]) => setUiSettings(...args),
		},
	},
}));

import { useNotificationPreferencesStore } from "./notification-preferences-store";

const none = { mutedKinds: [], mutedSources: [] };

describe("notification-preferences-store", () => {
	beforeEach(() => {
		getUiSettings.mockReset().mockResolvedValue({
			notificationsEnabled: false,
			notificationFilters: { mutedKinds: ["pr_merged"], mutedSources: ["cloud"] },
		});
		setUiSettings.mockReset().mockImplementation(async (settings: object) => settings);
		useNotificationPreferencesStore.setState({ enabled: true, filters: none, loaded: false, saving: false, saveError: false });
	});

	it("loads what was saved", async () => {
		await useNotificationPreferencesStore.getState().load();
		expect(useNotificationPreferencesStore.getState()).toMatchObject({
			enabled: false,
			filters: { mutedKinds: ["pr_merged"], mutedSources: ["cloud"] },
			loaded: true,
		});
	});

	it("saves a muted kind and machine alongside the master switch", async () => {
		const store = useNotificationPreferencesStore.getState();
		await store.setKindMuted("ci_failed", true);
		await useNotificationPreferencesStore.getState().setSourceMuted("remote:host-a", true);
		expect(setUiSettings).toHaveBeenLastCalledWith({
			notificationsEnabled: true,
			notificationFilters: { mutedKinds: ["ci_failed"], mutedSources: ["remote:host-a"] },
		});
		await useNotificationPreferencesStore.getState().setKindMuted("ci_failed", false);
		expect(useNotificationPreferencesStore.getState().filters).toEqual({ mutedKinds: [], mutedSources: ["remote:host-a"] });
	});

	it("shows a change at once and rolls it back when it cannot be saved", async () => {
		let reject!: (error: Error) => void;
		setUiSettings.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));
		const saving = useNotificationPreferencesStore.getState().setEnabled(false);
		expect(useNotificationPreferencesStore.getState().enabled).toBe(false);
		reject(new Error("disk full"));
		await saving;
		expect(useNotificationPreferencesStore.getState()).toMatchObject({ enabled: true, saveError: true, saving: false });
	});

	it("keeps a change made while the saved settings were still loading", async () => {
		let resolveGet!: (settings: object) => void;
		getUiSettings.mockReturnValueOnce(new Promise((resolve) => { resolveGet = resolve; }));
		const loading = useNotificationPreferencesStore.getState().load();
		await useNotificationPreferencesStore.getState().setEnabled(false);
		resolveGet({ notificationsEnabled: true, notificationFilters: none });
		await loading;
		expect(useNotificationPreferencesStore.getState().enabled).toBe(false);
	});
});
