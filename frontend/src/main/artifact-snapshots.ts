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

export const SNAPSHOT_CACHE_MAX_BYTES = 150 * 1024 * 1024;
export const SNAPSHOT_CACHE_MAX_FILES = 4_000;
export const SNAPSHOT_RETRY_MS = 60_000;
export const SNAPSHOT_MAX_QUEUED = 64;
const PRUNE_EVERY_WRITES = 25;
const CLOUD_FOLDER = "cloud";
const OWNER_FILE = "cloud-owner";
export const SNAPSHOT_GAP_MS = 150;

export type SnapshotRenderer = (job: ArtifactSnapshotJob) => Promise<Buffer>;

export class SnapshotUnavailableError extends Error {}

export class SnapshotCancelledError extends Error {}

export const SNAPSHOT_DROPPED = "dropped" as const;
export type SnapshotResult = Buffer | null | typeof SNAPSHOT_DROPPED;

export type ArtifactSnapshotStore = ReturnType<typeof createArtifactSnapshotStore>;

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
	const cloudDirectory = path.join(directory, CLOUD_FOLDER);
	const fileFor = (key: string, cloud: boolean) => path.join(cloud ? cloudDirectory : directory, `${key}.webp`);
	const inFlight = new Map<string, Promise<SnapshotResult>>();
	const failedAt = new Map<string, number>();
	const waiting: Array<{ start: () => void; drop: () => void }> = [];
	let running = false;
	let writes = 0;
	let cloudEpoch = 0;
	let cloudOwner: string | undefined;
	let bindSeq = 0;
	let binding: Promise<void> = Promise.resolve();
	const cloudWrites = new Set<Promise<void>>();
	const ownerFile = path.join(directory, OWNER_FILE);
	let pruning: Promise<void> | undefined;

	async function read(key: string, cloud: boolean): Promise<Buffer | null> {
		try {
			const bytes = await fs.readFile(fileFor(key, cloud));
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
			} finally {
				pruning = undefined;
			}
		})();
		return pruning;
	}

	async function clearCloudFiles(): Promise<void> {
		cloudEpoch += 1;
		await Promise.allSettled([...cloudWrites]);
		await fs.rm(cloudDirectory, { recursive: true, force: true });
	}

	function pump(): void {
		if (running) return;
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
			while (waiting.length > SNAPSHOT_MAX_QUEUED) waiting.shift()?.drop();
			pump();
		});
	}

	return {
		async get(snapshot: ArtifactSnapshotIdentity): Promise<Buffer | null> {
			if (snapshot.cloud && cloudOwner === undefined) return null;
			return read(artifactSnapshotKey(snapshot), Boolean(snapshot.cloud));
		},
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
		cancel(): void {
			while (waiting.length > 0) waiting.pop()?.drop();
		},
		async clearCloud(): Promise<void> {
			cloudOwner = undefined;
			bindSeq += 1;
			await clearCloudFiles();
			await fs.rm(ownerFile, { force: true });
		},
		bindCloudAccount(userId: string | null): Promise<void> {
			const owner = userId ? createHash("sha256").update(userId).digest("hex").slice(0, 32) : "";
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

export function registerArtifactSnapshotIpc(deps: { ipcMain: Pick<IpcMain, "handle">; store: Pick<ArtifactSnapshotStore, "request"> }): void {
	deps.ipcMain.handle("artifactSnapshot:request", async (_event, value: unknown) => {
		const job = parseArtifactSnapshotJob(value);
		if (!job) return false;
		const result = await deps.store.request(job);
		return result !== null && result !== SNAPSHOT_DROPPED;
	});
}

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
