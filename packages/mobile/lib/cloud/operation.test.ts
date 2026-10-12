import { beforeEach, describe, expect, it, vi } from "vitest";
const storage = vi.hoisted(() => ({ data: new Map<string, string>(), setItem: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => storage.data.get(key) ?? null,
	setItem: storage.setItem,
	removeItem: async (key: string) => { storage.data.delete(key); },
	getAllKeys: async () => [...storage.data.keys()],
} }));
import { acceptCloudOperation, clearCloudOperation, clearCloudOperationsForAccount, clearCloudOperationsForScope, prepareCloudOperation, readCloudOperation, reserveCloudOperation } from "./operation";
beforeEach(() => {
	storage.data.clear();
	storage.setItem.mockReset().mockImplementation(async (key: string, value: string) => { storage.data.set(key, value); });
});
const payload = { displayName: "Demo", repositoryUrl: "https://github.com/acme/demo", defaultBranch: "main", config: {} };
describe("scoped Cloud operation journal", () => {
	it("sign-out retires an in-flight first reservation before its storage key exists", async () => {
		const scope = JSON.stringify(["https://cloud.test", "user-1", "org-1"]);
		let release!: () => void;
		storage.setItem.mockImplementationOnce(async (key: string, value: string) => {
			await new Promise<void>((resolve) => { release = resolve; });
			storage.data.set(key, value);
		});
		const reservation = reserveCloudOperation("project-create/repo", prepareCloudOperation(undefined, scope, payload, () => "key")).catch(() => {});
		await vi.waitFor(() => expect(storage.setItem).toHaveBeenCalledOnce());
		const clearing = clearCloudOperationsForAccount("https://cloud.test", "user-1");
		await Promise.resolve(); await Promise.resolve();
		release(); await Promise.all([reservation, clearing]);
		expect(await readCloudOperation(scope, "project-create/repo")).toBeNull();
	});
	it("preserves the key and frozen payload for an unchanged retry", () => {
		const newKey = vi.fn().mockReturnValueOnce("first").mockReturnValueOnce("second");
		const first = prepareCloudOperation(undefined, "scope-one", payload, newKey);
		expect(prepareCloudOperation(first, "scope-one", payload, newKey)).toBe(first);
		expect(prepareCloudOperation(first, "scope-two", payload, newKey).key).toBe("second");
		payload.config = { changed: true };
		expect(first.payload.config).toEqual({});
		payload.config = {};
	});
	it("restores an uncertain operation, then records accepted ID without changing its request", async () => {
		const operation = prepareCloudOperation(undefined, "scope", payload, () => "key");
		await reserveCloudOperation("project-create/repo", operation);
		expect(await readCloudOperation("scope", "project-create/repo")).toEqual(operation);
		await acceptCloudOperation("scope", "project-create/repo", "key", "project-1");
		expect(await readCloudOperation("scope", "project-create/repo")).toEqual({ ...operation, acceptedId: "project-1" });
		await clearCloudOperation("scope", "project-create/repo", "wrong");
		expect(await readCloudOperation("scope", "project-create/repo")).not.toBeNull();
	});
	it("refuses a changed request until the uncertain write is explicitly resolved", async () => {
		await reserveCloudOperation("project-create/repo", prepareCloudOperation(undefined, "scope", payload, () => "first"));
		await expect(reserveCloudOperation("project-create/repo", prepareCloudOperation(undefined, "scope", { ...payload, displayName: "Other" }, () => "second"))).rejects.toThrow(/previous/i);
	});
	it("clears only the selected scope and cannot recreate it from a late acceptance", async () => {
		for (const scope of ["one", "two"]) await reserveCloudOperation("worker-create/p1", prepareCloudOperation(undefined, scope, { projectId: "p1" }, () => scope));
		await clearCloudOperationsForScope("one");
		await acceptCloudOperation("one", "worker-create/p1", "one", "late");
		expect(await readCloudOperation("one", "worker-create/p1")).toBeNull();
		expect(await readCloudOperation("two", "worker-create/p1")).not.toBeNull();
	});
	it("stops before HTTP work when reservation storage fails", async () => {
		storage.setItem.mockRejectedValueOnce(new Error("Storage unavailable"));
		await expect(reserveCloudOperation("project-create/repo", prepareCloudOperation(undefined, "scope", payload, () => "key"))).rejects.toThrow("Storage unavailable");
	});
	it("never persists credential fields", async () => {
		const operation = prepareCloudOperation(undefined, "scope", { secret: "not-for-AsyncStorage" }, () => "key");
		await expect(reserveCloudOperation("project-create/repo", operation)).rejects.toThrow(/credential/i);
		expect(storage.setItem).not.toHaveBeenCalled();
	});
});
