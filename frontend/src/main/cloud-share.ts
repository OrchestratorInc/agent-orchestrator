/**
 * Publishing a local artifact as a public share link: the main-process half.
 *
 * Only a person publishes. The renderer names a file; this module resolves it
 * strictly inside the session's artifacts folder, copies it once into a
 * private snapshot while hashing it and looking for credentials, and uploads
 * that snapshot straight to storage through the control plane's presigned
 * multipart upload. The bytes that go public are exactly the ones hashed and
 * confirmed, even if an agent rewrites the file meanwhile. The sign-in token
 * never leaves this process.
 */

import { createHash, randomUUID } from "node:crypto";
import { constants as fsConstants, promises as fs } from "node:fs";
import path from "node:path";
import type { CloudShareLink, CloudShareLinks } from "../shared/cloud-share";
import { parseCloudBaseUrl } from "./cloud-cp-proxy";

const API_PREFIX = "/api/cloud/v1";
/** The folder in a session's artifacts that holds version history; never shared. */
export const VERSIONS_DIR = ".versions";
const SESSION_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const READ_CHUNK = 1 << 20;
/** Only the start of a large file is scanned for credentials. */
export const MAX_SCANNED_BYTES = 32 << 20;
const SCAN_OVERLAP = 512;
/** Parts presigned per request; the control plane allows up to 100. */
const PRESIGN_BATCH = 8;
/** Parts uploaded at once. */
const UPLOAD_CONCURRENCY = 2;
/**
 * Headers Chromium sets itself: net.fetch refuses a request that names them
 * (net::ERR_INVALID_ARGUMENT). The body's own length is the signed one.
 */
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
	/** The file's real path, inside the session's artifacts folder. */
	realPath: string;
	name: string;
	/** Slash-separated path in the artifacts folder. */
	relativePath: string;
	size: number;
	/** The file's identity when it resolved: a later open must find the same file. */
	dev: number;
	ino: number;
}

/**
 * Resolves a file in a local session's artifacts folder,
 * <dataDir>/artifacts/<sessionId>. Anything that resolves outside it, through
 * "..", an absolute path or a symlink, is refused, as is the version history
 * folder and anything that is not a regular file.
 */
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

const SECRET_PATTERNS: Array<[string, RegExp]> = [
	["AWS access key", /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/],
	["private key", /-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----/],
	["GitHub token", /\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{40,})/],
	["Slack token", /\bxox[abprs]-[A-Za-z0-9-]{10,}/],
	["Anthropic API key", /\bsk-ant-[A-Za-z0-9_-]{20,}/],
	["OpenAI API key", /\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}/],
	["Stripe secret key", /\b[rs]k_live_[A-Za-z0-9]{20,}/],
	["Google API key", /\bAIza[0-9A-Za-z_-]{35}/],
	// Also catches DB_PASSWORD=..., client_secret: ..., x-api-key: ....
	["password or secret", /(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token)[A-Za-z0-9_]*["']?\s*[:=]\s*["']?[^\s"'<>]{8,}/i],
];

const SECRET_FILE_NAMES: Array<[string, RegExp]> = [
	["environment file", /^\.env(?:\..+)?$/i],
	["SSH key", /^id_(?:rsa|dsa|ecdsa|ed25519)(?:\.pub)?$/i],
	["key or certificate file", /\.(?:pem|key|p12|pfx|jks|keystore)$/i],
	["credentials file", /^(?:credentials(?:\.json)?|\.npmrc|\.netrc|\.pgpass)$/i],
];

/** Finds kinds of credential in a stream of bytes, across chunk boundaries. */
export class SecretScanner {
	private tail = "";
	private scanned = 0;
	readonly findings = new Set<string>();

	constructor(name: string) {
		for (const [kind, pattern] of SECRET_FILE_NAMES) if (pattern.test(name)) this.findings.add(kind);
	}

