import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, IpcMainInvokeEvent } from "electron";
import {
	artifactShareUrl,
	CLOUD_SHARE_PROGRESS_CHANNEL,
	sameShareFingerprint,
	type CloudShareAccess,
	type CloudShareFile,
	type CloudShareGrant,
	type CloudShareInspection,
	type CloudShareLinks,
	type CloudSharePublishResult,
	type CloudSessionShareFile,
	type CloudShareLink,
} from "../shared/cloud-share";
import {
	CloudShareError,
	type CloudShareApi,
	cloudSessionArtifactVersion,
	inviteShareGrants,
	listShareGrants,
	listShareLinks,
	removeShareGrant,
	setShareLinkAccess,
	resolveShareFile,
	shareLinksAvailable,
	shareCloudSessionArtifact,
	type ShareSnapshot,
	shareFileFingerprint,
	snapshotShareFile,
	statShareFile,
	unpublishShareLink,
	uploadShare,
} from "./cloud-share";
import { type ShareLinkRecordStore, artifactRecordKey, shareAccess, shareRecordKey } from "./cloud-share-links";

export interface CloudShareIpcDeps extends CloudShareApi {
	ipcMain: Pick<IpcMain, "handle">;
	daemonDataDir: () => string;
	snapshotDir: () => string;
	records: ShareLinkRecordStore;
}

const VIDEO_EXTENSIONS = new Set([".mp4", ".m4v", ".mov", ".webm", ".ogv", ".avi", ".mkv"]);

function isVideo(name: string): boolean {
	return VIDEO_EXTENSIONS.has(path.extname(name).toLowerCase());
}

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
	if (source !== undefined && source !== "local") throw new CloudShareError("invalid_request", "Not a file to share.");
	if (typeof projectId !== "string" || projectId === "") throw new CloudShareError("invalid_request", "Not a file to share.");
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

async function recordKey(records: ShareLinkRecordStore, baseUrl: string, sessionId: string, relativePath: string, artifactId: string | undefined): Promise<string> {
	const byPath = shareRecordKey(baseUrl, sessionId, relativePath);
	if (!artifactId) return byPath;
	const key = artifactRecordKey(baseUrl, sessionId, artifactId);
	await records.adopt(key, byPath);
	return key;
}

function cloudRecordKey(records: ShareLinkRecordStore, baseUrl: string, file: CloudSessionShareFile): Promise<string> {
	return recordKey(records, baseUrl, `cloud:${file.orgId}/${file.sessionId}`, file.path, file.artifactId);
}

function baseUrlArgument(value: unknown): string {
	const baseUrl = (value as { baseUrl?: unknown } | null)?.baseUrl;
	if (typeof baseUrl !== "string") throw new CloudShareError("invalid_request", "Not a control plane.");
	return baseUrl;
}

function requireMainFrame(event: unknown): void {
	const { senderFrame } = event as Partial<IpcMainInvokeEvent>;
	if (senderFrame === null || (senderFrame && senderFrame.parent !== null)) {
		throw new CloudShareError("invalid_request", "Sharing is only available from the app window.");
	}
}

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
		return { status: "error", message: error.message };
	}
	throw error;
}

