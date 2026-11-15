import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readdir, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { purgeRootCachesOnce, sweepTemporaryBrowserPartitions } from "./browser-storage-cleanup";

const uuid = (n: number) => `0000000${n}-1111-4222-8333-444444444444`;

describe("browser storage cleanup", () => {
	let userData: string;
	beforeEach(async () => {
		userData = await mkdtemp(path.join(os.tmpdir(), "ao-storage-cleanup-"));
	});
	afterEach(async () => {
		await rm(userData, { recursive: true, force: true });
	});

	it("removes every temporary partition folder and nothing else", async () => {
		const partitions = path.join(userData, "Partitions");
		for (const n of [1, 2, 3]) {
			await mkdir(path.join(partitions, `ao-browser-temporary-${uuid(n)}`, "Code Cache"), { recursive: true });
			await writeFile(path.join(partitions, `ao-browser-temporary-${uuid(n)}`, "Code Cache", "index"), "x");
		}
		// Named profiles hold the user's sign-ins.
		await mkdir(path.join(partitions, `ao-browser-profile-${uuid(4)}`, "Cookies"), { recursive: true });
		// Look-alikes: wrong shape, a file, and a symlink to somewhere that must survive.
		await mkdir(path.join(partitions, "ao-browser-temporary-not-a-uuid"), { recursive: true });
		await writeFile(path.join(partitions, `ao-browser-temporary-${uuid(5)}`), "a file");
		const outside = path.join(userData, "outside");
		await mkdir(outside);
		await writeFile(path.join(outside, "keep"), "keep");
		await symlink(outside, path.join(partitions, `ao-browser-temporary-${uuid(6)}`));

		expect(await sweepTemporaryBrowserPartitions(userData)).toEqual({ removed: 3, failed: 0 });
		expect((await readdir(partitions)).sort()).toEqual(
			[`ao-browser-profile-${uuid(4)}`, "ao-browser-temporary-not-a-uuid", `ao-browser-temporary-${uuid(5)}`, `ao-browser-temporary-${uuid(6)}`].sort(),
		);
		expect(existsSync(path.join(outside, "keep"))).toBe(true);
	});

	it("does nothing when there is no Partitions folder", async () => {
		expect(await sweepTemporaryBrowserPartitions(userData)).toEqual({ removed: 0, failed: 0 });
	});

	it("purges the root caches once per version", async () => {
		const session = { clearCache: vi.fn(async () => undefined), clearCodeCaches: vi.fn(async () => undefined) };
		expect(await purgeRootCachesOnce(session, userData)).toBe(true);
		expect(session.clearCache).toHaveBeenCalledOnce();
		expect(session.clearCodeCaches).toHaveBeenCalledWith({});
		expect(await purgeRootCachesOnce(session, userData)).toBe(false);
		expect(session.clearCache).toHaveBeenCalledOnce();
	});

	it("tries again next launch when a purge fails", async () => {
		const session = { clearCache: vi.fn(async (): Promise<void> => { throw new Error("busy"); }), clearCodeCaches: vi.fn(async () => undefined) };
		await expect(purgeRootCachesOnce(session, userData)).rejects.toThrow("busy");
		session.clearCache.mockResolvedValue(undefined);
		expect(await purgeRootCachesOnce(session, userData)).toBe(true);
	});
});