	push(chunk: Uint8Array): void {
		if (this.scanned >= MAX_SCANNED_BYTES) return;
		const take = chunk.subarray(0, MAX_SCANNED_BYTES - this.scanned);
		this.scanned += take.byteLength;
		// latin1 maps every byte to one character, so no match splits a code point.
		const text = this.tail + Buffer.from(take.buffer, take.byteOffset, take.byteLength).toString("latin1");
		for (const [kind, pattern] of SECRET_PATTERNS) if (pattern.test(text)) this.findings.add(kind);
		this.tail = text.slice(-SCAN_OVERLAP);
	}

	list(): string[] {
		return [...this.findings].sort();
	}
}

/**
 * What the person was shown about a file: the hash of its scanned start, its
 * size and its modification time. Publishing refuses a file whose
 * fingerprint changed since, so the bytes that go public are the ones whose
 * scan the person saw.
 */
class FingerprintHash {
	private readonly hash = createHash("sha256");
	private hashed = 0;

	push(chunk: Uint8Array): void {
		if (this.hashed >= MAX_SCANNED_BYTES) return;
		const take = chunk.subarray(0, MAX_SCANNED_BYTES - this.hashed);
		this.hash.update(take);
		this.hashed += take.byteLength;
	}

	digest(stat: { size: number; mtimeMs: number }): string {
		return `${this.hash.digest("hex")}:${stat.size}:${Math.trunc(stat.mtimeMs)}`;
	}
}

/** Reads a file's start once: the credentials it looks like it holds, and its fingerprint. */
export async function scanShareFile(file: ResolvedShareFile): Promise<{ secretFindings: string[]; fingerprint: string }> {
	const scanner = new SecretScanner(file.name);
	const fingerprint = new FingerprintHash();
	const handle = await openResolved(file);
	try {
		const stat = await handle.stat();
		const buffer = Buffer.alloc(READ_CHUNK);
		for (let read = 0; read < MAX_SCANNED_BYTES; ) {
			const { bytesRead } = await handle.read(buffer, 0, READ_CHUNK, null);
			if (bytesRead === 0) break;
			const chunk = buffer.subarray(0, bytesRead);
			scanner.push(chunk);
			fingerprint.push(chunk);
			read += bytesRead;
		}
		return { secretFindings: scanner.list(), fingerprint: fingerprint.digest(stat) };
	} finally {
		await handle.close();
	}
}

export interface ShareSnapshot {
	path: string;
	size: number;
	sha256: string;
	secretFindings: string[];
	/** The source's fingerprint as it was copied; see scanShareFile. */
	fingerprint: string;
}

export interface SnapshotOptions {
	/** Stops the copy between chunks. */
	signal?: AbortSignal;
	/** Bytes copied so far. */
	onProgress?: (copiedBytes: number) => void;
}

/**
 * Copies a shared file into a private snapshot under snapshotDir, hashing it
 * and scanning it as it goes. The upload reads only the snapshot. The copy
 * needs as much free space as the file, which is checked first.
 */
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
		const scanner = new SecretScanner(file.name);
		const fingerprint = new FingerprintHash();
		const buffer = Buffer.alloc(READ_CHUNK);
		let size = 0;
		for (;;) {
			if (options.signal?.aborted) throw new CloudShareError("cancelled", "Sharing was cancelled.");
			const { bytesRead } = await source.read(buffer, 0, READ_CHUNK, null);
			if (bytesRead === 0) break;
			const chunk = buffer.subarray(0, bytesRead);
			hash.update(chunk);
			scanner.push(chunk);
			fingerprint.push(chunk);
			await output.write(chunk);
			size += bytesRead;
			options.onProgress?.(size);
		}
		return { path: target, size, sha256: hash.digest("hex"), secretFindings: scanner.list(), fingerprint: fingerprint.digest(stat) };
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

/** Removes snapshots left by an earlier run. */
export async function clearShareSnapshots(snapshotDir: string): Promise<void> {
	await fs.rm(snapshotDir, { recursive: true, force: true });
}

