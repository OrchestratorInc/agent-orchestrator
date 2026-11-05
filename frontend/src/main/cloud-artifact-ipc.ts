/**
 * Reads a cloud session's artifact file for saving.
 *
 * A cloud artifact link is cross-origin to the renderer, and the cloud API
 * bridge carries text bodies only, so the main process reads the file's bytes
 * through the sender's own session. Only the control plane's artifact file
 * route is read.
 */

import { createWriteStream } from "node:fs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import type { ReadableStream as NodeReadableStream } from "node:stream/web";
import type { IpcMain, Session } from "electron";

/** The control plane refuses artifact files over 100 MiB. */
export const MAX_CLOUD_ARTIFACT_BYTES = 100 * 1024 * 1024;

/** At most this many artifact files are downloaded at once into a folder. */
export const CLOUD_ARTIFACT_COPY_CONCURRENCY = 3;

/** A copied session folder older than this is removed when the app starts. */
export const CLOUD_ARTIFACT_COPY_MAX_AGE_MS = 3 * 24 * 60 * 60 * 1000;

/** A half-written copy left by a crash is removed when the app starts once it is this old. */
const STAGING_MAX_AGE_MS = 60 * 60 * 1000;

const ARTIFACT_FILES_ROUTE = "/api/cloud/v1/artifact-files/";

/** A copy is written here first, then moved over the session's folder. */
const STAGING_PREFIX = ".staging-";

/** Request a cloud artifact file through `session`, refusing any other URL and a file declared too large. */
async function requestCloudArtifact(session: Pick<Session, "fetch">, url: string): Promise<Response> {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		throw new Error("Invalid artifact URL");
	}
	if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.pathname.includes(ARTIFACT_FILES_ROUTE)) {
		throw new Error("Not a cloud artifact URL");
	}
	const response = await session.fetch(parsed.toString());
	if (!response.ok) throw new Error(`Artifact request failed (${response.status})`);
	const declared = Number(response.headers.get("content-length"));
	if (Number.isFinite(declared) && declared > MAX_CLOUD_ARTIFACT_BYTES) throw new Error("Artifact is too large");
	return response;
}

/** Fetch a cloud artifact file's bytes through `session`. */
export async function fetchCloudArtifact(session: Pick<Session, "fetch">, url: string): Promise<Uint8Array> {
	const response = await requestCloudArtifact(session, url);
	const bytes = new Uint8Array(await response.arrayBuffer());
	if (bytes.byteLength > MAX_CLOUD_ARTIFACT_BYTES) throw new Error("Artifact is too large");
	return bytes;
}

/**
 * Stream a cloud artifact file through `session` into `target`, so the main
 * process never holds a whole file. A body that grows past the size limit is
 * refused part way, leaving a partial file for the caller to discard.
 */
export async function downloadCloudArtifact(session: Pick<Session, "fetch">, url: string, target: string): Promise<void> {
	const response = await requestCloudArtifact(session, url);
	await fs.mkdir(path.dirname(target), { recursive: true });
	let received = 0;
	const limit = new Transform({
		transform(chunk: Buffer, _encoding, done) {
			received += chunk.byteLength;
			if (received > MAX_CLOUD_ARTIFACT_BYTES) done(new Error("Artifact is too large"));
			else done(null, chunk);
		},
	});
	const body = response.body ? Readable.fromWeb(response.body as unknown as NodeReadableStream<Uint8Array>) : Readable.from([]);
	await pipeline(body, limit, createWriteStream(target));
}

/** Run `task` over `items`, at most `limit` at a time, failing on the first error. */
async function forEachLimited<T>(items: readonly T[], limit: number, task: (item: T) => Promise<void>): Promise<void> {
	let next = 0;
	let failed = false;
	const worker = async () => {
		while (!failed && next < items.length) {
			const item = items[next++];
			try {
				await task(item);
			} catch (error) {
				failed = true;
				throw error;
			}
		}
	};
	await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
}

/** One file of a cloud session's artifacts: its path in the session and its artifact file URL. */
export interface CloudArtifactFolderFile {
	path: string;
	url: string;
}

