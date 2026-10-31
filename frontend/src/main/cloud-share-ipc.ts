/**
 * cloudShare IPC: the only way to publish a share link. Agents, the ao CLI,
 * MCP and the daemon have no route to it; the renderer can only ask, and the
 * main process asks the person in a native dialog the renderer cannot answer.
 */

import { promises as fs } from "node:fs";
import path from "node:path";
import type { IpcMain, WebContents } from "electron";
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

/** What the native confirmation shows. */
export interface CloudShareConfirmation {
	name: string;
	size: number;
	secretFindings: string[];
	/** "2 of 3 active links" on the free plan; undefined when unlimited. */
	usage?: { used: number; limit: number };
	update: boolean;
}

export interface CloudShareIpcDeps extends CloudShareApi {
	ipcMain: Pick<IpcMain, "handle">;
	/** The daemon's data directory, which holds artifacts/<session>. */
	daemonDataDir: () => string;
	/** A private directory for upload snapshots. */
	snapshotDir: () => string;
	/** Asks the person to confirm publishing; true only on their explicit yes. */
	confirm: (sender: WebContents, confirmation: CloudShareConfirmation) => Promise<boolean>;
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
		return {
			name: resolved.name,
			size: resolved.size,
			secretFindings: await scanShareFile(resolved),
			unsupported: isVideo(resolved.name),
		};
	});

	deps.ipcMain.handle("cloudShare:list", async (_event, value: unknown): Promise<CloudShareLinks> =>
		listShareLinks(deps, baseUrlArgument(value)));

	deps.ipcMain.handle("cloudShare:unpublish", async (_event, value: unknown): Promise<void> => {
		const shareId = (value as { shareId?: unknown } | null)?.shareId;
		if (typeof shareId !== "string") throw new CloudShareError("invalid_request", "Not a share link.");
		await unpublishShareLink(deps, baseUrlArgument(value), shareId);
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
		let links: CloudShareLinks;
		try {
			links = await listShareLinks(deps, file.baseUrl);
		} catch (error) {
			return refusal(error);
		}
		const controller = new AbortController();
		inFlight.set(sender.id, controller);
		const snapshot = await snapshotShareFile(resolved, deps.snapshotDir()).catch((error: unknown) => {
			inFlight.delete(sender.id);
			throw error;
		});
		try {
			const confirmed = await deps.confirm(sender, {
				name: resolved.name,
				size: snapshot.size,
				secretFindings: snapshot.secretFindings,
				usage: links.unlimited ? undefined : { used: links.used, limit: links.limit },
				update: Boolean(file.shareId),
			});
			if (!confirmed || controller.signal.aborted) return { status: "cancelled" };
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

/** Formats a byte count for the confirmation dialog. */
export function formatShareSize(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	const units = ["KB", "MB", "GB", "TB"];
	let value = bytes / 1024;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit++;
	}
	return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

/** The native confirmation's text. */
export function confirmationText(confirmation: CloudShareConfirmation): { message: string; detail: string; button: string } {
	const lines = [
		`${confirmation.name} · ${formatShareSize(confirmation.size)}`,
		"Anyone with the link can view this file.",
	];
	if (confirmation.usage) {
		const used = confirmation.update ? confirmation.usage.used : confirmation.usage.used + 1;
		lines.push(`Free plan: ${used} of ${confirmation.usage.limit} active links.`);
	}
	if (confirmation.secretFindings.length > 0) {
		lines.push("", `This file looks like it contains a ${confirmation.secretFindings.join(", ")}. Publishing it makes that public.`);
	}
	return {
		message: confirmation.update ? "Update the public link to this file?" : "Publish this file as a public link?",
		detail: lines.join("\n"),
		button: confirmation.update ? "Update link" : "Publish",
	};
}
