/**
 * cloudShare IPC: the only way to publish a share link. Agents, the ao CLI,
 * MCP and the daemon have no route to it; only the app's own window's main
 * frame can ask, when the person presses Publish in its share popover. A
 * local file is uploaded from here, as is a remote host's file, downloaded
 * first through that host's loopback proxy; a cloud session's file is copied
 * by the control plane. See shared/cloud-share.ts for what is and is not confirmed.
 */

import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, IpcMainInvokeEvent } from "electron";
import {
	artifactShareUrl,
	CLOUD_SHARE_PROGRESS_CHANNEL,
	isUnplayableShareVideo,
	sameShareFingerprint,
	type CloudShareAccess,
	type CloudShareFile,
	type CloudShareGrants,
	type CloudShareInvite,
	type CloudShareInspection,
	type CloudShareLinks,
	type CloudSharePageFiles,
	type CloudSharePublishResult,
	type CloudSessionShareFile,
	type CloudHostShareFile,
	type CloudShareLink,
	type HostShareLink,
} from "../shared/cloud-share";
import {
	CloudShareError,
	type CloudShareApi,
	cloudSessionArtifactVersion,
	type CloudSessionArtifact,
	type CloudSessionShareFileRef,
	listCloudSessionArtifacts,
	readCloudSessionArtifactText,
	type HostFetch,
	hostShareFileUrl,
	inspectHostShareFile,
	inviteShareGrants,
	listShareGrants,
	listShareLinks,
	removeShareGrant,
	setShareLinkAccess,
	resolveShareFile,
	shareLinksAvailable,
	shareCloudSessionArtifact,
	MAX_SCANNED_PAGE,
	type PageFileSnapshot,
	type PageFilesOutcome,
	readShareFileText,
	type ResolvedShareFile,
	type ShareSnapshot,
	shareFileFingerprint,
	snapshotHostShareFile,
	snapshotShareFile,
	statShareFile,
	unpublishShareLink,
	uploadShare,
} from "./cloud-share";
import { type ShareLinkRecordStore, artifactRecordKey, shareAccess, shareRecordKey } from "./cloud-share-links";
import { collectPageFiles, findPageFiles, isSharePage, type PageFileSkip } from "./cloud-share-page-files";

/** Filesystem errors that say a file is no longer there to open, or is no longer a plain file. */
const SOURCE_GONE_CODES = new Set(["ENOENT", "ENOTDIR", "ELOOP"]);

/**
 * Whether copying a page's file failed because the file changed since it was
 * found: removed, swapped for a symlink or rewritten. Then only that file is
 * left out. A cancel, a full disk or an error writing the copy is still fatal.
 */
function sourceFileGone(error: unknown, realPath: string): boolean {
	if (error instanceof CloudShareError) return error.code !== "cancelled" && error.code !== "disk_full";
	const failure = error as NodeJS.ErrnoException | null;
	return typeof failure?.code === "string" && SOURCE_GONE_CODES.has(failure.code) && failure.path === realPath;
}

/**
 * What a page's file was when it was published, kept under the path the page
 * names it by: the file that path resolved to and its fingerprint. A symlink
 * pointed at another file then shows as a change, though neither file did.
 */
function pageFileVersion(file: ResolvedShareFile, fingerprint: string): string {
	return `${file.relativePath}\n${fingerprint}`;
}

/**
 * What a remote host's page file was when it was published: the path the
 * host resolved it to and its fingerprint, as pageFileVersion keeps a local one.
 */
function hostPageFileVersion(resolvedPath: string, fingerprint: string): string {
	return `${resolvedPath}\n${fingerprint}`;
}

/**
 * Whether a host refused one file of a page (403): that file is left out. Its
 * other refusals are about the request, not the file (401 for a revoked
 * device or wrong password, 408, 429 while it locks this address out), so
 * they fail the publish rather than drop files a link may already serve. A
 * missing file (404) is file_missing.
 */
function hostRefusedFile(error: unknown): boolean {
	return error instanceof CloudShareError && error.code === "host_unavailable" && error.status === 403;
}

/** How many of a published host page's files are looked up at once, to tell whether any changed. */
const HOST_PAGE_FILE_CHECKS = 8;

/** How long telling whether a published host page's files changed may take, all of them together. */
const HOST_PAGE_FILES_DEADLINE_MS = 10_000;

/** How many of a host page's files download at once. */
const HOST_PAGE_FILE_DOWNLOADS = 4;

/**
 * Whether any file a published page used changed since: rewritten, gone,
 * resolving to another file, or there now though it was missing. Read from
 * each file's metadata alone.
 */
async function pageFilesChanged(dataDir: string, sessionId: string, recorded: Record<string, string>): Promise<boolean> {
	for (const [referencedPath, version] of Object.entries(recorded)) {
		let now = "";
		try {
			const file = await resolveShareFile(dataDir, sessionId, referencedPath);
			now = pageFileVersion(file, await statShareFile(file));
		} catch (error) {
			if (!(error instanceof CloudShareError) && !(error as NodeJS.ErrnoException | null)?.code) throw error;
		}
		if (now !== version) return true;
	}
	return false;
}

/** A page's files as they were copied for upload (or listed, for a cloud session's page), and what it left out. */
interface PageFilesTaken<F = PageFileSnapshot> {
	files: F[];
	skipped: PageFileSkip[];
	/** Which file the page names by each included path. */
	referenced: Map<string, string>;
}

/**
 * What publishing a page did with its files, for the popover to say. The
 * videos the control plane refused are added to what the page left out, as a
 * missing file is; onRefusedVideo gets each one refused for being a video,
 * by its path in the session's artifacts.
 */
