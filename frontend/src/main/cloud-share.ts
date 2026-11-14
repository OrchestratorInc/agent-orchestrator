import { createHash, randomUUID } from "node:crypto";
import { constants as fsConstants, promises as fs } from "node:fs";
import path from "node:path";
import type { CloudShareAccess, CloudShareGrant, CloudShareLink, CloudShareLinks } from "../shared/cloud-share";
import { parseCloudBaseUrl } from "./cloud-cp-proxy";

const API_PREFIX = "/api/cloud/v1";
export const VERSIONS_DIR = ".versions";
const SESSION_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const READ_CHUNK = 1 << 20;
const PRESIGN_BATCH = 8;
const UPLOAD_CONCURRENCY = 2;
const CHROMIUM_MANAGED_HEADERS = new Set(["content-length", "host"]);

function partHeaders(headers: Record<string, string>): Record<string, string> {
	return Object.fromEntries(Object.entries(headers).filter(([name]) => !CHROMIUM_MANAGED_HEADERS.has(name.toLowerCase())));
}

export class CloudShareError extends Error {
	constructor(
		readonly code: string,
		message: string,
		readonly status = 0,
	) {
		super(message);
		this.name = "CloudShareError";
	}
}

export interface ResolvedShareFile {
	realPath: string;
	name: string;
	relativePath: string;
	size: number;
	dev: number;
	ino: number;
}

export async function resolveShareFile(dataDir: string, sessionId: unknown, relPath: unknown): Promise<ResolvedShareFile> {
	if (typeof sessionId !== "string" || !SESSION_ID_PATTERN.test(sessionId) || sessionId === "." || sessionId === "..") {
		throw new CloudShareError("invalid_file", "Not a session's artifact.");
	}
	if (typeof relPath !== "string" || relPath === "" || relPath.includes("\0") || path.isAbsolute(relPath) || path.win32.isAbsolute(relPath)) {
		throw new CloudShareError("invalid_file", "Not a file in the session's artifacts.");
	}
	let realRoot: string;
	let realFile: string;
	try {
		realRoot = await fs.realpath(path.join(dataDir, "artifacts", sessionId));
		realFile = await fs.realpath(path.resolve(realRoot, relPath));
	} catch {
		throw new CloudShareError("file_missing", "The file is no longer available.");
	}
	const relative = path.relative(realRoot, realFile);
	if (relative === "" || relative.startsWith("..") || path.isAbsolute(relative)) {
		throw new CloudShareError("invalid_file", "Only files in the session's artifacts folder can be shared.");
	}
	const segments = relative.split(path.sep);
	if (segments[0] === VERSIONS_DIR) {
		throw new CloudShareError("invalid_file", "Version history cannot be shared.");
	}
	const stat = await fs.stat(realFile);
	if (!stat.isFile()) throw new CloudShareError("invalid_file", "Only files can be shared.");
	return { realPath: realFile, name: path.basename(realFile), relativePath: segments.join("/"), size: stat.size, dev: stat.dev, ino: stat.ino };
}

export function shareFileFingerprint(stat: { size: number; mtimeMs: number }): string {
	return `${stat.size}:${Math.trunc(stat.mtimeMs)}`;
}

export async function statShareFile(file: ResolvedShareFile): Promise<string> {
	const handle = await openResolved(file);
	try {
		return shareFileFingerprint(await handle.stat());
	} finally {
		await handle.close();
	}
}

export interface ShareSnapshot {
	path: string;
	size: number;
	sha256: string;
	fingerprint: string;
}

export interface SnapshotOptions {
	signal?: AbortSignal;
	onProgress?: (copiedBytes: number) => void;
}

export async function snapshotShareFile(file: ResolvedShareFile, snapshotDir: string, options: SnapshotOptions = {}): Promise<ShareSnapshot> {
	await fs.mkdir(snapshotDir, { recursive: true, mode: 0o700 });
	const free = await fs.statfs(snapshotDir).then((stats) => stats.bavail * stats.bsize, () => Number.POSITIVE_INFINITY);
	if (free < file.size) throw new CloudShareError("disk_full", "There is not enough free disk space to prepare this file for sharing.");
	const target = path.join(snapshotDir, randomUUID());
	const source = await openResolved(file);
	let output: fs.FileHandle | undefined;
	try {
		const stat = await source.stat();
		output = await fs.open(target, "wx", 0o600);
		const hash = createHash("sha256");
		const buffer = Buffer.alloc(READ_CHUNK);
		let size = 0;
		for (;;) {
			if (options.signal?.aborted) throw new CloudShareError("cancelled", "Sharing was cancelled.");
			const { bytesRead } = await source.read(buffer, 0, READ_CHUNK, null);
			if (bytesRead === 0) break;
			const chunk = buffer.subarray(0, bytesRead);
			hash.update(chunk);
			await output.write(chunk);
			size += bytesRead;
			options.onProgress?.(size);
		}
		const after = await source.stat();
		if (size !== stat.size || after.size !== stat.size || after.mtimeMs !== stat.mtimeMs || after.ctimeMs !== stat.ctimeMs) {
			throw new CloudShareError("source_changed", "The file changed while it was being prepared for sharing.");
		}
		return { path: target, size, sha256: hash.digest("hex"), fingerprint: shareFileFingerprint(stat) };
	} catch (error) {
		await output?.close().catch(() => undefined);
		output = undefined;
		await fs.rm(target, { force: true });
		throw error;
	} finally {
		await source.close();
		await output?.close();
	}
}

