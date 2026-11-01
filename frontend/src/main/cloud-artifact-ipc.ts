/**
 * Reads a cloud session's artifact file for saving.
 *
 * A cloud artifact link is cross-origin to the renderer, and the cloud API
 * bridge carries text bodies only, so the main process reads the file's bytes
 * through the sender's own session. Only the control plane's artifact file
 * route is read.
 */

import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
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

/** One file of a cloud session's artifacts: its path in the session and its artifact file URL. */
export interface CloudArtifactFolderFile {
	path: string;
	url: string;
}

const SESSION_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Where a cloud session's artifacts are copied to open them in a folder: under ~/.ao, never an OS app-data location. */
export function cloudArtifactsFolder(sessionId: string, env: NodeJS.ProcessEnv = process.env): string {
	return path.join(env.AO_DATA_DIR?.trim() || path.join(os.homedir(), ".ao"), "cloud-artifacts", sessionId);
}

/**
 * Copies a cloud session's artifact files into its folder under ~/.ao,
 * replacing what an earlier copy left, and returns the folder (or, with
 * `reveal`, the copied file named by it). A cloud session's artifacts live in
 * the control plane, so this is the folder a local session's "Show in"
 * actions open. A path that would leave the folder is refused.
 */
export async function copyCloudArtifactsToFolder(
	session: Pick<Session, "fetch">,
	input: { sessionId: string; files: CloudArtifactFolderFile[]; reveal?: string },
	env: NodeJS.ProcessEnv = process.env,
): Promise<string> {
	if (!SESSION_ID.test(String(input.sessionId))) throw new Error("Invalid session");
	const root = cloudArtifactsFolder(input.sessionId, env);
	const targets = input.files.map((file) => {
		const target = path.resolve(root, String(file.path));
		if (!target.startsWith(root + path.sep)) throw new Error("Invalid artifact path");
		return { target, url: String(file.url) };
	});
	const downloads = await Promise.all(targets.map(async (file) => ({ ...file, bytes: await fetchCloudArtifact(session, file.url) })));
	await fs.rm(root, { recursive: true, force: true });
	for (const file of downloads) {
		await fs.mkdir(path.dirname(file.target), { recursive: true });
		await fs.writeFile(file.target, file.bytes);
	}
	await fs.mkdir(root, { recursive: true });
	if (input.reveal === undefined) return root;
	const revealed = path.resolve(root, input.reveal);
	return revealed.startsWith(root + path.sep) ? revealed : root;
}

export function registerCloudArtifactIpc(deps: {
	ipcMain: Pick<IpcMain, "handle">;
	/** Opens a folder, or shows a file in its folder, in the file manager. */
	openInFileManager?: (target: string, isFile: boolean) => Promise<void> | void;
}): void {
	deps.ipcMain.handle("cloudArtifact:fetch", (event, url: string) => fetchCloudArtifact(event.sender.session, String(url)));
	deps.ipcMain.handle("cloudArtifact:openFolder", async (event, input: { sessionId: string; files: CloudArtifactFolderFile[]; reveal?: string }) => {
		const target = await copyCloudArtifactsToFolder(event.sender.session, input);
		await deps.openInFileManager?.(target, input.reveal !== undefined && target !== cloudArtifactsFolder(input.sessionId));
	});
}
