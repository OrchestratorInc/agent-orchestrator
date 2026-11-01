/**
 * cloudShare IPC: the only way to publish a share link. Agents, the ao CLI,
 * MCP and the daemon have no route to it; only the app's own window can ask,
 * when the person presses Publish in its share popover.
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
} from "../shared/cloud-share";
import {
	CloudShareError,
	type CloudShareApi,
	listShareLinks,
	resolveShareFile,
	scanShareFile,
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
	const { baseUrl, sessionId, path: filePath, projectId, shareId } = value as Record<string, unknown>;
	if (typeof baseUrl !== "string" || typeof sessionId !== "string" || typeof filePath !== "string" || typeof projectId !== "string" ||
		(shareId !== undefined && typeof shareId !== "string")) {
		throw new CloudShareError("invalid_request", "Not a file to share.");
	}
	return { baseUrl, sessionId, path: filePath, projectId, shareId };
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
