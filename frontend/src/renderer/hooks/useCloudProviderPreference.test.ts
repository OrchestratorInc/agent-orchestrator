import { beforeEach, describe, expect, it, vi } from "vitest";
import { cloudProviderPreferenceQueryKey, migrateLegacyCloudProvider } from "./useCloudProviderPreference";

describe("Cloud provider preference migration", () => {
	beforeEach(() => window.localStorage.clear());

	it("keys cached values by Cloud origin and user ID", () => {
		expect(cloudProviderPreferenceQueryKey("https://cloud.test", "alice")).not.toEqual(cloudProviderPreferenceQueryKey("https://cloud.test", "bob"));
	});

	it("uses Cloud's saved value over a different legacy local choice", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "nodeops");
		const client = { getUserPreferences: vi.fn(), putUserPreferences: vi.fn() };
		await expect(migrateLegacyCloudProvider(client, "coder", ["nodeops", "coder"])).resolves.toBe("coder");
		expect(client.putUserPreferences).not.toHaveBeenCalled();
		expect(window.localStorage.getItem("ao.cloud.sandboxProvider")).toBeNull();
	});

	it("seeds a valid legacy value once, atomically", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "coder");
		const client = { getUserPreferences: vi.fn(), putUserPreferences: vi.fn().mockResolvedValue({ sandboxProvider: "coder" }) };
		await expect(migrateLegacyCloudProvider(client, null, ["nodeops", "coder"])).resolves.toBe("coder");
		expect(client.putUserPreferences).toHaveBeenCalledWith({ sandboxProvider: "coder", initializeOnly: true });
		expect(window.localStorage.getItem("ao.cloud.sandboxProvider")).toBeNull();
	});

	it("reloads the winner after concurrent migration conflict", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "nodeops");
		const client = { getUserPreferences: vi.fn().mockResolvedValue({ sandboxProvider: "coder" }), putUserPreferences: vi.fn().mockRejectedValue({ status: 409, code: "preference_conflict" }) };
		await expect(migrateLegacyCloudProvider(client, null, ["nodeops", "coder"])).resolves.toBe("coder");
		expect(client.getUserPreferences).toHaveBeenCalledOnce();
		expect(window.localStorage.getItem("ao.cloud.sandboxProvider")).toBeNull();
	});

	it("does not seed an unavailable legacy value", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "ecs");
		const client = { getUserPreferences: vi.fn(), putUserPreferences: vi.fn() };
		await expect(migrateLegacyCloudProvider(client, null, ["nodeops", "coder"])).resolves.toBeNull();
		expect(client.putUserPreferences).not.toHaveBeenCalled();
	});

	it("keeps legacy data on network failure for retry", async () => {
		window.localStorage.setItem("ao.cloud.sandboxProvider", "coder");
		const client = { getUserPreferences: vi.fn(), putUserPreferences: vi.fn().mockRejectedValue(new Error("offline")) };
		await expect(migrateLegacyCloudProvider(client, null, ["coder"])).rejects.toThrow("offline");
		expect(window.localStorage.getItem("ao.cloud.sandboxProvider")).toBe("coder");
	});
});