/**
 * Opens a resolved file, refusing anything but the very file that resolved.
 * O_NOFOLLOW refuses a final symlink swapped in since; comparing the opened
 * file's device and inode with the resolved ones refuses a folder on the way
 * swapped for a symlink, which O_NOFOLLOW does not see.
 */
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
	/** The sign-in token, or null when signed out. */
	getToken: () => Promise<string | null>;
}

function apiUrl(baseUrl: string, apiPath: string): string {
	const base = parseCloudBaseUrl(baseUrl);
	return base.origin + base.pathname.replace(/\/+$/, "") + API_PREFIX + apiPath;
}

async function callApi<T>(api: CloudShareApi, baseUrl: string, method: string, apiPath: string, body?: unknown, signal?: AbortSignal): Promise<T> {
	const token = await api.getToken();
	if (!token) throw new CloudShareError("signed_out", "Sign in to share.");
	const response = await api.fetch(apiUrl(baseUrl, apiPath), {
		method,
		headers: { Authorization: `Bearer ${token}`, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
		body: body === undefined ? undefined : JSON.stringify(body),
		signal,
	});
	if (response.status === 401) throw new CloudShareError("signed_out", "Sign in to share.", 401);
	if (!response.ok) {
		const envelope = (await response.json().catch(() => ({}))) as { code?: string; message?: string };
		throw new CloudShareError(envelope.code || `http_${response.status}`, envelope.message || `Request failed (${response.status})`, response.status);
	}
	if (response.status === 204) return undefined as T;
	return (await response.json()) as T;
}

export function listShareLinks(api: CloudShareApi, baseUrl: string): Promise<CloudShareLinks> {
	return callApi<CloudShareLinks>(api, baseUrl, "GET", "/me/artifact-shares").then((links) => ({
		...links,
		unlimited: Boolean(links.unlimited),
	}));
}

export async function unpublishShareLink(api: CloudShareApi, baseUrl: string, shareId: string): Promise<void> {
	if (!/^[0-9a-f-]{36}$/i.test(shareId)) throw new CloudShareError("invalid_request", "Not a share link.");
	await callApi<void>(api, baseUrl, "DELETE", `/me/artifact-shares/${shareId}`);
}

/** One committed file of a cloud session, named by its organization, session and path. */
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

/**
 * Finds a cloud session's file and looks for credentials in its start, read
 * through the session's short-lived file access prefix. The file never leaves
 * the control plane's bucket to be shared; this read is only the check.
 */
export async function inspectCloudSessionArtifact(
	api: CloudShareApi,
	baseUrl: string,
	ref: CloudSessionArtifactRef,
): Promise<{ name: string; size: number; sha256: string; secretFindings: string[] }> {
	const sessionPath = sessionApiPath(ref);
	const { artifacts } = await callApi<{ artifacts: Array<{ path: string; name: string; size: number; sha256: string }> }>(api, baseUrl, "GET", `${sessionPath}/artifacts`);
	const artifact = artifacts.find((candidate) => candidate.path === ref.path);
	if (!artifact) throw new CloudShareError("file_missing", "The file is no longer available.");
	const name = artifact.path.split("/").pop() || artifact.name;
	const scanner = new SecretScanner(name);
	const access = await callApi<{ filesPrefix: string }>(api, baseUrl, "POST", `${sessionPath}/artifacts/access`);
	if (!access.filesPrefix.startsWith(`${API_PREFIX}/artifact-files/`)) throw new CloudShareError("invalid_response", "The control plane gave no file access.");
	const base = parseCloudBaseUrl(baseUrl);
	const fileUrl = base.origin + base.pathname.replace(/\/+$/, "") + access.filesPrefix + artifact.path.split("/").map(encodeURIComponent).join("/");
	const controller = new AbortController();
	const response = await api.fetch(fileUrl, { method: "GET", headers: { Range: `bytes=0-${MAX_SCANNED_BYTES - 1}` }, signal: controller.signal });
	// An empty file has no range to read: 416.
	if (!response.ok && response.status !== 416) {
		throw new CloudShareError(`http_${response.status}`, `Reading the file failed (${response.status}).`, response.status);
	}
	if (response.ok && response.body) {
		const reader = response.body.getReader();
		let read = 0;
		try {
			while (read < MAX_SCANNED_BYTES) {
				const { done, value } = await reader.read();
				if (done) break;
				scanner.push(value);
				read += value.byteLength;
			}
		} finally {
			controller.abort();
		}
	}
	return { name, size: artifact.size, sha256: artifact.sha256, secretFindings: scanner.list() };
}

/**
 * Publishes a cloud session's file as a share link. The control plane copies
 * the file's committed version to the link inside its bucket, so nothing is
 * uploaded from here. It refuses (artifact_share_source_changed) when the
 * committed version is no longer sha256, the one the person was shown.
 */
export function shareCloudSessionArtifact(
	api: CloudShareApi,
	baseUrl: string,
	ref: CloudSessionArtifactRef & { sha256: string; shareId?: string },
	signal?: AbortSignal,
): Promise<{ share: CloudShareLink; replaced: boolean }> {
	return callApi<{ share: CloudShareLink; replaced: boolean }>(api, baseUrl, "POST", `${sessionApiPath(ref)}/artifacts/share`,
		{ path: ref.path, sha256: ref.sha256, ...(ref.shareId ? { shareId: ref.shareId } : {}) }, signal);
}

export interface ShareUploadInput {
	baseUrl: string;
	snapshot: ShareSnapshot;
	name: string;
	/** With feature and name, names the document the link serves. */
	project: string;
	feature: string;
	/** Update this active link instead. */
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

/**
 * Uploads a snapshot as a share link's file: starts the upload, presigns and
 * puts its parts, and finalizes it. A failure or cancel before finalizing
 * aborts the upload so its parts are discarded.
 */
export async function uploadShare(
	api: CloudShareApi,
	input: ShareUploadInput,
	onProgress: (sentBytes: number) => void,
	signal?: AbortSignal,
): Promise<{ share: CloudShareLink; replaced: boolean }> {
	const { baseUrl, snapshot } = input;
	const start = await callApi<UploadStart>(api, baseUrl, "POST", "/me/artifact-shares/uploads", input.shareId
		? { name: input.name, size: snapshot.size, sha256: snapshot.sha256, shareId: input.shareId }
		: { name: input.name, size: snapshot.size, sha256: snapshot.sha256, project: input.project, feature: input.feature }, signal);
	const handle = await fs.open(snapshot.path, "r");
	// One failed part stops the others before the upload is aborted and the
	// snapshot closed.
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
			// The snapshot does not change, so each part is read again only as it
			// is sent: at most UPLOAD_CONCURRENCY parts are in memory.
			const worker = async () => {
				for (let part = queue.shift(); part && !batch.signal.aborted; part = queue.shift()) {
					const signed = parts.find((candidate) => candidate.partNumber === part!.partNumber);
					if (!signed) throw new CloudShareError("upload_failed", "The upload was signed for another part.");
					const range = partRange(start, snapshot.size, part.partNumber);
					const body = Buffer.alloc(range.length);
					await handle.read(body, 0, range.length, range.offset);
					const response = await api.fetch(part.url, { method: part.method, headers: partHeaders(part.headers), body, signal: batch.signal });
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
			{ uploadToken: start.uploadToken, parts: completed }, signal);
	} catch (error) {
		batch.abort();
		await Promise.allSettled(running);
		// The upload was never finalized: discard its parts. Finalize itself
		// failing leaves nothing to discard but aborting is harmless.
		await callApi<void>(api, baseUrl, "POST", "/me/artifact-shares/uploads/abort", { uploadToken: start.uploadToken }).catch(() => undefined);
		throw error;
	} finally {
		signal?.removeEventListener("abort", stop);
		await handle.close();
	}
}
