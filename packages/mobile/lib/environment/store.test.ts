import { describe, expect, it, vi, beforeEach } from "vitest";

const storage = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: vi.fn(async (k: string) => storage.get(k) ?? null),
		setItem: vi.fn(async (k: string, v: string) => {
			storage.set(k, v);
		}),
		removeItem: vi.fn(async (k: string) => {
			storage.delete(k);
		}),
	},
}));

import { loadEnvironment, saveEnvironment } from "./store";

beforeEach(() => storage.clear());

describe("environment persistence", () => {
	it("defaults to local when nothing has been chosen", async () => {
		expect(await loadEnvironment()).toBe("local");
	});

	it("round-trips the chosen environment", async () => {
		await saveEnvironment("cloud");
		expect(await loadEnvironment()).toBe("cloud");
	});

	// A corrupted value must not strand the user in an environment that does
	// not exist.
	it("falls back to local for an unrecognised stored value", async () => {
		storage.set("ao.environment", "mainframe");
		expect(await loadEnvironment()).toBe("local");
	});
});
