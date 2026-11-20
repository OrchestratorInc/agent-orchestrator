/**
 * Publishing a local artifact as a public share link: the main-process half.
 *
 * Only a person publishes. The renderer names a file; this module resolves it
 * strictly inside the session's artifacts folder, copies it once into a
 * private snapshot while hashing it, and uploads that snapshot straight to
 * storage through the control plane's presigned multipart upload. The bytes
 * that go public are exactly the ones copied, even if an agent rewrites the
 * file meanwhile. The sign-in token never leaves this process.
 */

import { createHash, randomUUID } from "node:crypto";
import { constants as fsConstants, promises as fs } from "node:fs";
import path from "node:path";
import type { CloudShareAccess, CloudShareGrant, CloudShareGrants, CloudShareInvite, CloudShareLink, CloudShareLinks, CloudShareSubjectGrant } from "../shared/cloud-share";
import { parseCloudBaseUrl } from "./cloud-cp-proxy";

const API_PREFIX = "/api/cloud/v1";
/** The folder in a session's artifacts that holds version history; never shared. */
export const VERSIONS_DIR = ".versions";
const SESSION_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const READ_CHUNK = 1 << 20;
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

/**
 * A local file's fingerprint: its size and modification time. Publishing
 * refuses a file whose fingerprint changed since the panel showed it. Taken
 * from the file's metadata alone, so nothing is read before publishing.
 */
export function shareFileFingerprint(stat: { size: number; mtimeMs: number }): string {
	return `${stat.size}:${Math.trunc(stat.mtimeMs)}`;
}

/** The fingerprint of a resolved file, read from its metadata. */
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
	/** The source's fingerprint as it was copied; see shareFileFingerprint. */
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
 * as it goes. A file written while it is copied is refused (source_changed):
 * the snapshot would not be the version its fingerprint names. The upload reads only the snapshot. The copy
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
		// The fingerprint is taken before the copy, so the copy must end on the
		// same version: a write while it ran (an agent rewriting a part not yet
		// read) leaves bytes the fingerprint does not name. The same open file
		// is checked again, by its size, modification and change time, and the
		// bytes copied must be all of it.
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

/** How long reading a remote host's file may take: its headers, and the whole download. */
export const HOST_SHARE_TIMEOUTS: Record<"inspect" | "download", number> = {
	inspect: 30_000,
	download: 15 * 60_000,
};

/** A remote host's artifact as its daemon serves it. */
export interface HostShareFileInfo {
	name: string;
	/** Slash-separated path in the session's artifacts folder. */
	relativePath: string;
	size: number;
	/** See shareFileFingerprint; read from the host's ETag, so it matches a local file's. */
	fingerprint: string;
}

/**
 * The address of a remote host's artifact file on that host's loopback proxy
 * (apiBase), served as its exact bytes. The path is checked as a local
 * file's is, before the host's own check: no absolute path, no "..", and
 * never the version history folder.
 */
export function hostShareFileUrl(apiBase: string, sessionId: unknown, relPath: unknown): { url: string; relativePath: string; name: string } {
	if (typeof sessionId !== "string" || !SESSION_ID_PATTERN.test(sessionId) || sessionId === "." || sessionId === "..") {
		throw new CloudShareError("invalid_file", "Not a session's artifact.");
	}
	if (typeof relPath !== "string" || relPath === "" || relPath.includes("\0") || relPath.includes("\\") || relPath.startsWith("/") || path.win32.isAbsolute(relPath)) {
		throw new CloudShareError("invalid_file", "Not a file in the session's artifacts.");
	}
	const segments = relPath.split("/").filter((segment) => segment !== "");
	if (segments.length === 0 || segments.some((segment) => segment === "." || segment === "..")) {
		throw new CloudShareError("invalid_file", "Only files in the session's artifacts folder can be shared.");
	}
	if (segments[0] === VERSIONS_DIR) throw new CloudShareError("invalid_file", "Version history cannot be shared.");
	const relativePath = segments.join("/");
	const url = `${apiBase.replace(/\/+$/, "")}/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${segments.map(encodeURIComponent).join("/")}?source=artifact&raw=true`;
	return { url, relativePath, name: segments[segments.length - 1] ?? relativePath };
}

