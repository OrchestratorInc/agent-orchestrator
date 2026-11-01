/**
 * Saves a cloud session's artifact file to disk.
 *
 * A cloud artifact link is cross-origin to the renderer, and the cloud API
 * bridge carries text bodies only, so the main process reads the file through
 * the sender's own session. It streams the response straight into the file the
 * user picked, so an artifact of any size saves without being held in memory or
 * copied across processes. Only the control plane's artifact file route is read.
 */

import { randomUUID } from "node:crypto";
import { createWriteStream, rmSync } from "node:fs";
import { rename, rm } from "node:fs/promises";
import path from "node:path";
import { Readable, Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import type { ReadableStream as WebReadableStream } from "node:stream/web";
import type { Dialog, IpcMain, SaveDialogOptions, Session } from "electron";

const ARTIFACT_FILES_ROUTE = "/api/cloud/v1/artifact-files/";
/** A download that receives no bytes for this long fails instead of hanging. */
export const CLOUD_ARTIFACT_IDLE_TIMEOUT_MS = 60_000;

export type CloudArtifactIpcDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	dialog: Pick<Dialog, "showSaveDialog">;
	downloadsDirectory: () => string;
	idleTimeoutMs?: number;
};

/** Saves in progress, by the id the renderer gave them, so they can be cancelled. */
const inFlight = new Map<string, { controller: AbortController; partial?: string }>();

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

/** Request a cloud artifact file through `session` and fail on anything but a readable 2xx. */
export async function requestCloudArtifact(session: Pick<Session, "fetch">, url: string, signal?: AbortSignal): Promise<Response> {
	const parsed = artifactUrl(url);
	const response = await session.fetch(parsed.toString(), signal ? { signal } : undefined);
	if (!response.ok) throw new Error(`Artifact request failed (${response.status})`);
	if (!response.body) throw new Error("Artifact response has no body");
	return response;
}

/** Fails the stream when no chunk arrives for `ms`, so a stalled connection ends instead of hanging. */
function idleWatchdog(ms: number): Transform {
	let timer: NodeJS.Timeout;
	const watchdog = new Transform({
		transform(chunk, _encoding, callback) {
			timer.refresh();
			callback(null, chunk);
		},
		flush(callback) {
			clearTimeout(timer);
			callback();
		},
		destroy(error, callback) {
			clearTimeout(timer);
			callback(error);
		},
	});
	timer = setTimeout(() => watchdog.destroy(new Error("The artifact download stalled")), ms);
	return watchdog;
}

/**
 * Stream an artifact response into `filePath`. The bytes go to a uniquely named
 * sibling `.part` file, created only if absent, and replace `filePath` only once
 * complete. Concurrent saves never share a temporary file, an unrelated file is
 * never overwritten, and a failed, stalled or cancelled download never leaves a
 * truncated file under the name the user chose.
 */
export async function writeCloudArtifact(
	response: Response,
	filePath: string,
	options: { signal?: AbortSignal; idleTimeoutMs?: number; onPartial?: (partial: string) => void } = {},
): Promise<void> {
	if (!response.body) throw new Error("Artifact response has no body");
	const partial = `${filePath}.${randomUUID()}.part`;
	options.onPartial?.(partial);
	try {
		await pipeline(
			Readable.fromWeb(response.body as WebReadableStream<Uint8Array>),
			idleWatchdog(options.idleTimeoutMs ?? CLOUD_ARTIFACT_IDLE_TIMEOUT_MS),
			createWriteStream(partial, { flags: "wx" }),
			options.signal ? { signal: options.signal } : {},
		);
		await rename(partial, filePath);
	} catch (error) {
		await rm(partial, { force: true });
		throw error;
	}
}

/** Fetch an artifact file and stream it into `filePath`. */
export async function saveCloudArtifact(session: Pick<Session, "fetch">, url: string, filePath: string, idleTimeoutMs?: number): Promise<void> {
	await writeCloudArtifact(await requestCloudArtifact(session, url), filePath, { idleTimeoutMs });
}

/**
 * Stop every save in progress and remove its partial file. Called when the app
 * quits, which would otherwise leave large `.part` files behind; synchronous so
 * it finishes before the process exits.
 */
export function abortCloudArtifactSaves(): void {
	for (const save of inFlight.values()) {
		save.controller.abort();
		if (save.partial) rmSync(save.partial, { force: true });
	}
	inFlight.clear();
}

export function registerCloudArtifactIpc(deps: CloudArtifactIpcDeps): void {
	// Resolves true when saved, false when the user cancelled at the dialog or
	// during the download.
	deps.ipcMain.handle("cloudArtifact:save", async (event, input: { url: string; name: string; id?: string }) => {
		const id = String(input.id ?? randomUUID());
		const controller = new AbortController();
		const save: { controller: AbortController; partial?: string } = { controller };
		inFlight.set(id, save);
		try {
			// The request comes first, so an expired link or a missing file fails
			// before the user picks a location. Its body is not read while the
			// dialog is open, so memory stays flat meanwhile.
			const response = await requestCloudArtifact(event.sender.session, String(input.url), controller.signal);
			const options: SaveDialogOptions = {
				defaultPath: path.join(deps.downloadsDirectory(), cloudArtifactFileName(String(input.name))),
			};
			const result = await deps.dialog.showSaveDialog(options);
			if (result.canceled || !result.filePath) {
				await response.body?.cancel().catch(() => undefined);
				return false;
			}
			await writeCloudArtifact(response, result.filePath, {
				signal: controller.signal,
				idleTimeoutMs: deps.idleTimeoutMs,
				onPartial: (partial) => {
					save.partial = partial;
				},
			});
			return true;
		} catch (error) {
			if (controller.signal.aborted) return false;
			throw error;
		} finally {
			inFlight.delete(id);
		}
	});

	deps.ipcMain.handle("cloudArtifact:cancelSave", (_event, id: string) => {
		inFlight.get(String(id))?.controller.abort();
	});
}
