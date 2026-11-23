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
		useNotificationPreferencesStore.setState({ loaded: true });
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
		useNotificationPreferencesStore.setState({ loaded: true });
		let reject!: (error: Error) => void;
		setUiSettings.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));
		const saving = useNotificationPreferencesStore.getState().setEnabled(false);
		expect(useNotificationPreferencesStore.getState().enabled).toBe(false);
		reject(new Error("disk full"));
		await saving;
		expect(useNotificationPreferencesStore.getState()).toMatchObject({ enabled: true, saveError: true, saving: false });
	});

	it("applies a change made while loading to the saved settings, keeping the rest", async () => {
		let resolveGet!: (settings: object) => void;
		getUiSettings.mockReturnValueOnce(new Promise((resolve) => { resolveGet = resolve; }));
		const loading = useNotificationPreferencesStore.getState().load();
		const saving = useNotificationPreferencesStore.getState().setEnabled(false);
		await Promise.resolve();
		expect(setUiSettings).not.toHaveBeenCalled();
		resolveGet({ notificationsEnabled: true, notificationFilters: { mutedKinds: [], mutedSources: ["cloud"] } });
		await Promise.all([loading, saving]);
		const saved = { mutedKinds: [], mutedSources: ["cloud"] };
		expect(setUiSettings).toHaveBeenCalledWith({ notificationsEnabled: false, notificationFilters: saved });
		expect(useNotificationPreferencesStore.getState()).toMatchObject({ enabled: false, filters: saved, loaded: true });
	});

	it("reads the saved settings before an edit made without a load", async () => {
		await useNotificationPreferencesStore.getState().setKindMuted("ci_failed", true);
		expect(setUiSettings).toHaveBeenCalledWith({
			notificationsEnabled: false,
			notificationFilters: { mutedKinds: ["pr_merged", "ci_failed"], mutedSources: ["cloud"] },
		});
	});
});
