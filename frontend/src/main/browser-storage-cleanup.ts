import { lstat, mkdir, readdir, rm, writeFile, access } from "node:fs/promises";
import path from "node:path";

const TEMPORARY_PARTITION_DIR = /^ao-browser-temporary-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export type TemporaryPartitionSweep = { removed: number; failed: number };

export async function sweepTemporaryBrowserPartitions(userDataDir: string): Promise<TemporaryPartitionSweep> {
	const partitions = path.join(userDataDir, "Partitions");
	let names: string[];
	try {
		names = await readdir(partitions);
	} catch {
		return { removed: 0, failed: 0 };
	}
	let removed = 0;
	let failed = 0;
	for (const name of names) {
		if (!TEMPORARY_PARTITION_DIR.test(name)) continue;
		const folder = path.join(partitions, name);
		try {
			if (!(await lstat(folder)).isDirectory()) continue;
			await rm(folder, { recursive: true, force: true });
			removed += 1;
		} catch {
			failed += 1;
		}
	}
	return { removed, failed };
}

export type CacheSession = {
	clearCache(): Promise<void>;
	clearCodeCaches(options: { urls?: string[] }): Promise<void>;
};

export const ROOT_CACHE_PURGE_VERSION = 1;

export async function purgeRootCachesOnce(session: CacheSession, userDataDir: string): Promise<boolean> {
	const marker = path.join(userDataDir, `.ao-root-cache-purge-v${ROOT_CACHE_PURGE_VERSION}`);
	try {
		await access(marker);
		return false;
	} catch {
	}
	await session.clearCache();
	await session.clearCodeCaches({});
	await mkdir(userDataDir, { recursive: true });
	await writeFile(marker, `${new Date().toISOString()}\n`);
	return true;
}
