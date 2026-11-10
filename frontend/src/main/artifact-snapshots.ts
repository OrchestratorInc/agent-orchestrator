/**
 * The disk cache of artifact preview images, and the queue that fills it.
 *
 * Cards load a cached image through the snapshot scheme, which `get` answers
 * from disk; `request` renders what is missing, at most one at a time, and
 * each artifact once however many cards ask.
 * Files live under the app data dir and are pruned oldest-read first, so the
 * cache stays bounded.
 */

import { createHash } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import type { IpcMain, Protocol } from "electron";
import {
	ARTIFACT_SNAPSHOT_SCHEME,
	ARTIFACT_SNAPSHOT_VERSION,
	type ArtifactSnapshotIdentity,
	type ArtifactSnapshotJob,
	parseArtifactSnapshotJob,
	parseArtifactSnapshotUrl,
} from "../shared/artifact-snapshot";

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
export function artifactSnapshotKey(snapshot: ArtifactSnapshotIdentity): string {
	const identity = [ARTIFACT_SNAPSHOT_VERSION, snapshot.hostId ?? "", snapshot.cloud?.orgId ?? "", snapshot.sessionId, snapshot.path, snapshot.updatedAt, snapshot.theme, snapshot.styleTheme];
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
	// Oldest first; the newest request is at the end.
	const waiting: Array<{ start: () => void; drop: () => void }> = [];
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
		if (running) return;
		// The newest first: a card asks once it has been on screen a while, so the last to ask is the one being looked at.
		waiting.pop()?.start();
	}

	function enqueue(job: ArtifactSnapshotJob, key: string): Promise<SnapshotResult> {
		return new Promise((resolve) => {
			waiting.push({
				drop: () => resolve(SNAPSHOT_DROPPED),
				start: () => {
					running = true;
					void (async () => {
						try {
							const bytes = await render(job);
							await write(key, bytes);
							failedAt.delete(key);
							resolve(bytes);
						} catch (error) {
							failedAt.set(key, error instanceof SnapshotUnavailableError ? Number.POSITIVE_INFINITY : now());
							resolve(null);
						} finally {
							if (gapMs > 0) await new Promise((wait) => setTimeout(wait, gapMs));
							running = false;
							pump();
						}
					})();
				},
			});
			// The oldest request goes first when too many wait.
			while (waiting.length > SNAPSHOT_MAX_QUEUED) waiting.shift()?.drop();
			pump();
		});
	}

	return {
		/** The image already cached for the snapshot, or null. */
		async get(snapshot: ArtifactSnapshotIdentity): Promise<Buffer | null> {
			return read(artifactSnapshotKey(snapshot));
		},
		/**
		 * The job's image, rendered if it is not cached yet; null when it cannot be rendered,
		 * SNAPSHOT_DROPPED when the queue was full and the request should be made again.
		 */
		async request(job: ArtifactSnapshotJob): Promise<SnapshotResult> {
			const key = artifactSnapshotKey(job);
			const cached = await read(key);
			if (cached) return cached;
			const existing = inFlight.get(key);
			if (existing) return existing;
			const failed = failedAt.get(key);
			if (failed !== undefined && now() - failed < SNAPSHOT_RETRY_MS) return null;
			const pending = enqueue(job, key);
			inFlight.set(key, pending);
			void pending.finally(() => {
				if (inFlight.get(key) === pending) inFlight.delete(key);
			});
			return pending;
		},
		prune,
	};
}

/** Answers the renderer's request to render a missing snapshot: true once it is cached. */
export function registerArtifactSnapshotIpc(deps: { ipcMain: Pick<IpcMain, "handle">; store: Pick<ArtifactSnapshotStore, "request"> }): void {
	deps.ipcMain.handle("artifactSnapshot:request", async (_event, value: unknown) => {
		const job = parseArtifactSnapshotJob(value);
		if (!job) return false;
		const result = await deps.store.request(job);
		return result !== null && result !== SNAPSHOT_DROPPED;
	});
}

/**
 * Serves cached snapshots to the cards' <img>s, so a cached card costs one image load and no
 * script. A snapshot that is not cached yet is a 404, which the card answers by drawing live.
 * The image never changes for a URL (the URL names the file's version and theme), so it may be
 * kept; a miss may not, as the next visit may find the image rendered.
 */
export function registerArtifactSnapshotProtocol(deps: { protocol: Pick<Protocol, "handle">; store: Pick<ArtifactSnapshotStore, "get"> }): void {
	deps.protocol.handle(ARTIFACT_SNAPSHOT_SCHEME, async (request) => {
		const snapshot = parseArtifactSnapshotUrl(request.url);
		const bytes = snapshot ? await deps.store.get(snapshot) : null;
		if (!bytes) return new Response(null, { status: 404, headers: { "cache-control": "no-store" } });
		return new Response(new Uint8Array(bytes.buffer, bytes.byteOffset, bytes.byteLength), {
			headers: { "content-type": "image/webp", "cache-control": "max-age=31536000, immutable" },
		});
	});
}
