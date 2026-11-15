import { createWriteStream } from "node:fs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import type { ReadableStream as NodeReadableStream } from "node:stream/web";
import type { IpcMain, Session } from "electron";
import { isBrowserArtifactPath } from "../shared/editor-handoff";

export const MAX_CLOUD_ARTIFACT_BYTES = 100 * 1024 * 1024;

export const CLOUD_ARTIFACT_COPY_CONCURRENCY = 3;

export const CLOUD_ARTIFACT_COPY_MAX_AGE_MS = 3 * 24 * 60 * 60 * 1000;

const STAGING_MAX_AGE_MS = 60 * 60 * 1000;

const ARTIFACT_FILES_ROUTE = "/api/cloud/v1/artifact-files/";

const STAGING_PREFIX = ".staging-";

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

export async function fetchCloudArtifact(session: Pick<Session, "fetch">, url: string): Promise<Uint8Array> {
	const response = await requestCloudArtifact(session, url);
	const bytes = new Uint8Array(await response.arrayBuffer());
	if (bytes.byteLength > MAX_CLOUD_ARTIFACT_BYTES) throw new Error("Artifact is too large");
	return bytes;
}

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

export interface CloudArtifactFolderFile {
	path: string;
	url: string;
}

const SESSION_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const SESSION_FOLDER = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(\.browser)?$/i;

function cloudArtifactsRoot(env: NodeJS.ProcessEnv): string {
	return path.join(env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"), "cloud-artifacts");
}

export function cloudArtifactsFolder(sessionId: string, env: NodeJS.ProcessEnv = process.env, browser = false): string {
	return path.join(cloudArtifactsRoot(env), browser ? `${sessionId}.browser` : sessionId);
}

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

export async function copyCloudArtifactsToFolder(
	session: Pick<Session, "fetch">,
	input: { sessionId: string; files: CloudArtifactFolderFile[]; reveal?: string; browser?: boolean },
	env: NodeJS.ProcessEnv = process.env,
): Promise<string> {
	if (!SESSION_ID.test(String(input.sessionId))) throw new Error("Invalid session");
	const sessionId = String(input.sessionId);
	const browser = input.browser === true;
	const parent = cloudArtifactsRoot(env);
	const root = cloudArtifactsFolder(sessionId, env, browser);
	const revealed = input.reveal === undefined ? undefined : path.resolve(root, String(input.reveal));
	const files = input.files.map((file) => {
		const target = path.resolve(root, String(file.path));
		if (!target.startsWith(root + path.sep)) throw new Error("Invalid artifact path");
		return { path: String(file.path), relative: path.relative(root, target), url: String(file.url), required: !browser || target === revealed };
	});
	await oneCopyAtATime(path.basename(root), async () => {
		await fs.mkdir(parent, { recursive: true });
		const staging = await fs.mkdtemp(path.join(parent, `${STAGING_PREFIX}${sessionId}-`));
		try {
			await forEachLimited(files, CLOUD_ARTIFACT_COPY_CONCURRENCY, async (file) => {
				const target = path.join(staging, file.relative);
				try {
					await downloadCloudArtifact(session, file.url, target);
				} catch (error) {
					if (file.required) throw new Error(`Couldn't copy ${file.path}: ${error instanceof Error ? error.message : String(error)}`);
					await fs.rm(target, { force: true });
				}
			});
			await fs.rm(root, { recursive: true, force: true });
			await fs.rename(staging, root);
		} catch (error) {
			await fs.rm(staging, { recursive: true, force: true });
			throw error;
		}
		const now = new Date();
		await fs.utimes(root, now, now);
	});
	if (revealed === undefined) return root;
	return revealed.startsWith(root + path.sep) ? revealed : root;
}

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
		if (!entry.isDirectory() || (!staging && !SESSION_FOLDER.test(entry.name))) return;
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
	openInFileManager?: (target: string, isFile: boolean) => Promise<void> | void;
	openInBrowser?: (target: string) => Promise<void> | void;
}): void {
	deps.ipcMain.handle("cloudArtifact:fetch", (event, url: string) => fetchCloudArtifact(event.sender.session, String(url)));
	deps.ipcMain.handle("cloudArtifact:openFolder", async (event, input: { sessionId: string; files: CloudArtifactFolderFile[]; reveal?: string; open?: "browser" }) => {
		const browser = input.open === "browser";
		if (browser && (input.reveal === undefined || !isBrowserArtifactPath(String(input.reveal)))) throw new Error("This file does not open in a browser.");
		if (browser && !deps.openInBrowser) throw new Error("Opening in a browser is not available.");
		const target = await copyCloudArtifactsToFolder(event.sender.session, { ...input, browser });
		const isFile = input.reveal !== undefined && target !== cloudArtifactsFolder(input.sessionId, process.env, browser);
		if (browser) {
			if (!isFile) throw new Error("That artifact is not valid.");
			await deps.openInBrowser?.(target);
			return;
		}
		await deps.openInFileManager?.(target, isFile);
	});
}
