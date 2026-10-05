import { describe, expect, it, vi, beforeEach } from "vitest";

const store = new Map<string, string>();
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async (k: string) => store.get(k) ?? null),
	setItemAsync: vi.fn(async (k: string, v: string) => { store.set(k, v); }),
	deleteItemAsync: vi.fn(async (k: string) => { store.delete(k); }),
}));

import { clearTokens, isExpired, readTokens, writeTokens } from "./tokens";

beforeEach(() => store.clear());

describe("cloud tokens", () => {
	it("round-trips through the device keystore", async () => {
		await writeTokens({ accessToken: "a", refreshToken: "r", expiresAt: 1000 });
		expect(await readTokens()).toEqual({ accessToken: "a", refreshToken: "r", expiresAt: 1000 });
	});

	it("returns null when nothing is stored", async () => {
		expect(await readTokens()).toBeNull();
	});

	it("returns null rather than throwing on unreadable contents", async () => {
		store.set("ao.cloud.tokens", "{not json");
		expect(await readTokens()).toBeNull();
	});

	it("clears every key it wrote", async () => {
		await writeTokens({ accessToken: "a", expiresAt: 1000 });
		await clearTokens();
		expect(await readTokens()).toBeNull();
	});

	// A token that expires during the request is worse than one refreshed a
	// little early, so expiry is judged against a skew window.
	it("treats a token inside the skew window as expired", () => {
		expect(isExpired({ accessToken: "a", expiresAt: 1_000_000 }, 1_000_000 - 10_000)).toBe(true);
		expect(isExpired({ accessToken: "a", expiresAt: 1_000_000 }, 1_000_000 - 120_000)).toBe(false);
	});
});
