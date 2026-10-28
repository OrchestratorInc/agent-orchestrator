/**
 * Saves a cloud session's artifact file to disk.
 *
 * A cloud artifact link is cross-origin to the renderer, and the cloud API
 * bridge carries text bodies only, so the main process reads the file through
 * the sender's own session. It streams the response straight into the file the
 * user picked, so an artifact of any size the control plane accepts saves
 * without being held in memory or copied across processes. Only the control
 * plane's artifact file route is read.
 */

import { createWriteStream } from "node:fs";
import { rename, rm } from "node:fs/promises";
import path from "node:path";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import type { ReadableStream as WebReadableStream } from "node:stream/web";
import type { Dialog, IpcMain, SaveDialogOptions, Session } from "electron";

const ARTIFACT_FILES_ROUTE = "/api/cloud/v1/artifact-files/";

export type CloudArtifactIpcDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	dialog: Pick<Dialog, "showSaveDialog">;
	downloadsDirectory: () => string;
};

function artifactUrl(url: string): URL {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		throw new Error("Invalid artifact URL");
	}
	if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.pathname.includes(ARTIFACT_FILES_ROUTE)) {
		throw new Error("Not a cloud artifact URL");
	}
	return parsed;
}

/** A file name safe to offer in a save dialog; path separators never survive. */
export function cloudArtifactFileName(name: string): string {
	const base = path.basename(name.replace(/[\\/]/g, "/")).replace(/[\u0000-\u001f<>:"|?*]/g, "").trim();
	return base && base !== "." && base !== ".." ? base : "artifact";
}

/**
 * Stream a cloud artifact file through `session` into `filePath`. The bytes go
 * to a sibling `.part` file first and replace `filePath` only once complete, so
 * a failed or cut-off download never leaves a truncated file under the name the
 * user chose.
 */
export async function saveCloudArtifact(session: Pick<Session, "fetch">, url: string, filePath: string): Promise<void> {
	const parsed = artifactUrl(url);
	const response = await session.fetch(parsed.toString());
	if (!response.ok) throw new Error(`Artifact request failed (${response.status})`);
	if (!response.body) throw new Error("Artifact response has no body");
	const partial = `${filePath}.part`;
	try {
		await pipeline(Readable.fromWeb(response.body as WebReadableStream<Uint8Array>), createWriteStream(partial));
		await rename(partial, filePath);
	} catch (error) {
		await rm(partial, { force: true });
		throw error;
	}
}

export function registerCloudArtifactIpc(deps: CloudArtifactIpcDeps): void {
	deps.ipcMain.handle("cloudArtifact:save", async (event, input: { url: string; name: string }) => {
		// Check the link before asking where to save, so a bad link never opens a dialog.
		const url = artifactUrl(String(input.url)).toString();
		const options: SaveDialogOptions = {
			defaultPath: path.join(deps.downloadsDirectory(), cloudArtifactFileName(String(input.name))),
		};
		const result = await deps.dialog.showSaveDialog(options);
		if (result.canceled || !result.filePath) return false;
		await saveCloudArtifact(event.sender.session, url, result.filePath);
		return true;
	});
}