function pageFilesOutcome(page: PageFilesTaken<unknown>, uploaded: PageFilesOutcome | undefined, onRefusedVideo?: (relativePath: string) => void): CloudSharePageFiles | undefined {
	if (!uploaded) return undefined;
	const left = [
		...uploaded.refusedVideos.map((refused) => [refused, "video"] as const),
		...uploaded.tooLargeVideos.map((refused) => [refused, "too_large"] as const),
	];
	for (const [refused, reason] of left) {
		const relativePath = page.referenced.get(refused);
		page.skipped.push({ reference: refused, reason, ...(relativePath ? { relativePath } : {}) });
		if (relativePath && reason === "video") onRefusedVideo?.(relativePath);
	}
	// What was left out here is said whether or not the control plane takes files.
	return {
		included: uploaded.included,
		skipped: page.skipped.length,
		videos: page.skipped.filter((skip) => skip.reason === "video").length,
		...(page.skipped.some((skip) => skip.reason === "host_outdated") ? { hostOutdated: page.skipped.filter((skip) => skip.reason === "host_outdated").length } : {}),
		// Said only when the page had files to include.
		unsupported: uploaded.unsupported && page.files.length > 0,
	};
}

export interface CloudShareIpcDeps extends CloudShareApi {
	ipcMain: Pick<IpcMain, "handle">;
	/** The daemon's data directory, which holds artifacts/<session>. */
	daemonDataDir: () => string;
	/** A private directory for upload snapshots. */
	snapshotDir: () => string;
	/** Which link each local and remote-host file was last published to. */
	records: ShareLinkRecordStore;
	/** A connected remote host's API base on its loopback proxy; undefined while it is not connected. */
	hostApiBase: (hostId: string) => string | undefined;
	/** Requests to a host's loopback proxy, which adds the host's credential. */
	fetchHost: HostFetch;
}

/**
 * Refuses a video browsers cannot play before anything is uploaded. Any other
 * video goes to the control plane, which refuses it (415, shown as coming
 * soon) while it does not serve videos.
 */
const UNPLAYABLE_VIDEO_MESSAGE = "This video format can't be played in a browser. Use MP4, MOV or WebM.";

function accessArgument(value: unknown): CloudShareAccess | undefined {
	if (value === undefined) return undefined;
	if (value !== "restricted" && value !== "anyone") throw new CloudShareError("invalid_request", "Not who a link opens for.");
	return value;
}

function emailsArgument(value: unknown): string[] | undefined {
	if (value === undefined) return undefined;
	if (!Array.isArray(value) || !value.every((email) => typeof email === "string")) {
		throw new CloudShareError("invalid_request", "Not a list of emails.");
	}
	return value as string[];
}

function inviteArgument(value: unknown): CloudShareInvite {
	const { emails, orgId, teamIds, workspace } = (value ?? {}) as Record<string, unknown>;
	if (orgId !== undefined && typeof orgId !== "string") throw new CloudShareError("invalid_request", "Not a workspace.");
	if (teamIds !== undefined && (!Array.isArray(teamIds) || !teamIds.every((id) => typeof id === "string"))) {
		throw new CloudShareError("invalid_request", "Not a list of teams.");
	}
	if (workspace !== undefined && typeof workspace !== "boolean") throw new CloudShareError("invalid_request", "Not a workspace.");
	return {
		emails: emailsArgument(emails),
		...(orgId !== undefined ? { orgId } : {}),
		...(teamIds !== undefined ? { teamIds: teamIds as string[] } : {}),
		...(workspace !== undefined ? { workspace } : {}),
	};
}

function shareFileArgument(value: unknown): CloudShareFile {
	if (typeof value !== "object" || value === null) throw new CloudShareError("invalid_request", "Not a file to share.");
	const { source, baseUrl, sessionId, path: filePath, projectId, orgId, artifactId, shareId, fingerprint } = value as Record<string, unknown>;
	if (typeof baseUrl !== "string" || typeof sessionId !== "string" || typeof filePath !== "string" ||
		(artifactId !== undefined && (typeof artifactId !== "string" || artifactId === "")) ||
		(shareId !== undefined && typeof shareId !== "string") || (fingerprint !== undefined && typeof fingerprint !== "string")) {
		throw new CloudShareError("invalid_request", "Not a file to share.");
	}
	const access = accessArgument((value as { access?: unknown }).access);
	const emails = emailsArgument((value as { emails?: unknown }).emails);
	const startsWith = { ...(access ? { access } : {}), ...(emails ? { emails } : {}) };
	if (source === "cloud") {
		if (typeof orgId !== "string") throw new CloudShareError("invalid_request", "Not a file to share.");
		return { source, baseUrl, orgId, sessionId, path: filePath, projectId: "", artifactId, shareId, fingerprint, ...startsWith } satisfies CloudSessionShareFile;
	}
	// A local or remote-host file's project names the document: an empty one
	// would make a second document, and link, for a file already published.
	if (source !== undefined && source !== "local" && source !== "host") throw new CloudShareError("invalid_request", "Not a file to share.");
	if (typeof projectId !== "string" || projectId === "") throw new CloudShareError("invalid_request", "Not a file to share.");
	if (source === "host") {
		const { hostId } = value as Record<string, unknown>;
		if (typeof hostId !== "string" || hostId === "") throw new CloudShareError("invalid_request", "Not a file to share.");
		return { source, hostId, baseUrl, sessionId, path: filePath, projectId, artifactId, shareId, fingerprint, ...startsWith } satisfies CloudHostShareFile;
	}
	return { baseUrl, sessionId, path: filePath, projectId, artifactId, shareId, fingerprint, ...startsWith };
}

function shareIdArgument(value: unknown): string {
	const shareId = (value as { shareId?: unknown } | null)?.shareId;
	if (typeof shareId !== "string") throw new CloudShareError("invalid_request", "Not a share link.");
	return shareId;
}

function isCloudFile(file: CloudShareFile): file is CloudSessionShareFile {
	return file.source === "cloud" && typeof file.orgId === "string";
}

/**
 * The record key of a file: by its artifact ID when it has one, so the record
 * follows the file through a rename, else by its path. A record kept under the
 * path from before the file had an ID moves to the ID's key.
 */
