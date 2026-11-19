// Disk cleanup for AO's Chromium storage under ~/.ao/electron.
//
// Every worker's Browser panel gets a temporary profile partition named
// `persist:ao-browser-temporary-<uuid>`, a fresh uuid each time. Clearing it
// when the worker ends empties its storage but leaves the folder (and its code
// and GPU caches) behind, and a quit or crash skips even that, so these
// folders pile up: hundreds of them, gigabytes on a long-lived install. No
// temporary partition is ever reused across launches, and the app holds the
// single-instance lock on this profile, so at startup every one of them is
// orphaned.
//
// Named profiles (`ao-browser-profile-*`) hold the user's sign-ins and are
// never touched here.

import { lstat, mkdir, readdir, rm, writeFile, access } from "node:fs/promises";
import path from "node:path";

const TEMPORARY_PARTITION_DIR = /^ao-browser-temporary-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export type TemporaryPartitionSweep = { removed: number; failed: number };

/**
 * Delete the folders of temporary browser partitions under `userDataDir`.
 * Call it before this launch creates any browser partition. Only real
 * directories whose names are exactly a temporary partition's are removed;
 * symlinks and anything else are left alone.
 */
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

/** The slice of Electron's Session this module uses. */
export type CacheSession = {
	clearCache(): Promise<void>;
	clearCodeCaches(options: { urls?: string[] }): Promise<void>;
};

// Bump to purge again after a change that leaves caches worth dropping.
export const ROOT_CACHE_PURGE_VERSION = 1;

/**
 * Clear the app's default session HTTP and code caches once per purge
 * version. They only ever grew (about 1 GB on a long-lived install) and hold
 * nothing that cannot be fetched again. Cookies and storage are untouched.
 */
export async function purgeRootCachesOnce(session: CacheSession, userDataDir: string): Promise<boolean> {
	const marker = path.join(userDataDir, `.ao-root-cache-purge-v${ROOT_CACHE_PURGE_VERSION}`);
	try {
		await access(marker);
		return false;
	} catch {
		// Not purged at this version yet.
	}
	await session.clearCache();
	await session.clearCodeCaches({});
	await mkdir(userDataDir, { recursive: true });
	await writeFile(marker, `${new Date().toISOString()}\n`);
	return true;
}