/**
 * The fingerprint a remote host's response names. The daemon tags an
 * artifact "<size>-<modification time in ns>", which gives the same
 * fingerprint a local file's metadata would. A host that sends no such tag
 * falls back to its Content-Length and Last-Modified, which only tells
 * changes a second apart.
 */
export function hostShareFingerprint(headers: Headers): { size: number; fingerprint: string } | undefined {
	const tag = /^(?:W\/)?"?(\d+)-(\d+)"?$/.exec(headers.get("etag")?.trim() ?? "");
	if (tag) {
		const size = Number(tag[1]);
		const nanoseconds = BigInt(tag[2] ?? "0");
		return { size, fingerprint: shareFileFingerprint({ size, mtimeMs: Number(nanoseconds / 1_000_000n) }) };
	}
	const length = headers.get("content-length");
	const modified = Date.parse(headers.get("last-modified") ?? "");
	if (length === null || !/^\d+$/.test(length) || Number.isNaN(modified)) return undefined;
	const size = Number(length);
	return { size, fingerprint: shareFileFingerprint({ size, mtimeMs: modified }) };
}

function hostFileError(response: Response): CloudShareError {
	if (response.status === 404) return new CloudShareError("file_missing", "The file is no longer available.");
	return new CloudShareError("host_unavailable", `The machine this file is on didn't answer (${response.status}).`, response.status);
}

/** fetch from a remote host, with a time limit; a cancel stays a cancel. */
async function fetchHostWithin(fetchHost: HostFetch, url: string, init: RequestInit, timeoutMs: number, signal?: AbortSignal): Promise<Response> {
	const timeout = AbortSignal.timeout(timeoutMs);
	try {
		return await fetchHost(url, { ...init, signal: signal ? AbortSignal.any([signal, timeout]) : timeout });
	} catch (error) {
		if (timeout.aborted && !signal?.aborted) throw new CloudShareError("timeout", "The machine this file is on didn't answer in time. Check that it is online and try again.");
		if (signal?.aborted) throw error;
		throw new CloudShareError("host_unavailable", "The machine this file is on can't be reached. Check that it is online and try again.");
	}
}

export type HostFetch = (url: string, init: RequestInit) => Promise<Response>;

/** A remote host's file's name, size and fingerprint, read from its headers alone. */
export async function inspectHostShareFile(fetchHost: HostFetch, file: { url: string; relativePath: string; name: string }, signal?: AbortSignal): Promise<HostShareFileInfo> {
	const version = await hostFileVersion(fetchHost, file.url, signal);
	return { name: file.name, relativePath: file.relativePath, ...version };
}

/**
 * A remote host's file's size and fingerprint, from its headers alone. A GET whose body is dropped
 * once the headers are in: the host's file route answers GET only, and a HEAD there is refused with 405.
 */
async function hostFileVersion(fetchHost: HostFetch, url: string, signal?: AbortSignal): Promise<{ size: number; fingerprint: string }> {
	const response = await fetchHostWithin(fetchHost, url, { method: "GET" }, HOST_SHARE_TIMEOUTS.inspect, signal);
	await response.body?.cancel().catch(() => undefined);
	if (!response.ok) throw hostFileError(response);
	const version = hostShareFingerprint(response.headers);
	if (!version) throw new CloudShareError("host_unsupported", "The machine this file is on is running an older Orchestrator. Update it to share its files.");
	return version;
}

/**
 * Downloads a remote host's file into a private snapshot under snapshotDir,
 * hashing it as it goes. The version is the one the response names, so a
 * file the agent rewrote since the panel showed it is caught by the caller's
 * fingerprint check. A body shorter or longer than that version, or a file
 * that is no longer that version once the body is read, is refused as
 * source_changed. The download needs as much free space as the file.
 */
