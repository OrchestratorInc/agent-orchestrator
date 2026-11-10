/**
 * The disk cache of artifact preview images, and the queue that fills it.
 *
 * `get` answers from disk; `request` renders what is missing, at most
 * one at a time, and each artifact once however many cards ask.
 * Files live under the app data dir and are pruned oldest-read first, so the
 * cache stays bounded.
 */

import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import type { IpcMain } from "electron";
import { type ArtifactSnapshotJob, ARTIFACT_SNAPSHOT_VERSION, parseArtifactSnapshotJob } from "../shared/artifact-snapshot";

/** The most the cache keeps; the oldest-read files go first. */
export const SNAPSHOT_CACHE_MAX_BYTES = 150 * 1024 * 1024;
export const SNAPSHOT_CACHE_MAX_FILES = 4_000;
/** A snapshot that failed is not tried again for this long. */
export const SNAPSHOT_RETRY_MS = 60_000;
/** More than this waiting in the queue and the oldest request is dropped, so a long scroll cannot pile up work. */
export const SNAPSHOT_MAX_QUEUED = 64;
const PRUNE_EVERY_WRITES = 25;
/** A pause after each render, so a long queue leaves the app room to draw and scroll. */
export const SNAPSHOT_GAP_MS = 150;

export type SnapshotRenderer = (job: ArtifactSnapshotJob) => Promise<Buffer>;

/** A file with nothing to draw: asked again it would only fail again, so it is not retried until the app restarts. */
export class SnapshotUnavailableError extends Error {}

/** What `request` answers when the request was pushed out of a full queue: not a failure, ask again later. */
export const SNAPSHOT_DROPPED = "dropped" as const;
export type SnapshotResult = Buffer | null | typeof SNAPSHOT_DROPPED;

export type ArtifactSnapshotStore = ReturnType<typeof createArtifactSnapshotStore>;

/** The cache file name for a job: the same file, version and theme share one. URLs are left out, as they carry ports and tokens that change between launches. */
export function artifactSnapshotKey(job: ArtifactSnapshotJob): string {
	const identity = [ARTIFACT_SNAPSHOT_VERSION, job.hostId ?? "", job.cloud?.orgId ?? "", job.sessionId, job.path, job.updatedAt, job.theme, job.styleTheme];
	return createHash("sha256").update(JSON.stringify(identity)).digest("hex").slice(0, 40);
}

