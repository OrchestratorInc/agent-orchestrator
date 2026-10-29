/**
 * Reads a cloud session's artifact file for saving.
 *
 * A cloud artifact link is cross-origin to the renderer, and the cloud API
 * bridge carries text bodies only, so the main process reads the file's bytes
 * through the sender's own session. Only the control plane's artifact file
 * route is read.
 */

import type { IpcMain, Session } from "electron";

/** The control plane refuses artifact files over 100 MiB. */
export const MAX_CLOUD_ARTIFACT_BYTES = 100 * 1024 * 1024;

const ARTIFACT_FILES_ROUTE = "/api/cloud/v1/artifact-files/";

/** Fetch a cloud artifact file's bytes through `session`. */
export async function fetchCloudArtifact(session: Pick<Session, "fetch">, url: string): Promise<Uint8Array> {
	let parsed: URL;
	try {
		parsed = new URL(url);
	} catch {
		throw new Error("Invalid artifact URL");
	}
	if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.pathname.includes(ARTIFACT_FILES_ROUTE)) {
		throw new Error("Not a cloud artifact URL");
	}
	const response = await session.fetch(parsed.toString());
	if (!response.ok) throw new Error(`Artifact request failed (${response.status})`);
	const declared = Number(response.headers.get("content-length"));
	if (Number.isFinite(declared) && declared > MAX_CLOUD_ARTIFACT_BYTES) throw new Error("Artifact is too large");
	const bytes = new Uint8Array(await response.arrayBuffer());
	if (bytes.byteLength > MAX_CLOUD_ARTIFACT_BYTES) throw new Error("Artifact is too large");
	return bytes;
}

export function registerCloudArtifactIpc(deps: { ipcMain: Pick<IpcMain, "handle"> }): void {
	deps.ipcMain.handle("cloudArtifact:fetch", (event, url: string) => fetchCloudArtifact(event.sender.session, String(url)));
}