export async function clearShareSnapshots(snapshotDir: string): Promise<void> {
	await fs.rm(snapshotDir, { recursive: true, force: true });
}

async function openResolved(file: ResolvedShareFile): Promise<fs.FileHandle> {
	const handle = await fs.open(file.realPath, fsConstants.O_RDONLY | (fsConstants.O_NOFOLLOW ?? 0));
	try {
		const stat = await handle.stat();
		if (!stat.isFile()) throw new CloudShareError("invalid_file", "Only files can be shared.");
		if (stat.dev !== file.dev || stat.ino !== file.ino) {
			throw new CloudShareError("invalid_file", "The file changed while it was being shared.");
		}
		return handle;
	} catch (error) {
		await handle.close();
		throw error;
	}
}

export interface CloudShareApi {
	fetch: (url: string, init: RequestInit) => Promise<Response>;
	getToken: () => Promise<string | null>;
}

function apiUrl(baseUrl: string, apiPath: string): string {
	const base = parseCloudBaseUrl(baseUrl);
	return base.origin + base.pathname.replace(/\/+$/, "") + API_PREFIX + apiPath;
}

export const SHARE_TIMEOUTS: Record<"api" | "probe" | "transfer" | "publish", number> = {
	api: 30_000,
	probe: 10_000,
	transfer: 5 * 60_000,
	publish: 15 * 60_000,
};

async function fetchWithin<T>(
	timeoutMs: number,
	signal: AbortSignal | undefined,
	request: (signal: AbortSignal) => Promise<T>,
): Promise<T> {
	const timeout = AbortSignal.timeout(timeoutMs);
	try {
		return await request(signal ? AbortSignal.any([signal, timeout]) : timeout);
	} catch (error) {
		if (timeout.aborted && !signal?.aborted) {
			throw new CloudShareError("timeout", "Orchestrator Cloud didn't answer in time. Check your connection and try again.");
		}
		throw error;
	}
}

function untilAborted<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
	return new Promise<T>((resolve, reject) => {
		const stop = () => reject(signal.reason);
		promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", stop));
		if (signal.aborted) return stop();
		signal.addEventListener("abort", stop, { once: true });
	});
}