async function recordKey(records: ShareLinkRecordStore, baseUrl: string, sessionId: string, relativePath: string, artifactId: string | undefined): Promise<string> {
	const byPath = shareRecordKey(baseUrl, sessionId, relativePath);
	if (!artifactId) return byPath;
	const key = artifactRecordKey(baseUrl, sessionId, artifactId);
	await records.adopt(key, byPath);
	return key;
}

/** The record key of a cloud session's file: apart from every local session's. */
function cloudRecordKey(records: ShareLinkRecordStore, baseUrl: string, file: CloudSessionShareFile): Promise<string> {
	return recordKey(records, baseUrl, `cloud:${file.orgId}/${file.sessionId}`, file.path, file.artifactId);
}

function isHostFile(file: CloudShareFile): file is CloudHostShareFile {
	return file.source === "host" && typeof file.hostId === "string";
}

/**
 * The session part of a remote host's record keys: apart from every local
 * and cloud session's, and from the same session ID on another host. A
 * session ID has no "/", so the last one ends the host's ID.
 */
const HOST_SESSION_PREFIX = "host:";
function hostRecordSession(hostId: string, sessionId: string): string {
	return `${HOST_SESSION_PREFIX}${hostId}/${sessionId}`;
}

/** The host and session a record key's session part names, when it is a remote host's. */
function parseHostRecordSession(session: string): { hostId: string; sessionId: string } | undefined {
	if (!session.startsWith(HOST_SESSION_PREFIX)) return undefined;
	const slash = session.lastIndexOf("/");
	if (slash <= HOST_SESSION_PREFIX.length || slash === session.length - 1) return undefined;
	return { hostId: session.slice(HOST_SESSION_PREFIX.length, slash), sessionId: session.slice(slash + 1) };
}

/**
 * The remote hosts' links among the records, for one control plane: keys
 * [baseUrl, session, path] and [baseUrl, session, "", artifactId], with a
 * host's session part, whose link is active.
 */
function hostShareLinks(entries: readonly { key: string; url?: string; access?: CloudShareAccess }[], baseUrl: string): HostShareLink[] {
	return entries.flatMap(({ key, url, access }): HostShareLink[] => {
		if (!url) return [];
		let parts: unknown;
		try {
			parts = JSON.parse(key);
		} catch {
			return [];
		}
		if (!Array.isArray(parts) || parts[0] !== baseUrl || typeof parts[1] !== "string") return [];
		const host = parseHostRecordSession(parts[1]);
		if (!host) return [];
		const link = { ...host, url, ...(access ? { access } : {}) };
		if (parts.length === 4 && parts[2] === "" && typeof parts[3] === "string" && parts[3] !== "") return [{ ...link, artifactId: parts[3] }];
		if (parts.length === 3 && typeof parts[2] === "string" && parts[2] !== "") return [{ ...link, path: parts[2] }];
		return [];
	});
}

function baseUrlArgument(value: unknown): string {
	const baseUrl = (value as { baseUrl?: unknown } | null)?.baseUrl;
	if (typeof baseUrl !== "string") throw new CloudShareError("invalid_request", "Not a control plane.");
	return baseUrl;
}

/**
 * Refuses a call from anything but a window's main frame, where the bridge
 * lives: a frame with a parent is a subframe. It compares no frame objects,
 * so it does not depend on Electron returning the same one twice.
 */
function requireMainFrame(event: unknown): void {
	const { senderFrame } = event as Partial<IpcMainInvokeEvent>;
	if (senderFrame === null || (senderFrame && senderFrame.parent !== null)) {
		throw new CloudShareError("invalid_request", "Sharing is only available from the app window.");
	}
}

// Refusals that mean the bytes never made it to AO Cloud: the upload, or the
// machine or disk the file is read from.
const UPLOAD_FAILURES = new Set(["upload_failed", "timeout", "host_unavailable", "disk_full", "source_changed", "file_missing"]);

/** A refusal's category for publish telemetry. A file over the size limit is answered 413 or a too_large code. */
function shareFailure(error: CloudShareError): { failure?: "too_large" | "upload_failed" } {
	if (error.status === 413 || error.code.includes("too_large")) return { failure: "too_large" };
	if (UPLOAD_FAILURES.has(error.code)) return { failure: "upload_failed" };
	return {};
}

/**
 * Maps a refusal to its result. Every refusal is returned, not thrown: a
 * thrown error reaches the renderer wrapped in Electron's IPC text. Anything
 * else is a bug and is thrown.
 */
function refusal(error: unknown): CloudSharePublishResult {
	if (error instanceof CloudShareError) {
		switch (error.code) {
			case "signed_out":
				return { status: "signed_out" };
			case "artifact_share_limit_reached":
				return { status: "limit", message: error.message };
			case "artifact_share_type_unsupported":
				return { status: "unsupported", message: error.message };
			case "artifact_share_not_owner":
				return { status: "not_owner" };
		}
		return { status: "error", message: error.message, ...shareFailure(error) };
	}
	throw error;
}

