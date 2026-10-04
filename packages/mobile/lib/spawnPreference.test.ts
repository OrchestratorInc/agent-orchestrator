import { beforeEach, describe, expect, it, vi } from "vitest";

const storage = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: vi.fn(async (key: string) => storage.get(key) ?? null),
		setItem: vi.fn(async (key: string, value: string) => { storage.set(key, value); }),
	},
}));

import { loadSpawnPreference, saveSpawnPreference } from "./spawnPreference";

beforeEach(() => storage.clear());

describe("spawn preference", () => {
	it("restores the last host and project after reopening the sheet", async () => {
		const preference = { source: { kind: "local" as const, id: "desktop-1" }, projectId: "project-1" };
		await saveSpawnPreference(preference);
		expect(await loadSpawnPreference()).toEqual(preference);
	});
	it("ignores invalid saved data", async () => {
		storage.set("ao.spawnPreference", JSON.stringify({ source: { kind: "cloud", id: "" }, projectId: "project-1" }));
		expect(await loadSpawnPreference()).toBeNull();
		storage.set("ao.spawnPreference", "not-json");
		expect(await loadSpawnPreference()).toBeNull();
	});
});
