import { describe, expect, it, vi, beforeEach } from "vitest";

const store = new Map<string, string>();
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async (k: string) => store.get(k) ?? null),
	setItemAsync: vi.fn(async (k: string, v: string) => { store.set(k, v); }),
	deleteItemAsync: vi.fn(async (k: string) => { store.delete(k); }),
}));

import { clearTokens, isExpired, readTokens, writeTokens } from "./tokens";
import { resolveCloudDeployment } from "./deployment";

beforeEach(() => store.clear());

describe("cloud tokens", () => {
  it("retains existing staging credentials when its login return is unchanged", async () => {
    const previousKey = '["https://staging-api.aoagents.dev","client_01KZ3VRKC374HS91XGRDPT3671","aoagents.ao://callback","workos"]';
    await writeTokens({ accessToken: "existing-staging", expiresAt: 2000 }, previousKey);
    expect(await readTokens(resolveCloudDeployment({ development: true }).key)).toMatchObject({ accessToken: "existing-staging" });
  });
  it("never adopts ambiguous legacy tokens or touches pairing credentials", async () => {
    store.set("ao.cloud.tokens", JSON.stringify({ accessToken: "legacy", expiresAt: 1000 }));
    store.set("ao.account.session", JSON.stringify({ refreshToken: "legacy" }));
    store.set("ao.connection.password", "paired");
    expect(await readTokens("staging")).toBeNull();
    expect(store.has("ao.cloud.tokens")).toBe(true);
    await writeTokens({ accessToken: "fresh", expiresAt: 2000 }, "staging");
    expect(await readTokens("production")).toBeNull();
    expect(await readTokens("staging")).toMatchObject({ accessToken: "fresh" });
    expect(store.has("ao.cloud.tokens")).toBe(false);
    expect(store.get("ao.connection.password")).toBe("paired");
  });
  it("rejects an envelope for a different deployment", async () => {
    await writeTokens({ accessToken: "a", expiresAt: 1000 }, "staging");
    const [key, value] = [...store.entries()][0]!;
    store.set(key, JSON.stringify({ ...JSON.parse(value), deploymentKey: "production" }));
    expect(await readTokens("staging")).toBeNull();
  });
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