export async function snapshotHostShareFile(
	fetchHost: HostFetch,
	file: { url: string },
	snapshotDir: string,
	options: SnapshotOptions & { expectedSize?: number } = {},
): Promise<ShareSnapshot> {
	await fs.mkdir(snapshotDir, { recursive: true, mode: 0o700 });
	if (options.expectedSize !== undefined) {
		const free = await fs.statfs(snapshotDir).then((stats) => stats.bavail * stats.bsize, () => Number.POSITIVE_INFINITY);
		if (free < options.expectedSize) throw new CloudShareError("disk_full", "There is not enough free disk space to prepare this file for sharing.");
	}
	const response = await fetchHostWithin(fetchHost, file.url, { method: "GET" }, HOST_SHARE_TIMEOUTS.download, options.signal);
	if (!response.ok) {
		await response.body?.cancel().catch(() => undefined);
		throw hostFileError(response);
	}
	const version = hostShareFingerprint(response.headers);
	if (!version || !response.body) {
		await response.body?.cancel().catch(() => undefined);
		throw new CloudShareError("host_unsupported", "The machine this file is on is running an older Orchestrator. Update it to share its files.");
	}
	const target = path.join(snapshotDir, randomUUID());
	// The body is cancelled on every way out short of reading it all, so it never holds its connection.
	const reader = response.body.getReader();
	const output = await fs.open(target, "wx", 0o600).catch(async (error: unknown) => {
		await reader.cancel().catch(() => undefined);
		throw error;
	});
	const hash = createHash("sha256");
	let size = 0;
	try {
		for (;;) {
			if (options.signal?.aborted) {
				await reader.cancel().catch(() => undefined);
				throw new CloudShareError("cancelled", "Sharing was cancelled.");
			}
			const { done, value } = await reader.read();
			if (done) break;
			hash.update(value);
			await output.write(value);
			size += value.byteLength;
			if (size > version.size) {
				await reader.cancel().catch(() => undefined);
				break;
			}
			options.onProgress?.(size);
		}
		// The bytes must be all of the version the response named, and no more.
		if (size !== version.size) throw new CloudShareError("source_changed", "The file changed while it was being prepared for sharing.");
		// The host streams the live file, so a rewrite in place during the body can mix two versions
		// under the first one's tag: the file must still be that version once it is all read.
		if ((await hostFileVersion(fetchHost, file.url, options.signal)).fingerprint !== version.fingerprint) {
			throw new CloudShareError("source_changed", "The file changed while it was being prepared for sharing.");
		}
		await output.close();
		return { path: target, size, sha256: hash.digest("hex"), fingerprint: version.fingerprint };
	} catch (error) {
		await reader.cancel().catch(() => undefined);
		await output.close().catch(() => undefined);
		await fs.rm(target, { force: true });
		// A connection that dropped mid-download reads as the host being away, not as a bug.
		if (error instanceof CloudShareError || options.signal?.aborted) throw error;
		throw new CloudShareError("host_unavailable", "The machine this file is on stopped answering. Check that it is online and try again.");
	}
}

/** The largest page whose files are looked for; a larger one is shared alone. */
export const MAX_SCANNED_PAGE = 32 << 20;

/**
 * A resolved file's text, read the way a snapshot reads it (see
 * openResolved), or null when it is larger than limit bytes.
 */