export function registerCloudShareIpc(deps: CloudShareIpcDeps): void {
	const inFlight = new Map<number, AbortController>();

	// A cloud session's file is named by its committed version: the
	// fingerprint is its SHA-256, which the renderer already has from the
	// session's artifact list, and the control plane refuses to share another
	// version. Only when the renderer did not name it is the list read here.
	const inspectCloudFile = async (file: CloudSessionShareFile, refresh = false, linkOnly = false): Promise<CloudShareInspection> => {
		const key = await cloudRecordKey(deps.records, file.baseUrl, file);
		const listed = refresh || !file.fingerprint ? await cloudSessionArtifactVersion(deps, file.baseUrl, file) : undefined;
		const name = listed?.name ?? (file.path.split("/").pop() || file.path);
		const shareId = await deps.records.get(key);
		const publishedFingerprint = shareId ? await deps.records.publishedFingerprint(key) : undefined;
		const pageFiles = shareId && !linkOnly ? await deps.records.publishedPageFiles(key) : undefined;
		return {
			name,
			...(listed ? { size: listed.size } : {}),
			unsupported: isUnplayableShareVideo(name),
			fingerprint: listed?.sha256 ?? file.fingerprint ?? "",
			...(shareId ? { shareId } : {}),
			...(publishedFingerprint ? { publishedFingerprint } : {}),
			...(pageFiles && await cloudPageFilesChangedOrUnknown(file, pageFiles) ? { pageFilesChanged: true } : {}),
		};
	};

	// The link and fingerprint are already known: a list that fails or times
	// out leaves only the page's files unknown, which counts as unchanged, as a
	// remote host that stops answering does.
	const cloudPageFilesChangedOrUnknown = async (file: CloudSessionShareFile, recorded: Record<string, string>): Promise<boolean> => {
		try {
			return await cloudPageFilesChanged(file, recorded);
		} catch (error) {
			if (error instanceof CloudShareError) return false;
			throw error;
		}
	};

	// Whether any file a published cloud page used changed since: committed
	// again, deleted, or committed though it was missing. Read from one list of
	// the session's files.
	const cloudPageFilesChanged = async (file: CloudSessionShareFile, recorded: Record<string, string>): Promise<boolean> => {
		if (Object.keys(recorded).length === 0) return false;
		const listed = new Map((await listCloudSessionArtifacts(deps, file.baseUrl, file)).map((artifact) => [artifact.path, artifact.sha256]));
		return Object.entries(recorded).some(([artifactPath, sha256]) => (listed.get(artifactPath) ?? "") !== sha256);
	};

	// The files a cloud session's HTML page uses, found in the page as the
	// session committed it and looked up in the session's artifact list; the
	// control plane copies them beside the page. Null when the session
	// committed another version of the page than the one the person was shown;
	// undefined for a page too large to look in.
	const findCloudPageFiles = async (file: CloudSessionShareFile & { fingerprint: string }, signal?: AbortSignal): Promise<(PageFilesTaken<CloudSessionShareFileRef> & { fingerprints: Record<string, string> }) | null | undefined> => {
		const listed = await listCloudSessionArtifacts(deps, file.baseUrl, file, signal);
		const page = listed.find((artifact) => artifact.path === file.path);
		if (!page || page.sha256 !== file.fingerprint) return null;
		if (page.size > MAX_SCANNED_PAGE) return undefined;
		const html = await readCloudSessionArtifactText(deps, file.baseUrl, { ...file, sha256: file.fingerprint }, signal);
		if (html === null) return null;
		const byPath = new Map(listed.map((artifact) => [artifact.path, artifact]));
		const found = await findPageFiles(file.path, html, async (relative): Promise<CloudSessionArtifact & { relativePath: string }> => {
			const artifact = byPath.get(relative);
			if (!artifact) throw new CloudShareError("file_missing", "The file is no longer available.");
			return { ...artifact, relativePath: relative };
		});
		// Each file's committed version, and "" for one missing, so a later commit shows.
		const fingerprints: Record<string, string> = {};
		for (const skip of found.skipped) if (skip.relativePath) fingerprints[skip.relativePath] = "";
		for (const used of found.files) fingerprints[used.referencedPath] = used.file.sha256;
		return {
			files: found.files.map((used) => ({ path: used.path, artifactPath: used.referencedPath })),
			skipped: [...found.skipped],
			referenced: new Map(found.files.map((used) => [used.path, used.referencedPath])),
			fingerprints,
		};
	};

	const publishCloudFile = async (file: CloudSessionShareFile): Promise<CloudSharePublishResult> => {
		if (isUnplayableShareVideo(file.path)) return { status: "unsupported", message: UNPLAYABLE_VIDEO_MESSAGE };
		if (!(await deps.getToken())) return { status: "signed_out" };
		if (!file.fingerprint) return { status: "changed", inspection: await inspectCloudFile(file, true) };
		const sha256 = file.fingerprint;
		try {
			// Finding a page's files reads only the session's list and the page.
			const pageFiles = isSharePage(file.path) ? await findCloudPageFiles({ ...file, fingerprint: sha256 }) : undefined;
			if (pageFiles === null) return { status: "changed", inspection: await inspectCloudFile(file, true) };
			// The control plane copies the files inside its bucket, so there is
			// nothing to cancel: stopping the wait could leave a public link the
			// person was told was cancelled.
			let result: Awaited<ReturnType<typeof shareCloudSessionArtifact>>;
			let unsupported = false;
			try {
				result = await shareCloudSessionArtifact(deps, file.baseUrl, { ...file, sha256, ...(pageFiles ? { files: pageFiles.files } : {}) });
			} catch (error) {
				// A control plane that predates a page's files refuses the request
				// before it does anything: the page is published alone.
				if (!(pageFiles && error instanceof CloudShareError && error.status === 400 && error.code === "invalid_request")) throw error;
				unsupported = true;
				result = await shareCloudSessionArtifact(deps, file.baseUrl, { ...file, sha256 });
			}
			if (pageFiles) {
				for (const missing of result.pageFiles?.missing ?? []) {
					const relativePath = pageFiles.referenced.get(missing);
					pageFiles.skipped.push({ reference: missing, reason: "missing", ...(relativePath ? { relativePath } : {}) });
					if (relativePath) pageFiles.fingerprints[relativePath] = "";
				}
			}
			const servesFiles = Boolean(result.pageFiles) && !unsupported;
			// A video refused now is offered again once the control plane takes it.
			const outcome = pageFiles ? pageFilesOutcome(pageFiles, {
				included: result.pageFiles?.included ?? 0,
				unsupported: !servesFiles,
				refusedVideos: result.pageFiles?.refusedVideos ?? [],
				tooLargeVideos: result.pageFiles?.tooLargeVideos ?? [],
			}, (relativePath) => { pageFiles.fingerprints[relativePath] = ""; }) : undefined;
			const key = await cloudRecordKey(deps.records, file.baseUrl, file);
			// The link serves the page's files only when the control plane took them.
			await deps.records.set(key, result.share.id, sha256, artifactShareUrl(result.share), shareAccess(result.share), pageFiles && servesFiles ? pageFiles.fingerprints : undefined);
			return { status: "published", share: result.share, replaced: result.replaced, ...(outcome ? { pageFiles: outcome } : {}) };
		} catch (error) {
			// Another version was committed since: offer the newest.
			if (error instanceof CloudShareError && error.code === "artifact_share_source_changed") {
				return { status: "changed", inspection: await inspectCloudFile(file, true) };
			}
			throw error;
		}
	};

	// A remote host's file, at its address on the host's loopback proxy. The
	// host must be connected: its file is read nowhere else.
	const hostFile = (file: CloudHostShareFile) => {
		const apiBase = deps.hostApiBase(file.hostId);
		if (!apiBase) throw new CloudShareError("host_unavailable", "The machine this file is on isn't connected. Check that it is online and try again.");
		return hostShareFileUrl(apiBase, file.sessionId, file.path);
	};
	const hostRecordKey = (file: CloudHostShareFile, relativePath: string) =>
		recordKey(deps.records, file.baseUrl, hostRecordSession(file.hostId, file.sessionId), relativePath, file.artifactId);

	// Read from the file's headers on its host: its bytes are not downloaded.
	// What a file a remote host's page uses is now, as a published page
	// records it (hostPageFileVersion): "" when the host no longer serves it
	// there, and undefined when the host could not say, which counts as unchanged.
	const hostPageFileNow = async (file: CloudHostShareFile, referencedPath: string, signal: AbortSignal): Promise<string | undefined> => {
		try {
			const info = await inspectHostShareFile(deps.fetchHost, hostFile({ ...file, path: referencedPath }), signal);
			return info.resolvedPath ? hostPageFileVersion(info.resolvedPath, info.fingerprint) : "";
		} catch (error) {
			if (error instanceof CloudShareError && (error.code === "file_missing" || error.code === "invalid_file" || error.code === "host_unsupported" || hostRefusedFile(error))) return "";
			// The host, not the file: a timeout, a lost connection, a lockout, or the check's own deadline.
			return undefined;
		}
	};

	// Whether any file a published host page used changed since: rewritten,
	// gone, resolving to another file, or there now though it was missing.
	// Each is looked up from its headers alone, a few at a time, until one has.
	// The check has one deadline and stops at the first answer about the host
	// rather than a file, which then counts as unchanged: the panel waits on it.
	const hostPageFilesChanged = async (file: CloudHostShareFile, recorded: Record<string, string>): Promise<boolean> => {
		const entries = Object.entries(recorded);
		const deadline = AbortSignal.timeout(HOST_PAGE_FILES_DEADLINE_MS);
		let next = 0;
		let changed = false;
		let stopped = false;
		const worker = async () => {
			while (!changed && !stopped && !deadline.aborted && next < entries.length) {
				const [referencedPath, version] = entries[next++];
				const now = await hostPageFileNow(file, referencedPath, deadline);
				if (now === undefined) stopped = true;
				else if (now !== version) changed = true;
			}
		};
		await Promise.all(Array.from({ length: Math.min(HOST_PAGE_FILE_CHECKS, entries.length) }, worker));
		return changed;
	};

	const inspectHostFile = async (file: CloudHostShareFile, linkOnly = false): Promise<CloudShareInspection> => {
		const located = hostFile(file);
		const info = await inspectHostShareFile(deps.fetchHost, located);
		const key = await hostRecordKey(file, info.relativePath);
		const shareId = await deps.records.get(key);
		const publishedFingerprint = shareId ? await deps.records.publishedFingerprint(key) : undefined;
		const pageFiles = shareId && !linkOnly ? await deps.records.publishedPageFiles(key) : undefined;
		return {
			name: info.name,
			size: info.size,
			unsupported: isUnplayableShareVideo(info.name),
			fingerprint: info.fingerprint,
			...(shareId ? { shareId } : {}),
			...(publishedFingerprint ? { publishedFingerprint } : {}),
			...(pageFiles && await hostPageFilesChanged(file, pageFiles) ? { pageFilesChanged: true } : {}),
		};
	};

	// A file a remote host's page uses, looked up on that host from its
	// headers alone, and named by the path the host resolves it to, so the
	// page's folder check sees where a symlink leads, as a local page's does.
	// One the host refuses to serve, or that a host too old to say where it
	// resolves names, is left out, as one outside the page's folder is; a host
	// that stopped answering fails the publish.
	const hostPageFile = async (file: CloudHostShareFile, relative: string, signal: AbortSignal) => {
		const located = hostFile({ ...file, path: relative });
		try {
			const info = await inspectHostShareFile(deps.fetchHost, located, signal);
			if (!info.resolvedPath) throw new CloudShareError("host_unsupported", "The machine this file is on is running an older Orchestrator. Update it to share a page's files.");
			return { ...located, relativePath: info.resolvedPath, size: info.size };
		} catch (error) {
			if (error instanceof CloudShareError && hostRefusedFile(error)) {
				throw new CloudShareError("invalid_file", error.message, error.status);
			}
			throw error;
		}
	};

	// Downloaded into a private snapshot through the host's proxy, then
	// uploaded as a local file is. Downloading is the first half of the
	// progress, uploading the second. An HTML page's files are downloaded
	// through the proxy too and go with it, as a local page's do.
	const publishHostFile = async (file: CloudHostShareFile, sender: IpcMainInvokeEvent["sender"], controller: AbortController): Promise<CloudSharePublishResult> => {
		if (isUnplayableShareVideo(file.path)) return { status: "unsupported", message: UNPLAYABLE_VIDEO_MESSAGE };
		if (!(await deps.getToken())) return { status: "signed_out" };
		const located = hostFile(file);
		const key = await hostRecordKey(file, located.relativePath);
		const shareId = await deps.records.get(key);
		const progress = (sentBytes: number, totalBytes: number) => {
			if (!sender.isDestroyed()) sender.send(CLOUD_SHARE_PROGRESS_CHANNEL, { sessionId: file.sessionId, path: file.path, sentBytes, totalBytes });
		};
		// The file as it is now, for the panel to show and publish again.
		const changed = async (): Promise<CloudSharePublishResult> => {
			const now = await inspectHostShareFile(deps.fetchHost, located, controller.signal);
			return { status: "changed", inspection: { name: now.name, size: now.size, unsupported: false, fingerprint: now.fingerprint, ...(shareId ? { shareId } : {}) } };
		};
		const expected = await inspectHostShareFile(deps.fetchHost, located, controller.signal);
		// Publish only the file as the panel showed it: one rewritten since is offered again.
		if (!file.fingerprint || !sameShareFingerprint(expected.fingerprint, file.fingerprint)) return await changed();
		// A page's files are found in its downloaded copy, so the progress
		// starts once they are, when it knows every byte.
		const page = isSharePage(located.name) && expected.size <= MAX_SCANNED_PAGE;
		let snapshot: ShareSnapshot | undefined;
		const fileSnapshots: ShareSnapshot[] = [];
		try {
			try {
				snapshot = await snapshotHostShareFile(deps.fetchHost, located, deps.snapshotDir(), {
					expectedSize: expected.size,
					signal: controller.signal,
					...(page ? {} : { onProgress: (copied: number) => progress(copied, expected.size * 2) }),
				});
			} catch (error) {
				if (error instanceof CloudShareError && error.code === "source_changed") return await changed();
				throw error;
			}
			const taken = snapshot;
			// The download names its own version: one written between the check and the download is offered again.
			if (!sameShareFingerprint(taken.fingerprint, file.fingerprint)) return await changed();
			if (controller.signal.aborted) return { status: "cancelled" };
			let copied = taken.size;
			let total = taken.size * 2;
			let pageFiles: (PageFilesTaken & { fingerprints: Record<string, string> }) | undefined;
			if (page) {
				const html = await fs.readFile(taken.path, "utf8");
				const found = await findPageFiles(located.relativePath, html, (relative) => hostPageFile(file, relative, controller.signal));
				total = (taken.size + found.files.reduce((sum, used) => sum + used.file.size, 0)) * 2;
				// What each file was as it downloaded, and what was missing, so a later change shows.
				pageFiles = { files: [], skipped: [...found.skipped], referenced: new Map(), fingerprints: {} };
				for (const skip of found.skipped) if (skip.relativePath) pageFiles.fingerprints[skip.relativePath] = "";
				// A few download at once, each a round trip or two to the host;
				// progress is the bytes of every one so far.
				const downloaded = new Array<number>(found.files.length).fill(0);
				const results = new Array<ShareSnapshot | PageFileSkip | undefined>(found.files.length);
				const base = copied;
				const report = () => progress(base + downloaded.reduce((sum, bytes) => sum + bytes, 0), total);
				let next = 0;
				let failed = false;
				const worker = async () => {
					while (!failed && !controller.signal.aborted && next < found.files.length) {
						const index = next++;
						const used = found.files[index];
						try {
							const copy = await snapshotHostShareFile(deps.fetchHost, used.file, deps.snapshotDir(), {
								expectedSize: used.file.size,
								expectedResolvedPath: used.file.relativePath,
								signal: controller.signal,
								onProgress: (bytes) => { downloaded[index] = bytes; report(); },
							});
							fileSnapshots.push(copy);
							results[index] = copy;
						} catch (error) {
							if (error instanceof CloudShareError && hostRefusedFile(error)) {
								// One the host refuses to serve now is left out, as it is when it is looked up.
								results[index] = { reference: used.path, reason: "outside" };
							} else if (error instanceof CloudShareError && (error.code === "file_missing" || error.code === "source_changed")) {
								// A file gone or rewritten on the host while it downloaded is left out, not the page.
								results[index] = { reference: used.path, reason: "missing", relativePath: used.referencedPath };
							} else {
								failed = true;
								throw error;
							}
						}
						downloaded[index] = used.file.size;
					}
				};
				// Every download settles before a failure is thrown, so each copy made is cleaned up.
				const settled = await Promise.allSettled(Array.from({ length: Math.min(HOST_PAGE_FILE_DOWNLOADS, found.files.length) }, worker));
				const failure = settled.find((result): result is PromiseRejectedResult => result.status === "rejected");
				if (failure) throw failure.reason;
				if (controller.signal.aborted) return { status: "cancelled" };
				// In the page's order, as a local page's are.
				found.files.forEach((used, index) => {
					const result = results[index];
					if (!result) return;
					if ("reason" in result) {
						pageFiles?.skipped.push(result);
						if (result.relativePath && pageFiles) pageFiles.fingerprints[result.relativePath] = "";
						return;
					}
					pageFiles?.files.push({ path: used.path, snapshot: result });
					pageFiles?.referenced.set(used.path, used.referencedPath);
					if (pageFiles) pageFiles.fingerprints[used.referencedPath] = hostPageFileVersion(used.file.relativePath, result.fingerprint);
				});
				copied = base + found.files.reduce((sum, used) => sum + used.file.size, 0);
			}
			const folder = path.posix.dirname(located.relativePath);
			const result = await uploadShare(deps, {
				baseUrl: file.baseUrl,
				snapshot: taken,
				name: located.name,
				project: file.projectId,
				feature: folder === "." ? file.sessionId : `${file.sessionId}/${folder}`,
				shareId: file.shareId,
				access: file.access,
				emails: file.emails,
				...(pageFiles ? { page: { files: pageFiles.files } } : {}),
			}, (sentBytes) => progress(copied + sentBytes, total), controller.signal);
			// A video refused now is offered again once the control plane takes it.
			const outcome = pageFiles ? pageFilesOutcome(pageFiles, result.pageFiles, (relativePath) => { pageFiles.fingerprints[relativePath] = ""; }) : undefined;
			// The link serves the page's files only when the control plane took them.
			const served = pageFiles && result.pageFiles && !result.pageFiles.unsupported ? pageFiles.fingerprints : undefined;
			await deps.records.set(key, result.share.id, taken.fingerprint, artifactShareUrl(result.share), shareAccess(result.share), served);
			return { status: "published", share: result.share, replaced: result.replaced, ...(outcome ? { pageFiles: outcome } : {}) };
		} finally {
			for (const copy of [snapshot, ...fileSnapshots]) {
				if (copy) await fs.rm(copy.path, { force: true });
			}
		}
	};

	// What the panel and the Publish button show: the file's link, whether the
	// file changed since it was published there, and the fingerprint a publish
	// names. Read from this computer's record and the file's metadata (a cloud
	// file's listed SHA-256): the file itself is never read.
	// linkOnly reads the link alone, for a delete that unpublishes it: whether
	// the page's files changed is not looked up.
	deps.ipcMain.handle("cloudShare:inspect", async (event, value: unknown, options?: unknown): Promise<CloudShareInspection> => {
		requireMainFrame(event);
		const file = shareFileArgument(value);
		const linkOnly = (options as { linkOnly?: unknown } | undefined)?.linkOnly === true;
		if (isCloudFile(file)) return inspectCloudFile(file, false, linkOnly);
		if (isHostFile(file)) return inspectHostFile(file, linkOnly);
		const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
		const key = await recordKey(deps.records, file.baseUrl, file.sessionId, resolved.relativePath, file.artifactId);
		const shareId = await deps.records.get(key);
		const publishedFingerprint = shareId ? await deps.records.publishedFingerprint(key) : undefined;
		const pageFiles = shareId && !linkOnly ? await deps.records.publishedPageFiles(key) : undefined;
		return {
			name: resolved.name,
			size: resolved.size,
			unsupported: isUnplayableShareVideo(resolved.name),
			fingerprint: await statShareFile(resolved),
			...(shareId ? { shareId } : {}),
			...(publishedFingerprint ? { publishedFingerprint } : {}),
			...(pageFiles && await pageFilesChanged(deps.daemonDataDir(), file.sessionId, pageFiles) ? { pageFilesChanged: true } : {}),
		};
	});

	deps.ipcMain.handle("cloudShare:available", async (_event, value: unknown): Promise<boolean | null> =>
		shareLinksAvailable(deps, baseUrlArgument(value)));

	deps.ipcMain.handle("cloudShare:list", async (_event, value: unknown): Promise<CloudShareLinks> => {
		const baseUrl = baseUrlArgument(value);
		// A link published, changed or unpublished here while the list was in
		// flight keeps what that write recorded: the list predates it.
		const since = deps.records.generation();
		const links = await listShareLinks(deps, baseUrl);
		await deps.records.syncLinks(baseUrl, links.shares, since);
		return links;
	});

	// The links this computer published remote hosts' files to. Their daemons
	// have no copy of the records, so the renderer marks their files from these.
	deps.ipcMain.handle("cloudShare:hostLinks", async (_event, value: unknown): Promise<HostShareLink[]> =>
		hostShareLinks(await deps.records.list(), baseUrlArgument(value)));

	deps.ipcMain.handle("cloudShare:unpublish", async (event, value: unknown): Promise<void> => {
		requireMainFrame(event);
		const shareId = shareIdArgument(value);
		await unpublishShareLink(deps, baseUrlArgument(value), shareId);
		await deps.records.forgetShare(shareId);
	});

	// Who a published link opens for. Like publishing, only the app's own
	// window may change it: making a link public is the person's decision.
	deps.ipcMain.handle("cloudShare:setAccess", async (event, value: unknown): Promise<CloudShareLink> => {
		requireMainFrame(event);
		const access = accessArgument((value as { access?: unknown } | null)?.access);
		if (!access) throw new CloudShareError("invalid_request", "Not who a link opens for.");
		const share = await setShareLinkAccess(deps, baseUrlArgument(value), shareIdArgument(value), access);
		// The daemon serves the artifact's access from the record, so every list marks it at once.
		await deps.records.setAccess(share.id, shareAccess(share));
		return share;
	});

	deps.ipcMain.handle("cloudShare:grants", async (_event, value: unknown): Promise<CloudShareGrants> =>
		listShareGrants(deps, baseUrlArgument(value), shareIdArgument(value)));

	deps.ipcMain.handle("cloudShare:invite", async (event, value: unknown): Promise<CloudShareGrants> => {
		requireMainFrame(event);
		const invite = inviteArgument(value);
		if ((invite.emails?.length ?? 0) === 0 && (invite.teamIds?.length ?? 0) === 0 && !invite.workspace) {
			throw new CloudShareError("invalid_request", "Not a list of emails.");
		}
		return inviteShareGrants(deps, baseUrlArgument(value), shareIdArgument(value), invite);
	});

	deps.ipcMain.handle("cloudShare:removeGrant", async (event, value: unknown): Promise<void> => {
		requireMainFrame(event);
		const grantId = (value as { grantId?: unknown } | null)?.grantId;
		if (typeof grantId !== "string") throw new CloudShareError("invalid_request", "Not someone the link is shared with.");
		await removeShareGrant(deps, baseUrlArgument(value), shareIdArgument(value), grantId);
	});

	deps.ipcMain.handle("cloudShare:cancel", (event) => {
		inFlight.get(event.sender.id)?.abort();
	});

	deps.ipcMain.handle("cloudShare:publish", async (event, value: unknown): Promise<CloudSharePublishResult> => {
		requireMainFrame(event);
		const file = shareFileArgument(value);
		const sender = event.sender;
		if (inFlight.has(sender.id)) return { status: "error", message: "Another file is being shared." };
		// Hold the window's slot before anything waits, so a second Publish
		// is refused rather than racing this one.
		const controller = new AbortController();
		inFlight.set(sender.id, controller);
		if (isCloudFile(file)) {
			try {
				return await publishCloudFile(file);
			} catch (error) {
				return refusal(error);
			} finally {
				inFlight.delete(sender.id);
			}
		}
		if (isHostFile(file)) {
			try {
				return await publishHostFile(file, sender, controller);
			} catch (error) {
				if (controller.signal.aborted) return { status: "cancelled" };
				return refusal(error);
			} finally {
				inFlight.delete(sender.id);
			}
		}
		let snapshot: ShareSnapshot | undefined;
		const fileSnapshots: ShareSnapshot[] = [];
		try {
			const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
			if (isUnplayableShareVideo(resolved.name)) return { status: "unsupported", message: UNPLAYABLE_VIDEO_MESSAGE };
			if (!(await deps.getToken())) return { status: "signed_out" };
			const progress = (sentBytes: number, totalBytes: number) => {
				if (!sender.isDestroyed()) sender.send(CLOUD_SHARE_PROGRESS_CHANNEL, { sessionId: file.sessionId, path: file.path, sentBytes, totalBytes });
			};
			const key = await recordKey(deps.records, file.baseUrl, file.sessionId, resolved.relativePath, file.artifactId);
			const shareId = await deps.records.get(key);
			// The file as it is now, for the panel to show and publish again.
			const changed = async (): Promise<CloudSharePublishResult> => {
				const now = await fs.stat(resolved.realPath);
				return {
					status: "changed",
					inspection: {
						name: resolved.name,
						size: now.size,
						unsupported: false,
						fingerprint: shareFileFingerprint(now),
						...(shareId ? { shareId } : {}),
					},
				};
			};
			// An HTML page's files go with it. They are found before it is
			// copied, so the progress knows every byte from the start. A page
			// rewritten after this is refused below, as any changed file is, so
			// what is found is what the copy uses.
			const html = isSharePage(resolved.name) ? await readShareFileText(resolved, MAX_SCANNED_PAGE) : null;
			const found = html === null ? undefined : await collectPageFiles(deps.daemonDataDir(), file.sessionId, resolved.relativePath, html);
			// Preparing the snapshots is the first half of the progress, uploading them the second.
			const total = (resolved.size + (found?.files.reduce((sum, used) => sum + used.file.size, 0) ?? 0)) * 2;
			try {
				snapshot = await snapshotShareFile(resolved, deps.snapshotDir(), {
					signal: controller.signal,
					onProgress: (copied) => progress(copied, total),
				});
			} catch (error) {
				// Written to while it was copied: nothing is published, and the newest version is offered.
				if (error instanceof CloudShareError && error.code === "source_changed") return await changed();
				throw error;
			}
			const taken = snapshot;
			// Publish only the file as the panel showed it: one rewritten since is offered again.
			if (!file.fingerprint || !sameShareFingerprint(taken.fingerprint, file.fingerprint)) return await changed();
			if (controller.signal.aborted) return { status: "cancelled" };
			let copied = taken.size;
			let pageFiles: (PageFilesTaken & { fingerprints: Record<string, string> }) | undefined;
			if (found) {
				const files: PageFileSnapshot[] = [];
				const skipped = [...found.skipped];
				// What each file held as it was copied, and what was missing, so a later change shows.
				const fingerprints: Record<string, string> = {};
				// Which file the page names by each included path.
				const referenced = new Map<string, string>();
				for (const skip of found.skipped) if (skip.relativePath) fingerprints[skip.relativePath] = "";
				for (const used of found.files) {
					if (controller.signal.aborted) return { status: "cancelled" };
					const before = copied;
					try {
						const copy = await snapshotShareFile(used.file, deps.snapshotDir(), {
							signal: controller.signal,
							onProgress: (bytes) => progress(before + bytes, total),
						});
						fileSnapshots.push(copy);
						files.push({ path: used.path, snapshot: copy });
						referenced.set(used.path, used.referencedPath);
						fingerprints[used.referencedPath] = pageFileVersion(used.file, copy.fingerprint);
					} catch (error) {
						// A file gone or rewritten while it was copied is left out, not the page.
						if (!sourceFileGone(error, used.file.realPath)) throw error;
						skipped.push({ reference: used.path, reason: "missing", relativePath: used.referencedPath });
						fingerprints[used.referencedPath] = "";
					}
					copied = before + used.file.size;
				}
				pageFiles = { files, skipped, fingerprints, referenced };
			}
			// The document is the file's path in its session's artifacts, in its project.
			const folder = path.posix.dirname(resolved.relativePath);
			const result = await uploadShare(deps, {
				baseUrl: file.baseUrl,
				snapshot: taken,
				name: resolved.name,
				project: file.projectId,
				feature: folder === "." ? file.sessionId : `${file.sessionId}/${folder}`,
				shareId: file.shareId,
				access: file.access,
				emails: file.emails,
				...(pageFiles ? { page: { files: pageFiles.files } } : {}),
			}, (sentBytes) => progress(copied + sentBytes, total), controller.signal);
			// A video refused now is offered again once the control plane takes it.
			const outcome = pageFiles ? pageFilesOutcome(pageFiles, result.pageFiles, (relativePath) => { pageFiles.fingerprints[relativePath] = ""; }) : undefined;
			// The link serves the page's files only when the control plane took them.
			const served = pageFiles && result.pageFiles && !result.pageFiles.unsupported ? pageFiles.fingerprints : undefined;
			await deps.records.set(key, result.share.id, taken.fingerprint, artifactShareUrl(result.share), shareAccess(result.share), served);
			return { status: "published", share: result.share, replaced: result.replaced, ...(outcome ? { pageFiles: outcome } : {}) };
		} catch (error) {
			if (controller.signal.aborted) return { status: "cancelled" };
			return refusal(error);
		} finally {
			inFlight.delete(sender.id);
			for (const copy of [snapshot, ...fileSnapshots]) {
				if (copy) await fs.rm(copy.path, { force: true });
			}
		}
	});
}
