/**
 * cloudShare IPC: the only way to publish a share link. Agents, the ao CLI,
 * MCP and the daemon have no route to it; only the app's own window's main
 * frame can ask, when the person presses Publish in its share popover. A
 * local file is uploaded from here; a cloud session's file is copied by the
 * control plane. See shared/cloud-share.ts for what is and is not confirmed.
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
	type CloudSessionShareFile,
} from "../shared/cloud-share";
import {
	CloudShareError,
	type CloudShareApi,
	inspectCloudSessionArtifact,
	listShareLinks,
	resolveShareFile,
	shareLinksAvailable,
	scanShareFile,
	shareCloudSessionArtifact,
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
	const { source, baseUrl, sessionId, path: filePath, projectId, orgId, shareId, fingerprint } = value as Record<string, unknown>;
	if (typeof baseUrl !== "string" || typeof sessionId !== "string" || typeof filePath !== "string" ||
		(shareId !== undefined && typeof shareId !== "string") || (fingerprint !== undefined && typeof fingerprint !== "string")) {
		throw new CloudShareError("invalid_request", "Not a file to share.");
	}
	if (source === "cloud") {
		if (typeof orgId !== "string") throw new CloudShareError("invalid_request", "Not a file to share.");
		return { source, baseUrl, orgId, sessionId, path: filePath, projectId: "", shareId, fingerprint } satisfies CloudSessionShareFile;
	}
	// A local file's project names the document: an empty one would make a
	// second document, and link, for a file already published.
	if (source !== undefined && source !== "local") throw new CloudShareError("invalid_request", "Not a file to share.");
	if (typeof projectId !== "string" || projectId === "") throw new CloudShareError("invalid_request", "Not a file to share.");
	return { baseUrl, sessionId, path: filePath, projectId, shareId, fingerprint };
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
		}
		return { status: "error", message: error.message };
	}
	throw error;
}

export function registerCloudShareIpc(deps: CloudShareIpcDeps): void {
	const inFlight = new Map<number, AbortController>();

	// A cloud session's file is named by its committed version: the
	// fingerprint is its SHA-256, and the control plane refuses to share
	// another version.
	const inspectCloudFile = async (file: CloudSessionShareFile): Promise<CloudShareInspection> => {
		const found = await inspectCloudSessionArtifact(deps, file.baseUrl, file);
		const shareId = await deps.records.get(cloudRecordKey(file.baseUrl, file));
		return {
			name: found.name,
			size: found.size,
			secretFindings: found.secretFindings,
			unsupported: isVideo(found.name),
			fingerprint: found.sha256,
			...(shareId ? { shareId } : {}),
		};
	};

	const publishCloudFile = async (file: CloudSessionShareFile, signal: AbortSignal): Promise<CloudSharePublishResult> => {
		if (isVideo(file.path)) return { status: "unsupported", message: "Sharing videos is coming soon." };
		if (!(await deps.getToken())) return { status: "signed_out" };
		if (!file.fingerprint) return { status: "changed", inspection: await inspectCloudFile(file) };
		try {
			// The control plane copies the file inside its bucket: there is
			// nothing to upload. Cancel stops waiting for it; sharing the file
			// again later reuses the file's link either way.
			const result = await shareCloudSessionArtifact(deps, file.baseUrl, { ...file, sha256: file.fingerprint }, signal);
			await deps.records.set(cloudRecordKey(file.baseUrl, file), result.share.id);
			return { status: "published", share: result.share, replaced: result.replaced };
		} catch (error) {
			if (error instanceof CloudShareError && error.code === "artifact_share_source_changed") {
				return { status: "changed", inspection: await inspectCloudFile(file) };
			}
			throw error;
		}
	};

	deps.ipcMain.handle("cloudShare:inspect", async (event, value: unknown): Promise<CloudShareInspection> => {
		requireMainFrame(event);
		const file = shareFileArgument(value);
		if (isCloudFile(file)) return inspectCloudFile(file);
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

	deps.ipcMain.handle("cloudShare:available", async (_event, value: unknown): Promise<boolean | null> =>
		shareLinksAvailable(deps, baseUrlArgument(value)));

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
		if (inFlight.has(sender.id)) return { status: "error", message: "Another file is being shared." };
		// Hold the window's slot before anything waits, so a second Publish
		// is refused rather than racing this one.
		const controller = new AbortController();
		inFlight.set(sender.id, controller);
		if (isCloudFile(file)) {
			try {
				return await publishCloudFile(file, controller.signal);
			} catch (error) {
				if (controller.signal.aborted) return { status: "cancelled" };
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