const SESSION_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** The folder every cloud session's copied artifacts live under: under ~/.ao, never an OS app-data location. */
function cloudArtifactsRoot(env: NodeJS.ProcessEnv): string {
	return path.join(env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"), "cloud-artifacts");
}

/** Where a cloud session's artifacts are copied to open them in a folder: under ~/.ao, never an OS app-data location. */
export function cloudArtifactsFolder(sessionId: string, env: NodeJS.ProcessEnv = process.env): string {
	return path.join(cloudArtifactsRoot(env), sessionId);
}

/** Copies of one session's folder run one after another, so two never race to replace it. */
const copiesInFlight = new Map<string, Promise<unknown>>();

function oneCopyAtATime<T>(sessionId: string, copy: () => Promise<T>): Promise<T> {
	const previous = copiesInFlight.get(sessionId) ?? Promise.resolve();
	const current = previous.catch(() => undefined).then(copy);
	copiesInFlight.set(sessionId, current);
	const forget = () => {
		if (copiesInFlight.get(sessionId) === current) copiesInFlight.delete(sessionId);
	};
	current.then(forget, forget);
	return current;
}

/**
 * Copies the given artifact files of a cloud session into its folder under
 * ~/.ao, replacing what an earlier copy left, and returns the folder (or, with
 * `reveal`, the copied file named by it). A cloud session's artifacts live in
 * the control plane, so this is the folder a local session's "Show in"
 * actions open. Each file is streamed to disk, a few at a time, into a
 * staging folder that replaces the session's folder only once every file has
 * arrived. A path that would leave the folder is refused.
 */
export async function copyCloudArtifactsToFolder(
	session: Pick<Session, "fetch">,
	input: { sessionId: string; files: CloudArtifactFolderFile[]; reveal?: string },
	env: NodeJS.ProcessEnv = process.env,
): Promise<string> {
	if (!SESSION_ID.test(String(input.sessionId))) throw new Error("Invalid session");
	const sessionId = String(input.sessionId);
	const parent = cloudArtifactsRoot(env);
	const root = cloudArtifactsFolder(sessionId, env);
	const files = input.files.map((file) => {
		const target = path.resolve(root, String(file.path));
		if (!target.startsWith(root + path.sep)) throw new Error("Invalid artifact path");
		return { relative: path.relative(root, target), url: String(file.url) };
	});
	await oneCopyAtATime(sessionId, async () => {
		await fs.mkdir(parent, { recursive: true });
		const staging = await fs.mkdtemp(path.join(parent, `${STAGING_PREFIX}${sessionId}-`));
		try {
			await forEachLimited(files, CLOUD_ARTIFACT_COPY_CONCURRENCY, (file) => downloadCloudArtifact(session, file.url, path.join(staging, file.relative)));
			await fs.rm(root, { recursive: true, force: true });
			await fs.rename(staging, root);
		} catch (error) {
			await fs.rm(staging, { recursive: true, force: true });
			throw error;
		}
		// The folder's age is when it was last copied, for the startup prune.
		const now = new Date();
		await fs.utimes(root, now, now);
	});
	if (input.reveal === undefined) return root;
	const revealed = path.resolve(root, input.reveal);
	return revealed.startsWith(root + path.sep) ? revealed : root;
}

/**
 * Removes copied session folders not refreshed for `maxAgeMs`, and copies a
 * crash left half written, so ~/.ao/cloud-artifacts does not grow without
 * bound. Run once when the app starts. Only folders this module writes (a
 * session id, or a staging folder) are touched.
 */
export async function pruneCloudArtifactFolders(
	env: NodeJS.ProcessEnv = process.env,
	now: number = Date.now(),
	maxAgeMs: number = CLOUD_ARTIFACT_COPY_MAX_AGE_MS,
): Promise<string[]> {
	const parent = cloudArtifactsRoot(env);
	let entries: import("node:fs").Dirent[];
	try {
		entries = await fs.readdir(parent, { withFileTypes: true });
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return [];
		throw error;
	}
	const removed: string[] = [];
	await Promise.all(entries.map(async (entry) => {
		const staging = entry.name.startsWith(STAGING_PREFIX);
		if (!entry.isDirectory() || (!staging && !SESSION_ID.test(entry.name))) return;
		const target = path.join(parent, entry.name);
		const { mtimeMs } = await fs.stat(target);
		if (now - mtimeMs <= (staging ? STAGING_MAX_AGE_MS : maxAgeMs)) return;
		await fs.rm(target, { recursive: true, force: true });
		removed.push(entry.name);
	}));
	return removed.sort();
}

export function registerCloudArtifactIpc(deps: {
	ipcMain: Pick<IpcMain, "handle">;
	/** Opens a folder, or shows a file in its folder, in the file manager. */
	openInFileManager?: (target: string, isFile: boolean) => Promise<void> | void;
}): void {
	deps.ipcMain.handle("cloudArtifact:fetch", (event, url: string) => fetchCloudArtifact(event.sender.session, String(url)));
	deps.ipcMain.handle("cloudArtifact:openFolder", async (event, input: { sessionId: string; files: CloudArtifactFolderFile[]; reveal?: string }) => {
		const target = await copyCloudArtifactsToFolder(event.sender.session, input);
		await deps.openInFileManager?.(target, input.reveal !== undefined && target !== cloudArtifactsFolder(input.sessionId));
	});
}
