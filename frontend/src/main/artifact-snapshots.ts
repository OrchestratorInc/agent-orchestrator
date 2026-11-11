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
const CLOUD_FOLDER = "cloud";
/** Names the account the Cloud folder belongs to (a hash of its user id), so another account never sees its files. */
const OWNER_FILE = "cloud-owner";
/** A pause after each render, so a long queue leaves the app room to draw and scroll. */
export const SNAPSHOT_GAP_MS = 150;

export type SnapshotRenderer = (job: ArtifactSnapshotJob) => Promise<Buffer>;

/** A file with nothing to draw: asked again it would only fail again, so it is not retried until the app restarts. */
export class SnapshotUnavailableError extends Error {}

/** A render that did not happen because nothing is left to show it (the app window closed): not held against its file. */
export class SnapshotCancelledError extends Error {}

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
	// Cloud snapshots live in their own folder so signing out can remove them all at once.
	const cloudDirectory = path.join(directory, CLOUD_FOLDER);
	const fileFor = (key: string, cloud: boolean) => path.join(cloud ? cloudDirectory : directory, `${key}.webp`);
	const inFlight = new Map<string, Promise<SnapshotResult>>();
	const failedAt = new Map<string, number>();
	// Oldest first; the newest request is at the end.
	const waiting: Array<{ start: () => void; drop: () => void }> = [];
	// One render at a time: every job shares the renderer's single window, and starting a job
	// unmounts the previous one's preview, so two at once would wipe each other mid-render.
	let running = false;
	let writes = 0;
	// Bumped whenever the Cloud snapshots are cleared or change owner, so a render that began for an
	// earlier account is not written after it.
	let cloudEpoch = 0;
	// The account the Cloud folder belongs to, once known: until then (and when signed out) no Cloud snapshot is served or made.
	let cloudOwner: string | undefined;
	let bindSeq = 0;
	let binding: Promise<void> = Promise.resolve();
	// Cloud writes in flight, which a clear waits for so none lands after it.
	const cloudWrites = new Set<Promise<void>>();
	const ownerFile = path.join(directory, OWNER_FILE);
	let pruning: Promise<void> | undefined;

	async function read(key: string, cloud: boolean): Promise<Buffer | null> {
		try {
			const bytes = await fs.readFile(fileFor(key, cloud));
			// Reading marks the file as recent for pruning; a failure to is harmless.
			const at = new Date();
			void fs.utimes(fileFor(key, cloud), at, at).catch(() => undefined);
			return bytes;
		} catch {
			return null;
		}
	}

	async function write(key: string, bytes: Buffer, cloud: boolean): Promise<void> {
		const target = fileFor(key, cloud);
		await fs.mkdir(path.dirname(target), { recursive: true });
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
				const listed = await Promise.all([directory, cloudDirectory].map(async (folder) => {
					const names = await fs.readdir(folder).catch(() => [] as string[]);
					return Promise.all(names.filter((name) => name.endsWith(".webp") || name.endsWith(".tmp")).map(async (name) => {
						const file = path.join(folder, name);
						try {
							const stat = await fs.stat(file);
							return { file, size: stat.size, at: stat.mtimeMs };
						} catch {
							return undefined;
						}
					}));
				}));
				const files = listed.flat().filter((entry): entry is { file: string; size: number; at: number } => entry !== undefined);
				// A staging file left by a crash is garbage once it is not being written.
				const stale = files.filter((entry) => entry.file.endsWith(".tmp") && now() - entry.at > 60_000);
				await Promise.all(stale.map((entry) => fs.rm(entry.file, { force: true })));
				const kept = files.filter((entry) => entry.file.endsWith(".webp")).sort((x, y) => x.at - y.at);
				let bytes = kept.reduce((total, entry) => total + entry.size, 0);
				let count = kept.length;
				for (const entry of kept) {
					if (bytes <= SNAPSHOT_CACHE_MAX_BYTES && count <= SNAPSHOT_CACHE_MAX_FILES) break;
					await fs.rm(entry.file, { force: true });
					bytes -= entry.size;
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

	/** Deletes the Cloud folder once the writes already under way have finished, so none recreates it. */
	async function clearCloudFiles(): Promise<void> {
		cloudEpoch += 1;
		await Promise.allSettled([...cloudWrites]);
		await fs.rm(cloudDirectory, { recursive: true, force: true });
	}

	function pump(): void {
		if (running) return;
		// The newest first: a card asks once it has been on screen a while, so the last to ask is the one being looked at.
		waiting.pop()?.start();
	}

	function enqueue(job: ArtifactSnapshotJob, key: string): Promise<SnapshotResult> {
		const cloud = Boolean(job.cloud);
		const epoch = cloudEpoch;
		const signedOut = () => cloud && (epoch !== cloudEpoch || cloudOwner === undefined);
		return new Promise((resolve) => {
			waiting.push({
				drop: () => resolve(SNAPSHOT_DROPPED),
				start: () => {
					running = true;
					void (async () => {
						try {
							if (signedOut()) throw new Error("signed out before the render began");
							const bytes = await render(job);
							if (signedOut()) throw new Error("signed out during the render");
							const writing = write(key, bytes, cloud);
							if (cloud) {
								cloudWrites.add(writing);
								const done = () => void cloudWrites.delete(writing);
								writing.then(done, done);
							}
							await writing;
							failedAt.delete(key);
							resolve(bytes);
						} catch (error) {
							if (!(error instanceof SnapshotCancelledError)) failedAt.set(key, error instanceof SnapshotUnavailableError ? Number.POSITIVE_INFINITY : now());
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
			if (snapshot.cloud && cloudOwner === undefined) return null;
			return read(artifactSnapshotKey(snapshot), Boolean(snapshot.cloud));
		},
		/**
		 * The job's image, rendered if it is not cached yet; null when it cannot be rendered,
		 * SNAPSHOT_DROPPED when the queue was full and the request should be made again.
		 */
		async request(job: ArtifactSnapshotJob): Promise<SnapshotResult> {
			if (job.cloud && cloudOwner === undefined) return null;
			const key = artifactSnapshotKey(job);
			const cached = await read(key, Boolean(job.cloud));
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
		/**
		 * Drops every request still waiting, each answering as dropped, for when nothing is left to
		 * show the result (the app window closed): the queue must not go on opening windows. A render
		 * already under way finishes or fails by itself.
		 */
		cancel(): void {
			while (waiting.length > 0) waiting.pop()?.drop();
		},
		/** Deletes every Cloud snapshot, for a sign-out: nothing of an account's content stays on disk. */
		async clearCloud(): Promise<void> {
			cloudOwner = undefined;
			bindSeq += 1;
			await clearCloudFiles();
			await fs.rm(ownerFile, { force: true });
		},
		/**
		 * Tells the store which account is signed in, or null for none. The Cloud folder belongs to
		 * one account: when it was left by another (or by none), it is deleted first, and Cloud
		 * snapshots are served only once that has worked. A failed delete leaves the old owner on
		 * record, so the next call (a restart, a new sign-in) tries again, and rejects so the caller can report it.
		 */
		bindCloudAccount(userId: string | null): Promise<void> {
			const owner = userId ? createHash("sha256").update(userId).digest("hex").slice(0, 32) : "";
			// The same account again, with nothing pending (a session refresh says so too): nothing changes, and
			// Cloud renders under way and requests keep going. While a bind is pending the owner is unset, so
			// that never counts as settled, and a lost or different account always takes the full path.
			if (owner && cloudOwner === owner) return Promise.resolve();
			const seq = (bindSeq += 1);
			cloudOwner = undefined;
			cloudEpoch += 1;
			const run = binding.then(async () => {
				const recorded = ((await fs.readFile(ownerFile, "utf8").catch(() => "")) ?? "").trim();
				if (recorded !== owner) {
					await clearCloudFiles();
					if (owner) {
						await fs.mkdir(directory, { recursive: true });
						await fs.writeFile(ownerFile, owner);
					} else {
						await fs.rm(ownerFile, { force: true });
					}
				}
				if (seq === bindSeq && owner) cloudOwner = owner;
			});
			binding = run.catch(() => undefined);
			return run;
		},
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
		return new Response(new Uint8Array(bytes), {
			headers: { "content-type": "image/webp", "cache-control": "max-age=31536000, immutable" },
		});
	});
}
