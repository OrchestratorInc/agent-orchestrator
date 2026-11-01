/**
 * cloudShare IPC: the only way to publish a share link. Agents, the ao CLI,
 * MCP and the daemon have no route to it; only the app's own window can ask,
 * when the person presses Publish in its share popover. A local file is
 * uploaded from here; a cloud session's file is copied by the control plane.
 */

import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain } from "electron";
import {
	CLOUD_SHARE_PROGRESS_CHANNEL,
	type CloudShareFile,
	type CloudShareInspection,
	type CloudShareLinks,
	type CloudSharePublishResult,
	type CloudSessionShareFile,
} from "../shared/cloud-share";
import {
	CloudShareError,
	type CloudShareApi,
	inspectCloudSessionArtifact,
	listShareLinks,
	resolveShareFile,
	scanShareFile,
	shareCloudSessionArtifact,
	snapshotShareFile,
	unpublishShareLink,
	uploadShare,
} from "./cloud-share";
import { type ShareLinkRecordStore, shareRecordKey } from "./cloud-share-links";

export interface CloudShareIpcDeps extends CloudShareApi {
	ipcMain: Pick<IpcMain, "handle">;
	/** The daemon's data directory, which holds artifacts/<session>. */
	daemonDataDir: () => string;
	/** A private directory for upload snapshots. */
	snapshotDir: () => string;
	/** Which link each local file was last published to. */
	records: ShareLinkRecordStore;
}

const VIDEO_EXTENSIONS = new Set([".mp4", ".m4v", ".mov", ".webm", ".ogv", ".avi", ".mkv"]);

function isVideo(name: string): boolean {
	return VIDEO_EXTENSIONS.has(path.extname(name).toLowerCase());
}

function shareFileArgument(value: unknown): CloudShareFile {
	if (typeof value !== "object" || value === null) throw new CloudShareError("invalid_request", "Not a file to share.");
	const { source, baseUrl, sessionId, path: filePath, projectId, orgId, shareId } = value as Record<string, unknown>;
	if (typeof baseUrl !== "string" || typeof sessionId !== "string" || typeof filePath !== "string" ||
		(shareId !== undefined && typeof shareId !== "string")) {
		throw new CloudShareError("invalid_request", "Not a file to share.");
	}
	if (source === "cloud") {
		if (typeof orgId !== "string") throw new CloudShareError("invalid_request", "Not a file to share.");
		return { source, baseUrl, orgId, sessionId, path: filePath, projectId: "", shareId } satisfies CloudSessionShareFile;
	}
	if (source !== undefined && source !== "local") throw new CloudShareError("invalid_request", "Not a file to share.");
	if (typeof projectId !== "string") throw new CloudShareError("invalid_request", "Not a file to share.");
	return { baseUrl, sessionId, path: filePath, projectId, shareId };
}

function isCloudFile(file: CloudShareFile): file is CloudSessionShareFile {
	return file.source === "cloud" && typeof file.orgId === "string";
}

/** The record key of a cloud session's file: apart from every local session's. */
function cloudRecordKey(baseUrl: string, file: CloudSessionShareFile): string {
	return shareRecordKey(baseUrl, `cloud:${file.orgId}/${file.sessionId}`, file.path);
}

function baseUrlArgument(value: unknown): string {
	const baseUrl = (value as { baseUrl?: unknown } | null)?.baseUrl;
	if (typeof baseUrl !== "string") throw new CloudShareError("invalid_request", "Not a control plane.");
	return baseUrl;
}

/** Maps a refusal the person can act on to its result; rethrows the rest. */
function refusal(error: unknown): CloudSharePublishResult {
	if (error instanceof CloudShareError) {
		switch (error.code) {
			case "signed_out":
				return { status: "signed_out" };
			case "artifact_share_limit_reached":
				return { status: "limit", message: error.message };
			case "artifact_share_type_unsupported":
				return { status: "unsupported", message: error.message };
		}
	}
	throw error;
}