export async function readShareFileText(file: ResolvedShareFile, limit: number): Promise<string | null> {
	const handle = await openResolved(file);
	try {
		if ((await handle.stat()).size > limit) return null;
		return await handle.readFile("utf8");
	} finally {
		await handle.close();
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

/**
 * How long a request may take before it fails. A connection that dropped
 * (Wi-Fi off and on) can otherwise leave a request that never answers, and
 * the popover waiting on it for good.
 */
export const SHARE_TIMEOUTS: Record<"api" | "probe" | "transfer" | "publish", number> = {
	/** An ordinary control-plane request. */
	api: 30_000,
	/** Whether the control plane serves share links at all. */
	probe: 10_000,
	/** Each upload part. */
	transfer: 5 * 60_000,
	/** Finalizing an upload or copying a cloud session's file: the control plane may be moving a large file. */
	publish: 15 * 60_000,
};

/**
 * fetch with a time limit. A request that takes longer is refused with
 * CloudShareError "timeout"; one the caller's signal stopped still throws as
 * an abort, so a cancel stays a cancel. The limit covers reading the body too.
 */
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

/**
 * `promise`, or the signal's abort as soon as it comes. For a step that takes
 * no signal, such as reading the session token, which may refresh it over
 * the network, so it counts against the request's own time limit.
 */
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

/**
 * Whether the control plane serves share links: the routes exist only while
 * its share flag is on, so a 404 means it does not. It needs no sign-in: a
 * signed-out request gets 401 from a control plane that serves them. Null
 * when the control plane could not be reached.
 */
export async function shareLinksAvailable(api: CloudShareApi, baseUrl: string): Promise<boolean | null> {
	// The token read (which may refresh it) and the request share one limit.
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

/** Makes a link private or public. Its URL stays the same. */
export function setShareLinkAccess(api: CloudShareApi, baseUrl: string, shareId: string, access: CloudShareAccess): Promise<CloudShareLink> {
	return callApi<CloudShareLink>(api, baseUrl, "PATCH", shareLinkPath(shareId), { access });
}

interface ShareGrantsBody {
	grants: CloudShareGrant[];
	teams?: CloudShareSubjectGrant[];
	workspaces?: CloudShareSubjectGrant[];
}

/** A control plane that predates team sharing lists only people. */
function shareGrants(body: ShareGrantsBody): CloudShareGrants {
	return { people: body.grants, teams: body.teams ?? [], workspaces: body.workspaces ?? [] };
}

/** Everyone a link is shared with: people, teams and workspaces. */
export async function listShareGrants(api: CloudShareApi, baseUrl: string, shareId: string): Promise<CloudShareGrants> {
	return shareGrants(await callApi<ShareGrantsBody>(api, baseUrl, "GET", `${shareLinkPath(shareId)}/grants`));
}

/**
 * Shares a link with people, teams and a workspace at once, and returns
 * everyone it is shared with now. Only the fields set are sent, so sharing
 * with people alone works on a control plane that predates team sharing.
 */
export async function inviteShareGrants(api: CloudShareApi, baseUrl: string, shareId: string, invite: CloudShareInvite): Promise<CloudShareGrants> {
	const teamIds = invite.teamIds ?? [];
	const subjects = teamIds.length > 0 || invite.workspace === true;
	if (subjects && (!invite.orgId || !UUID_PATTERN.test(invite.orgId) || !teamIds.every((id) => UUID_PATTERN.test(id)))) {
		throw new CloudShareError("invalid_request", "Not a team or workspace to share with.");
	}
	const body = {
		...(invite.emails && invite.emails.length > 0 ? { emails: invite.emails } : {}),
		...(subjects ? { orgId: invite.orgId } : {}),
		...(teamIds.length > 0 ? { teamIds } : {}),
		...(invite.workspace === true ? { workspace: true } : {}),
	};
	return shareGrants(await callApi<ShareGrantsBody>(api, baseUrl, "POST", `${shareLinkPath(shareId)}/grants`, body));
}

export async function removeShareGrant(api: CloudShareApi, baseUrl: string, shareId: string, grantId: string): Promise<void> {
	if (!UUID_PATTERN.test(grantId)) throw new CloudShareError("invalid_request", "Not someone the link is shared with.");
	await callApi<void>(api, baseUrl, "DELETE", `${shareLinkPath(shareId)}/grants/${grantId}`);
}

/**
 * What a publish asks a new link to start with. Neither is sent when unset:
 * a control plane that predates private links refuses fields it does not know.
 */
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
 * A cloud session's file as its session lists it: its name, size and
 * committed version (SHA-256). One request, made only when the renderer did
 * not already name the version, or after the control plane refused a
 * version that changed. The file itself is never read.
 */
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

/**
 * Publishes a cloud session's file as a share link. The control plane copies
 * the file's committed version to the link inside its bucket, so nothing is
 * uploaded from here. It refuses (artifact_share_source_changed) when the
 * committed version is no longer sha256, the one the person was shown.
 */
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
	/** With feature and name, names the document the link serves. */
	project: string;
	feature: string;
	/** Update this active link instead. */
	shareId?: string;
	/**
	 * The snapshot is an HTML page: files are the files it uses, uploaded with
	 * it, and from now on the only files its link serves beside it (none when
	 * empty). See cloud-share-page-files.ts.
	 */
	page?: { files: PageFileSnapshot[] };
}

/** One file a page uses, as it was copied, and where its link serves it. */
export interface PageFileSnapshot {
	path: string;
	snapshot: ShareSnapshot;
}

/** What publishing a page did with the files it uses. */
export interface PageFilesOutcome {
	/** How many the link serves beside the page now. */
	included: number;
	/**
	 * The control plane cannot serve a page's files (it predates them): the
	 * page was published alone, and an active link kept the files it had.
	 */
	unsupported: boolean;
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

interface CompletedPart {
	partNumber: number;
	sha256: string;
	etag: string;
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
 * Presigns and puts every part of one started upload of snapshot, and returns
 * the parts in order, ready to finish it. One failed part stops the others
 * before this throws. onSent gets the bytes of each part as it is sent.
 */
async function putSnapshotParts(
	api: CloudShareApi,
	baseUrl: string,
	start: UploadStart,
	snapshot: ShareSnapshot,
	onSent: (bytes: number) => void,
	signal?: AbortSignal,
): Promise<CompletedPart[]> {
	const handle = await fs.open(snapshot.path, "r");
	const batch = new AbortController();
	const stop = () => batch.abort();
	signal?.addEventListener("abort", stop, { once: true });
	const running: Array<Promise<void>> = [];
	try {
		const completed: CompletedPart[] = [];
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
					const response = await fetchWithin(SHARE_TIMEOUTS.transfer, batch.signal, (signal) =>
						api.fetch(part.url, { method: part.method, headers: partHeaders(part.headers), body, signal }));
					const etag = response.headers.get("etag");
					if (!response.ok || !etag) throw new CloudShareError("upload_failed", `Uploading part ${part.partNumber} failed (${response.status}).`, response.status);
					completed.push({ partNumber: part.partNumber, sha256: signed.sha256, etag });
					onSent(body.byteLength);
				}
			};
			const workers = Array.from({ length: UPLOAD_CONCURRENCY }, worker);
			running.push(...workers);
			await Promise.all(workers);
		}
		return completed.sort((a, b) => a.partNumber - b.partNumber);
	} catch (error) {
		batch.abort();
		await Promise.allSettled(running);
		throw error;
	} finally {
		signal?.removeEventListener("abort", stop);
		await handle.close();
	}
}

/** Whether a control-plane refusal says it has no such route: it predates it. */
function isMissingRoute(error: unknown): boolean {
	return error instanceof CloudShareError && error.status === 404;
}

/**
 * Uploads the files a page uses under the page's upload (pageToken) and
 * returns their file tokens for finalizing, or null when the control plane
 * predates included files: it then cannot take a files list either, since it
 * refuses fields it does not know. With no files, one request that is refused
 * either way tells which: an unknown route (404) or a path it checks (400).
 */
async function uploadPageFiles(
	api: CloudShareApi,
	baseUrl: string,
	pageToken: string,
	files: PageFileSnapshot[],
	onSent: (bytes: number) => void,
	signal?: AbortSignal,
): Promise<string[] | null> {
	if (files.length === 0) {
		try {
			await callApi<unknown>(api, baseUrl, "POST", "/me/artifact-shares/uploads/files",
				{ uploadToken: pageToken, path: "", size: 0, sha256: "0".repeat(64) }, signal);
		} catch (error) {
			if (isMissingRoute(error)) return null;
			if (error instanceof CloudShareError && error.status === 400) return [];
			throw error;
		}
		return [];
	}
	const tokens: string[] = [];
	for (const [index, file] of files.entries()) {
		let start: UploadStart;
		try {
			start = await callApi<UploadStart>(api, baseUrl, "POST", "/me/artifact-shares/uploads/files",
				{ uploadToken: pageToken, path: file.path, size: file.snapshot.size, sha256: file.snapshot.sha256 }, signal);
		} catch (error) {
			if (index === 0 && isMissingRoute(error)) return null;
			throw error;
		}
		try {
			const parts = await putSnapshotParts(api, baseUrl, start, file.snapshot, onSent, signal);
			const done = await callApi<{ fileToken: string }>(api, baseUrl, "POST", "/me/artifact-shares/uploads/files/complete",
				{ uploadToken: start.uploadToken, parts }, signal, SHARE_TIMEOUTS.publish);
			tokens.push(done.fileToken);
		} catch (error) {
			await callApi<void>(api, baseUrl, "POST", "/me/artifact-shares/uploads/abort", { uploadToken: start.uploadToken }).catch(() => undefined);
			throw error;
		}
	}
	return tokens;
}

/**
 * Uploads a snapshot as a share link's file: starts the upload, presigns and
 * puts its parts, and finalizes it. A page's files go first, under the same
 * upload, and finalizing lists them. A failure or cancel before finalizing
 * aborts the upload so its parts are discarded; a file uploaded but never
 * listed is deleted by the control plane in time. onProgress gets the bytes
 * sent so far, the page's files included.
 */
export async function uploadShare(
	api: CloudShareApi,
	input: ShareUploadInput,
	onProgress: (sentBytes: number) => void,
	signal?: AbortSignal,
): Promise<{ share: CloudShareLink; replaced: boolean; pageFiles?: PageFilesOutcome }> {
	const { baseUrl, snapshot } = input;
	// An update names where the file is now too, so its link follows a file
	// moved to another folder.
	const start = await callApi<UploadStart>(api, baseUrl, "POST", "/me/artifact-shares/uploads", {
		name: input.name, size: snapshot.size, sha256: snapshot.sha256, project: input.project, feature: input.feature,
		...(input.shareId ? { shareId: input.shareId } : {}),
	}, signal);
	let sent = 0;
	const onSent = (bytes: number) => {
		sent += bytes;
		onProgress(sent);
	};
	try {
		const fileTokens = input.page ? await uploadPageFiles(api, baseUrl, start.uploadToken, input.page.files, onSent, signal) : null;
		const parts = await putSnapshotParts(api, baseUrl, start, snapshot, onSent, signal);
		const result = await callApi<{ share: CloudShareLink; replaced: boolean }>(api, baseUrl, "POST", "/me/artifact-shares/finalize", {
			uploadToken: start.uploadToken,
			parts,
			...accessFields(input),
			...(fileTokens ? { files: fileTokens.map((fileToken) => ({ fileToken })) } : {}),
		}, signal, SHARE_TIMEOUTS.publish);
		if (!input.page) return result;
		return { ...result, pageFiles: { included: fileTokens?.length ?? 0, unsupported: fileTokens === null } };
	} catch (error) {
		// The upload was never finalized: discard its parts. Finalize itself
		// failing leaves nothing to discard but aborting is harmless.
		await callApi<void>(api, baseUrl, "POST", "/me/artifact-shares/uploads/abort", { uploadToken: start.uploadToken }).catch(() => undefined);
		throw error;
	}
}