export function createArtifactSnapshotStore(options: {
	directory: string;
	render: SnapshotRenderer;
	gapMs?: number;
	now?: () => number;
}) {
	const { directory, render } = options;
	const now = options.now ?? Date.now;
	const gapMs = options.gapMs ?? SNAPSHOT_GAP_MS;
	const fileFor = (key: string) => path.join(directory, `${key}.webp`);
	const inFlight = new Map<string, Promise<SnapshotResult>>();
	const failedAt = new Map<string, number>();
	const waiting: Array<{ key: string; priority: number; seq: number; start: () => void; drop: () => void }> = [];
	let seq = 0;
	// One render at a time: every job shares the renderer's single window, and starting a job
	// unmounts the previous one's preview, so two at once would wipe each other mid-render.
	let running = false;
	let writes = 0;
	let pruning: Promise<void> | undefined;

	async function read(key: string): Promise<Buffer | null> {
		try {
			const bytes = await fs.readFile(fileFor(key));
			// Reading marks the file as recent for pruning; a failure to is harmless.
			const at = new Date();
			void fs.utimes(fileFor(key), at, at).catch(() => undefined);
			return bytes;
		} catch {
			return null;
		}
	}

	async function write(key: string, bytes: Buffer): Promise<void> {
		await fs.mkdir(directory, { recursive: true });
		const target = fileFor(key);
		const staging = `${target}.${process.pid}.tmp`;
		await fs.writeFile(staging, bytes);
		await fs.rename(staging, target);
		writes += 1;
		if (writes % PRUNE_EVERY_WRITES === 0) void prune();
	}

	/** Removes the oldest-read files until the cache is within its limits. */
	function prune(): Promise<void> {
		pruning ??= (async () => {
			try {
				const names = (await fs.readdir(directory)).filter((name) => name.endsWith(".webp") || name.endsWith(".tmp"));
				const files = (await Promise.all(names.map(async (name) => {
					try {
						const stat = await fs.stat(path.join(directory, name));
						return { name, size: stat.size, at: stat.mtimeMs };
					} catch {
						return undefined;
					}
				}))).filter((file): file is { name: string; size: number; at: number } => file !== undefined);
				// A staging file left by a crash is garbage once it is not being written.
				const stale = files.filter((file) => file.name.endsWith(".tmp") && now() - file.at > 60_000);
				await Promise.all(stale.map((file) => fs.rm(path.join(directory, file.name), { force: true })));
				const kept = files.filter((file) => file.name.endsWith(".webp")).sort((a, b) => a.at - b.at);
				let bytes = kept.reduce((total, file) => total + file.size, 0);
				let count = kept.length;
				for (const file of kept) {
					if (bytes <= SNAPSHOT_CACHE_MAX_BYTES && count <= SNAPSHOT_CACHE_MAX_FILES) break;
					await fs.rm(path.join(directory, file.name), { force: true });
					bytes -= file.size;
					count -= 1;
				}
			} catch {
				// A cache that cannot be pruned still works.
			} finally {
				pruning = undefined;
			}
		})();
		return pruning;
	}

	function pump(): void {
		if (running || waiting.length === 0) return;
		// Cards on screen first, then the newest: the cards being looked at are the ones that asked last.
		let best = 0;
		for (let index = 1; index < waiting.length; index++) {
			const a = waiting[index]!;
			const b = waiting[best]!;
			if (a.priority > b.priority || (a.priority === b.priority && a.seq > b.seq)) best = index;
		}
		waiting.splice(best, 1)[0]?.start();
	}

	function enqueue(key: string, job: ArtifactSnapshotJob, priority: number): Promise<SnapshotResult> {
		return new Promise((resolve) => {
			const finish = (bytes: SnapshotResult) => resolve(bytes);
			waiting.push({
				key,
				priority,
				seq: (seq += 1),
				drop: () => finish(SNAPSHOT_DROPPED),
				start: () => {
					running = true;
					void (async () => {
						try {
							const bytes = await render(job);
							await write(key, bytes);
							failedAt.delete(key);
							finish(bytes);
						} catch (error) {
							failedAt.set(key, error instanceof SnapshotUnavailableError ? Number.POSITIVE_INFINITY : now());
							finish(null);
						} finally {
							if (gapMs > 0) await new Promise((resolve) => setTimeout(resolve, gapMs));
							running = false;
							pump();
						}
					})();
				},
			});
			// The lowest priority, oldest request goes first when too many wait.
			while (waiting.length > SNAPSHOT_MAX_QUEUED) {
				let worst = 0;
				for (let index = 1; index < waiting.length; index++) {
					const a = waiting[index]!;
					const b = waiting[worst]!;
					if (a.priority < b.priority || (a.priority === b.priority && a.seq < b.seq)) worst = index;
				}
				waiting.splice(worst, 1)[0]?.drop();
			}
			pump();
		});
	}

	return {
		/** The image already cached for the job, or null. */
		async get(job: ArtifactSnapshotJob): Promise<Buffer | null> {
			return read(artifactSnapshotKey(job));
		},
		/**
		 * The job's image, rendered if it is not cached yet; null when it cannot be rendered,
		 * SNAPSHOT_DROPPED when the queue was full and the request should be made again.
		 * A higher `priority` (a card on screen) renders first.
		 */
		async request(job: ArtifactSnapshotJob, priority = 0): Promise<SnapshotResult> {
			const key = artifactSnapshotKey(job);
			const cached = await read(key);
			if (cached) return cached;
			const existing = inFlight.get(key);
			if (existing) {
				const queued = waiting.find((entry) => entry.key === key);
				if (queued && priority > queued.priority) queued.priority = priority;
				return existing;
			}
			const failed = failedAt.get(key);
			if (failed !== undefined && now() - failed < SNAPSHOT_RETRY_MS) return null;
			const pending = enqueue(key, job, priority);
			inFlight.set(key, pending);
			void pending.finally(() => {
				if (inFlight.get(key) === pending) inFlight.delete(key);
			});
			return pending;
		},
		prune,
	};
}

/** Answers the renderer's `get` and `request` for cached artifact previews. */
export function registerArtifactSnapshotIpc(deps: { ipcMain: Pick<IpcMain, "handle">; store: Pick<ArtifactSnapshotStore, "get" | "request"> }): void {
	const asBytes = (bytes: SnapshotResult) => (bytes === SNAPSHOT_DROPPED ? bytes : bytes ? new Uint8Array(bytes.buffer, bytes.byteOffset, bytes.byteLength) : null);
	deps.ipcMain.handle("artifactSnapshot:get", async (_event, value: unknown) => {
		const job = parseArtifactSnapshotJob(value);
		return job ? asBytes(await deps.store.get(job)) : null;
	});
	deps.ipcMain.handle("artifactSnapshot:request", async (_event, value: unknown, priority: unknown) => {
		const job = parseArtifactSnapshotJob(value);
		return job ? asBytes(await deps.store.request(job, priority === 1 ? 1 : 0)) : null;
	});
}