export function registerCloudShareIpc(deps: CloudShareIpcDeps): void {
	const inFlight = new Map<number, AbortController>();

	deps.ipcMain.handle("cloudShare:inspect", async (_event, value: unknown): Promise<CloudShareInspection> => {
		const file = shareFileArgument(value);
		if (isCloudFile(file)) {
			const found = await inspectCloudSessionArtifact(deps, file.baseUrl, file);
			const shareId = await deps.records.get(cloudRecordKey(file.baseUrl, file));
			return { ...found, unsupported: isVideo(found.name), ...(shareId ? { shareId } : {}) };
		}
		const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
		const shareId = await deps.records.get(shareRecordKey(file.baseUrl, file.sessionId, resolved.relativePath));
		return {
			name: resolved.name,
			size: resolved.size,
			secretFindings: await scanShareFile(resolved),
			unsupported: isVideo(resolved.name),
			...(shareId ? { shareId } : {}),
		};
	});

	deps.ipcMain.handle("cloudShare:list", async (_event, value: unknown): Promise<CloudShareLinks> =>
		listShareLinks(deps, baseUrlArgument(value)));

	deps.ipcMain.handle("cloudShare:unpublish", async (_event, value: unknown): Promise<void> => {
		const shareId = (value as { shareId?: unknown } | null)?.shareId;
		if (typeof shareId !== "string") throw new CloudShareError("invalid_request", "Not a share link.");
		await unpublishShareLink(deps, baseUrlArgument(value), shareId);
		await deps.records.forgetShare(shareId);
	});

	deps.ipcMain.handle("cloudShare:cancel", (event) => {
		inFlight.get(event.sender.id)?.abort();
	});

	deps.ipcMain.handle("cloudShare:publish", async (event, value: unknown): Promise<CloudSharePublishResult> => {
		const file = shareFileArgument(value);
		const sender = event.sender;
		if (inFlight.has(sender.id)) throw new CloudShareError("busy", "Another file is being shared.");
		if (isCloudFile(file)) {
			if (isVideo(file.path)) return { status: "unsupported", message: "Sharing videos is coming soon." };
			if (!(await deps.getToken())) return { status: "signed_out" };
			// The control plane copies the file inside its bucket: there is
			// nothing to upload. Cancel stops waiting for it; sharing the file
			// again later reuses the file's link either way.
			const controller = new AbortController();
			inFlight.set(sender.id, controller);
			try {
				const result = await shareCloudSessionArtifact(deps, file.baseUrl, file, controller.signal);
				await deps.records.set(cloudRecordKey(file.baseUrl, file), result.share.id);
				return { status: "published", share: result.share, replaced: result.replaced };
			} catch (error) {
				if (controller.signal.aborted) return { status: "cancelled" };
				return refusal(error);
			} finally {
				inFlight.delete(sender.id);
			}
		}
		const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
		if (isVideo(resolved.name)) return { status: "unsupported", message: "Sharing videos is coming soon." };
		if (!(await deps.getToken())) return { status: "signed_out" };
		const controller = new AbortController();
		inFlight.set(sender.id, controller);
		const snapshot = await snapshotShareFile(resolved, deps.snapshotDir()).catch((error: unknown) => {
			inFlight.delete(sender.id);
			throw error;
		});
		try {
			if (controller.signal.aborted) return { status: "cancelled" };
			// The document is the file's path in its session's artifacts, in its project.
			const folder = path.posix.dirname(resolved.relativePath);
			const result = await uploadShare(deps, {
				baseUrl: file.baseUrl,
				snapshot,
				name: resolved.name,
				project: file.projectId,
				feature: folder === "." ? file.sessionId : `${file.sessionId}/${folder}`,
				shareId: file.shareId,
			}, (sentBytes) => {
				if (!sender.isDestroyed()) {
					sender.send(CLOUD_SHARE_PROGRESS_CHANNEL, { sessionId: file.sessionId, path: file.path, sentBytes, totalBytes: snapshot.size });
				}
			}, controller.signal);
			await deps.records.set(shareRecordKey(file.baseUrl, file.sessionId, resolved.relativePath), result.share.id);
			return { status: "published", share: result.share, replaced: result.replaced };
		} catch (error) {
			if (controller.signal.aborted) return { status: "cancelled" };
			return refusal(error);
		} finally {
			inFlight.delete(sender.id);
			await fs.rm(snapshot.path, { force: true });
		}
	});
}