export function registerCloudShareIpc(deps: CloudShareIpcDeps): void {
	const inFlight = new Map<number, AbortController>();

	const inspectCloudFile = async (file: CloudSessionShareFile, refresh = false): Promise<CloudShareInspection> => {
		const key = await cloudRecordKey(deps.records, file.baseUrl, file);
		const listed = refresh || !file.fingerprint ? await cloudSessionArtifactVersion(deps, file.baseUrl, file) : undefined;
		const name = listed?.name ?? (file.path.split("/").pop() || file.path);
		const shareId = await deps.records.get(key);
		const publishedFingerprint = shareId ? await deps.records.publishedFingerprint(key) : undefined;
		return {
			name,
			...(listed ? { size: listed.size } : {}),
			unsupported: isVideo(name),
			fingerprint: listed?.sha256 ?? file.fingerprint ?? "",
			...(shareId ? { shareId } : {}),
			...(publishedFingerprint ? { publishedFingerprint } : {}),
		};
	};

	const publishCloudFile = async (file: CloudSessionShareFile): Promise<CloudSharePublishResult> => {
		if (isVideo(file.path)) return { status: "unsupported", message: "Sharing videos is coming soon." };
		if (!(await deps.getToken())) return { status: "signed_out" };
		if (!file.fingerprint) return { status: "changed", inspection: await inspectCloudFile(file, true) };
		try {
			const result = await shareCloudSessionArtifact(deps, file.baseUrl, { ...file, sha256: file.fingerprint });
			const key = await cloudRecordKey(deps.records, file.baseUrl, file);
			await deps.records.set(key, result.share.id, file.fingerprint, artifactShareUrl(result.share), shareAccess(result.share));
			return { status: "published", share: result.share, replaced: result.replaced };
		} catch (error) {
			if (error instanceof CloudShareError && error.code === "artifact_share_source_changed") {
				return { status: "changed", inspection: await inspectCloudFile(file, true) };
			}
			throw error;
		}
	};

	deps.ipcMain.handle("cloudShare:inspect", async (event, value: unknown): Promise<CloudShareInspection> => {
		requireMainFrame(event);
		const file = shareFileArgument(value);
		if (isCloudFile(file)) return inspectCloudFile(file);
		const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
		const key = await recordKey(deps.records, file.baseUrl, file.sessionId, resolved.relativePath, file.artifactId);
		const shareId = await deps.records.get(key);
		const publishedFingerprint = shareId ? await deps.records.publishedFingerprint(key) : undefined;
		return {
			name: resolved.name,
			size: resolved.size,
			unsupported: isVideo(resolved.name),
			fingerprint: await statShareFile(resolved),
			...(shareId ? { shareId } : {}),
			...(publishedFingerprint ? { publishedFingerprint } : {}),
		};
	});

	deps.ipcMain.handle("cloudShare:available", async (_event, value: unknown): Promise<boolean | null> =>
		shareLinksAvailable(deps, baseUrlArgument(value)));

	deps.ipcMain.handle("cloudShare:list", async (_event, value: unknown): Promise<CloudShareLinks> => {
		const baseUrl = baseUrlArgument(value);
		const since = deps.records.generation();
		const links = await listShareLinks(deps, baseUrl);
		await deps.records.syncLinks(baseUrl, links.shares, since);
		return links;
	});

	deps.ipcMain.handle("cloudShare:unpublish", async (event, value: unknown): Promise<void> => {
		requireMainFrame(event);
		const shareId = shareIdArgument(value);
		await unpublishShareLink(deps, baseUrlArgument(value), shareId);
		await deps.records.forgetShare(shareId);
	});

	deps.ipcMain.handle("cloudShare:setAccess", async (event, value: unknown): Promise<CloudShareLink> => {
		requireMainFrame(event);
		const access = accessArgument((value as { access?: unknown } | null)?.access);
		if (!access) throw new CloudShareError("invalid_request", "Not who a link opens for.");
		const share = await setShareLinkAccess(deps, baseUrlArgument(value), shareIdArgument(value), access);
		await deps.records.setAccess(share.id, shareAccess(share));
		return share;
	});

	deps.ipcMain.handle("cloudShare:grants", async (_event, value: unknown): Promise<CloudShareGrant[]> =>
		listShareGrants(deps, baseUrlArgument(value), shareIdArgument(value)));

	deps.ipcMain.handle("cloudShare:invite", async (event, value: unknown): Promise<CloudShareGrant[]> => {
		requireMainFrame(event);
		const emails = emailsArgument((value as { emails?: unknown } | null)?.emails);
		if (!emails || emails.length === 0) throw new CloudShareError("invalid_request", "Not a list of emails.");
		return inviteShareGrants(deps, baseUrlArgument(value), shareIdArgument(value), emails);
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
		let snapshot: ShareSnapshot | undefined;
		try {
			const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
			if (isVideo(resolved.name)) return { status: "unsupported", message: "Sharing videos is coming soon." };
			if (!(await deps.getToken())) return { status: "signed_out" };
			const progress = (sentBytes: number, totalBytes: number) => {
				if (!sender.isDestroyed()) sender.send(CLOUD_SHARE_PROGRESS_CHANNEL, { sessionId: file.sessionId, path: file.path, sentBytes, totalBytes });
			};
			const key = await recordKey(deps.records, file.baseUrl, file.sessionId, resolved.relativePath, file.artifactId);
			const shareId = await deps.records.get(key);
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
			try {
				snapshot = await snapshotShareFile(resolved, deps.snapshotDir(), {
					signal: controller.signal,
					onProgress: (copied) => progress(copied, resolved.size * 2),
				});
			} catch (error) {
				if (error instanceof CloudShareError && error.code === "source_changed") return await changed();
				throw error;
			}
			const taken = snapshot;
			if (!file.fingerprint || !sameShareFingerprint(taken.fingerprint, file.fingerprint)) return await changed();
			if (controller.signal.aborted) return { status: "cancelled" };
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
			}, (sentBytes) => progress(taken.size + sentBytes, taken.size * 2), controller.signal);
			await deps.records.set(key, result.share.id, taken.fingerprint, artifactShareUrl(result.share), shareAccess(result.share));
			return { status: "published", share: result.share, replaced: result.replaced };
		} catch (error) {
			if (controller.signal.aborted) return { status: "cancelled" };
			return refusal(error);
		} finally {
			inFlight.delete(sender.id);
			if (snapshot) await fs.rm(snapshot.path, { force: true });
		}
	});
}