async function callApi<T>(
	api: CloudShareApi,
	baseUrl: string,
	method: string,
	apiPath: string,
	body?: unknown,
	signal?: AbortSignal,
	timeoutMs: number = SHARE_TIMEOUTS.api,
): Promise<T> {
	return fetchWithin(timeoutMs, signal, async (limited) => {
		const token = await untilAborted(api.getToken(), limited);
		if (!token) throw new CloudShareError("signed_out", "Sign in to share.");
		const response = await api.fetch(apiUrl(baseUrl, apiPath), {
			method,
			headers: { Authorization: `Bearer ${token}`, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
			body: body === undefined ? undefined : JSON.stringify(body),
			signal: limited,
		});
		if (response.status === 401) throw new CloudShareError("signed_out", "Sign in to share.", 401);
		if (!response.ok) {
			const envelope = (await response.json().catch(() => ({}))) as { code?: string; message?: string };
			throw new CloudShareError(envelope.code || `http_${response.status}`, envelope.message || `Request failed (${response.status})`, response.status);
		}
		if (response.status === 204) return undefined as T;
		return (await response.json()) as T;
	});
}

export async function shareLinksAvailable(api: CloudShareApi, baseUrl: string): Promise<boolean | null> {
	const limited = AbortSignal.timeout(SHARE_TIMEOUTS.probe);
	try {
		const token = await untilAborted(api.getToken(), limited);
		const response = await api.fetch(apiUrl(baseUrl, "/me/artifact-shares"), {
			method: "GET",
			headers: token ? { Authorization: `Bearer ${token}` } : {},
			signal: limited,
		});
		await response.body?.cancel().catch(() => undefined);
		return response.status !== 404;
	} catch {
		return null;
	}
}

export function listShareLinks(api: CloudShareApi, baseUrl: string): Promise<CloudShareLinks> {
	return callApi<CloudShareLinks>(api, baseUrl, "GET", "/me/artifact-shares").then((links) => ({
		...links,
		unlimited: Boolean(links.unlimited),
		privateLinks: Boolean(links.privateLinks),
	}));
}

function shareLinkPath(shareId: string): string {
	if (!UUID_PATTERN.test(shareId)) throw new CloudShareError("invalid_request", "Not a share link.");
	return `/me/artifact-shares/${shareId}`;
}

export async function unpublishShareLink(api: CloudShareApi, baseUrl: string, shareId: string): Promise<void> {
	await callApi<void>(api, baseUrl, "DELETE", shareLinkPath(shareId));
}

export function setShareLinkAccess(api: CloudShareApi, baseUrl: string, shareId: string, access: CloudShareAccess): Promise<CloudShareLink> {
	return callApi<CloudShareLink>(api, baseUrl, "PATCH", shareLinkPath(shareId), { access });
}

export async function listShareGrants(api: CloudShareApi, baseUrl: string, shareId: string): Promise<CloudShareGrant[]> {
	return (await callApi<{ grants: CloudShareGrant[] }>(api, baseUrl, "GET", `${shareLinkPath(shareId)}/grants`)).grants;
}

export async function inviteShareGrants(api: CloudShareApi, baseUrl: string, shareId: string, emails: string[]): Promise<CloudShareGrant[]> {
	return (await callApi<{ grants: CloudShareGrant[] }>(api, baseUrl, "POST", `${shareLinkPath(shareId)}/grants`, { emails })).grants;
}

export async function removeShareGrant(api: CloudShareApi, baseUrl: string, shareId: string, grantId: string): Promise<void> {
	if (!UUID_PATTERN.test(grantId)) throw new CloudShareError("invalid_request", "Not someone the link is shared with.");
	await callApi<void>(api, baseUrl, "DELETE", `${shareLinkPath(shareId)}/grants/${grantId}`);
}

export interface ShareAccessInput {
	access?: CloudShareAccess;
	emails?: string[];
}

function accessFields(input: ShareAccessInput): ShareAccessInput {
	return {
		...(input.access ? { access: input.access } : {}),
		...(input.emails && input.emails.length > 0 ? { emails: input.emails } : {}),
	};
}

export interface CloudSessionArtifactRef {
	orgId: string;
	sessionId: string;
	path: string;
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function sessionApiPath(ref: CloudSessionArtifactRef): string {
	if (!UUID_PATTERN.test(ref.orgId) || !UUID_PATTERN.test(ref.sessionId)) {
		throw new CloudShareError("invalid_file", "Not a cloud session's artifact.");
	}
	return `/orgs/${ref.orgId}/sessions/${ref.sessionId}`;
}

export async function cloudSessionArtifactVersion(
	api: CloudShareApi,
	baseUrl: string,
	ref: CloudSessionArtifactRef,
): Promise<{ name: string; size: number; sha256: string }> {
	const { artifacts } = await callApi<{ artifacts: Array<{ path: string; name: string; size: number; sha256: string }> }>(api, baseUrl, "GET", `${sessionApiPath(ref)}/artifacts`);
	const artifact = artifacts.find((candidate) => candidate.path === ref.path);
	if (!artifact) throw new CloudShareError("file_missing", "The file is no longer available.");
	return { name: artifact.path.split("/").pop() || artifact.name, size: artifact.size, sha256: artifact.sha256 };
}

export function shareCloudSessionArtifact(
	api: CloudShareApi,
	baseUrl: string,
	ref: CloudSessionArtifactRef & ShareAccessInput & { sha256: string; shareId?: string },
	signal?: AbortSignal,
): Promise<{ share: CloudShareLink; replaced: boolean }> {
	return callApi<{ share: CloudShareLink; replaced: boolean }>(api, baseUrl, "POST", `${sessionApiPath(ref)}/artifacts/share`,
		{ path: ref.path, sha256: ref.sha256, ...(ref.shareId ? { shareId: ref.shareId } : {}), ...accessFields(ref) }, signal, SHARE_TIMEOUTS.publish);
}

export interface ShareUploadInput extends ShareAccessInput {
	baseUrl: string;
	snapshot: ShareSnapshot;
	name: string;
	project: string;
	feature: string;
	shareId?: string;
}

interface UploadStart {
	uploadToken: string;
	partSize: number;
	partCount: number;
}

interface PresignedPart {
	partNumber: number;
	method: string;
	url: string;
	headers: Record<string, string>;
	size: number;
}

function partRange(start: UploadStart, size: number, number: number): { offset: number; length: number } {
	const offset = (number - 1) * start.partSize;
	return { offset, length: Math.max(0, Math.min(start.partSize, size - offset)) };
}

async function hashRange(handle: fs.FileHandle, range: { offset: number; length: number }): Promise<string> {
	const hash = createHash("sha256");
	const buffer = Buffer.alloc(Math.min(READ_CHUNK, Math.max(range.length, 1)));
	for (let done = 0; done < range.length; ) {
		const { bytesRead } = await handle.read(buffer, 0, Math.min(buffer.length, range.length - done), range.offset + done);
		if (bytesRead === 0) throw new CloudShareError("upload_failed", "The snapshot ended early.");
		hash.update(buffer.subarray(0, bytesRead));
		done += bytesRead;
	}
	return hash.digest("hex");
}

export async function uploadShare(
	api: CloudShareApi,
	input: ShareUploadInput,
	onProgress: (sentBytes: number) => void,
	signal?: AbortSignal,
): Promise<{ share: CloudShareLink; replaced: boolean }> {
	const { baseUrl, snapshot } = input;
	const start = await callApi<UploadStart>(api, baseUrl, "POST", "/me/artifact-shares/uploads", {
		name: input.name, size: snapshot.size, sha256: snapshot.sha256, project: input.project, feature: input.feature,
		...(input.shareId ? { shareId: input.shareId } : {}),
	}, signal);
	const handle = await fs.open(snapshot.path, "r");
	const batch = new AbortController();
	const stop = () => batch.abort();
	signal?.addEventListener("abort", stop, { once: true });
	const running: Array<Promise<void>> = [];
	try {
		const completed: Array<{ partNumber: number; sha256: string; etag: string }> = [];
		let sent = 0;
		for (let first = 1; first <= start.partCount; first += PRESIGN_BATCH) {
			const numbers = Array.from({ length: Math.min(PRESIGN_BATCH, start.partCount - first + 1) }, (_, index) => first + index);
			const parts: Array<{ partNumber: number; sha256: string }> = [];
			for (const number of numbers) {
				parts.push({ partNumber: number, sha256: await hashRange(handle, partRange(start, snapshot.size, number)) });
			}
			const presigned = await callApi<{ parts: PresignedPart[] }>(api, baseUrl, "POST", "/me/artifact-shares/uploads/parts",
				{ uploadToken: start.uploadToken, parts }, signal);
			const queue = [...presigned.parts];
			const worker = async () => {
				for (let part = queue.shift(); part && !batch.signal.aborted; part = queue.shift()) {
					const signed = parts.find((candidate) => candidate.partNumber === part!.partNumber);
					if (!signed) throw new CloudShareError("upload_failed", "The upload was signed for another part.");
					const range = partRange(start, snapshot.size, part.partNumber);
					const body = Buffer.alloc(range.length);
					await handle.read(body, 0, range.length, range.offset);
					const response = await fetchWithin(SHARE_TIMEOUTS.transfer, batch.signal, (signal) =>
						api.fetch(part.url, { method: part.method, headers: partHeaders(part.headers), body, signal }));
					const etag = response.headers.get("etag");
					if (!response.ok || !etag) throw new CloudShareError("upload_failed", `Uploading part ${part.partNumber} failed (${response.status}).`, response.status);
					completed.push({ partNumber: part.partNumber, sha256: signed.sha256, etag });
					sent += body.byteLength;
					onProgress(sent);
				}
			};
			const workers = Array.from({ length: UPLOAD_CONCURRENCY }, worker);
			running.push(...workers);
			await Promise.all(workers);
		}
		completed.sort((a, b) => a.partNumber - b.partNumber);
		return await callApi<{ share: CloudShareLink; replaced: boolean }>(api, baseUrl, "POST", "/me/artifact-shares/finalize",
			{ uploadToken: start.uploadToken, parts: completed, ...accessFields(input) }, signal, SHARE_TIMEOUTS.publish);
	} catch (error) {
		batch.abort();
		await Promise.allSettled(running);
		await callApi<void>(api, baseUrl, "POST", "/me/artifact-shares/uploads/abort", { uploadToken: start.uploadToken }).catch(() => undefined);
		throw error;
	} finally {
		signal?.removeEventListener("abort", stop);
		await handle.close();
	}
}
