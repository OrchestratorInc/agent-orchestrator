/**
 * cloudShare IPC: the only way to publish a share link. Agents, the ao CLI,
 * MCP and the daemon have no route to it; only the app's own window's main
 * frame can ask, when the person presses Publish in its share popover. See
 * shared/cloud-share.ts for what is and is not confirmed.
 */

import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, IpcMainInvokeEvent } from "electron";
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
	type ShareSnapshot,
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
	const { baseUrl, sessionId, path: filePath, projectId, shareId, fingerprint } = value as Record<string, unknown>;
	// The project names the document: an empty one would make a second
	// document, and link, for a file already published.
	if (typeof baseUrl !== "string" || typeof sessionId !== "string" || typeof filePath !== "string" || typeof projectId !== "string" || projectId === "" ||
		(shareId !== undefined && typeof shareId !== "string") || (fingerprint !== undefined && typeof fingerprint !== "string")) {
		throw new CloudShareError("invalid_request", "Not a file to share.");
	}
	return { baseUrl, sessionId, path: filePath, projectId, shareId, fingerprint };
}

function baseUrlArgument(value: unknown): string {
	const baseUrl = (value as { baseUrl?: unknown } | null)?.baseUrl;
	if (typeof baseUrl !== "string") throw new CloudShareError("invalid_request", "Not a control plane.");
	return baseUrl;
}

/** Refuses a call from anything but a window's main frame, where the bridge lives. */
function requireMainFrame(event: unknown): void {
	const { sender, senderFrame } = event as Partial<IpcMainInvokeEvent>;
	if (senderFrame === null || (senderFrame && sender?.mainFrame && senderFrame !== sender.mainFrame)) {
		throw new CloudShareError("invalid_request", "Sharing is only available from the app window.");
	}
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

	deps.ipcMain.handle("cloudShare:inspect", async (event, value: unknown): Promise<CloudShareInspection> => {
		requireMainFrame(event);
		const file = shareFileArgument(value);
		const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
		const shareId = await deps.records.get(shareRecordKey(file.baseUrl, file.sessionId, resolved.relativePath));
		const { secretFindings, fingerprint } = await scanShareFile(resolved);
		return {
			name: resolved.name,
			size: resolved.size,
			secretFindings,
			unsupported: isVideo(resolved.name),
			fingerprint,
			...(shareId ? { shareId } : {}),
		};
	});

	deps.ipcMain.handle("cloudShare:list", async (_event, value: unknown): Promise<CloudShareLinks> =>
		listShareLinks(deps, baseUrlArgument(value)));

	deps.ipcMain.handle("cloudShare:unpublish", async (event, value: unknown): Promise<void> => {
		requireMainFrame(event);
		const shareId = (value as { shareId?: unknown } | null)?.shareId;
		if (typeof shareId !== "string") throw new CloudShareError("invalid_request", "Not a share link.");
		await unpublishShareLink(deps, baseUrlArgument(value), shareId);
		await deps.records.forgetShare(shareId);
	});

	deps.ipcMain.handle("cloudShare:cancel", (event) => {
		inFlight.get(event.sender.id)?.abort();
	});

	deps.ipcMain.handle("cloudShare:publish", async (event, value: unknown): Promise<CloudSharePublishResult> => {
		requireMainFrame(event);
		const file = shareFileArgument(value);
		const sender = event.sender;
		if (inFlight.has(sender.id)) throw new CloudShareError("busy", "Another file is being shared.");
		// Hold the window's slot before anything waits, so a second Publish
		// is refused rather than racing this one.
		const controller = new AbortController();
		inFlight.set(sender.id, controller);
		let snapshot: ShareSnapshot | undefined;
		try {
			const resolved = await resolveShareFile(deps.daemonDataDir(), file.sessionId, file.path);
			if (isVideo(resolved.name)) return { status: "unsupported", message: "Sharing videos is coming soon." };
			if (!(await deps.getToken())) return { status: "signed_out" };
			const progress = (sentBytes: number, totalBytes: number) => {
				if (!sender.isDestroyed()) sender.send(CLOUD_SHARE_PROGRESS_CHANNEL, { sessionId: file.sessionId, path: file.path, sentBytes, totalBytes });
			};
			// Preparing the snapshot is the first half of the progress, uploading it the second.
			snapshot = await snapshotShareFile(resolved, deps.snapshotDir(), {
				signal: controller.signal,
				onProgress: (copied) => progress(copied, resolved.size * 2),
			});
			const taken = snapshot;
			// Publish only what the person was shown: the file as inspected,
			// with no credential the inspection did not report.
			const shareId = await deps.records.get(shareRecordKey(file.baseUrl, file.sessionId, resolved.relativePath));
			if (taken.fingerprint !== file.fingerprint) {
				return {
					status: "changed",
					inspection: {
						name: resolved.name,
						size: taken.size,
						secretFindings: taken.secretFindings,
						unsupported: false,
						fingerprint: taken.fingerprint,
						...(shareId ? { shareId } : {}),
					},
				};
			}
			if (controller.signal.aborted) return { status: "cancelled" };
			// The document is the file's path in its session's artifacts, in its project.
			const folder = path.posix.dirname(resolved.relativePath);
			const result = await uploadShare(deps, {
				baseUrl: file.baseUrl,
				snapshot: taken,
				name: resolved.name,
				project: file.projectId,
				feature: folder === "." ? file.sessionId : `${file.sessionId}/${folder}`,
				shareId: file.shareId,
			}, (sentBytes) => progress(taken.size + sentBytes, taken.size * 2), controller.signal);
			await deps.records.set(shareRecordKey(file.baseUrl, file.sessionId, resolved.relativePath), result.share.id);
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
