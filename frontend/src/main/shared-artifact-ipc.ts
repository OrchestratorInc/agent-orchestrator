/**
 * IPC for starting a session from a link someone shared with the person:
 * copying the link's files, and remembering which session works from it.
 */

import type { IpcMain, IpcMainInvokeEvent } from "electron";
import { AoError } from "../../../packages/shared/telemetry/ao-error";
import { copySharedArtifact, SharedArtifactCopyError, type SharedArtifactCopy } from "./shared-artifact-copy";
import type { SharedArtifactSessionKey, SharedArtifactSessionRecord, SharedArtifactSessions } from "./shared-artifact-sessions";

/** A copy, or why there is none: an error thrown over IPC keeps only its message. */
export type SharedArtifactCopyResult =
	| { ok: true; copy: SharedArtifactCopy }
	| { ok: false; code: SharedArtifactCopyError["code"]; message: string };

/** What to copy; an update names the first copy's folder and entry to write into. */
export interface SharedArtifactCopyInput {
	contentUrl: string;
	name: string;
	into?: { dir: string; entry: string };
}

export async function copySharedArtifactResult(
	fetcher: Parameters<typeof copySharedArtifact>[0],
	input: SharedArtifactCopyInput,
	options: { allowLocal?: boolean } = {},
): Promise<SharedArtifactCopyResult> {
	try {
		return { ok: true, copy: await copySharedArtifact(fetcher, input, options) };
	} catch (error) {
		if (error instanceof SharedArtifactCopyError) return { ok: false, code: error.code, message: error.message };
		return { ok: false, code: "unavailable", message: error instanceof Error ? error.message : String(error) };
	}
}

/**
 * Refuses a call from anything but a window's main frame, where the bridge
 * lives: an embedded page's frame never reaches these files or downloads.
 */
function requireMainFrame(event: unknown): void {
	const { senderFrame } = event as Partial<IpcMainInvokeEvent>;
	if (senderFrame === null || (senderFrame && senderFrame.parent !== null)) throw new AoError("SHARED_ARTIFACT_NOT_APP_WINDOW", "Only the app window can do this.");
}

export function registerSharedArtifactIpc(deps: {
	ipcMain: Pick<IpcMain, "handle">;
	fetch: (url: string, init?: { redirect?: "follow" | "manual" }) => Promise<Response>;
	sessions: SharedArtifactSessions;
	/** A dev build's own control plane serves links over local http. */
	allowLocal?: boolean;
	/** Whether a session's artifact file is a copy (sharedCopyCheck). */
	isCopy: (sessionId: string, relativePath: string) => Promise<boolean>;
}): void {
	deps.ipcMain.handle("sharedArtifact:copy", (event, input: SharedArtifactCopyInput) => {
		requireMainFrame(event);
		const into = input?.into ? { dir: String(input.into.dir ?? ""), entry: String(input.into.entry ?? "") } : undefined;
		return copySharedArtifactResult(deps.fetch, { contentUrl: String(input?.contentUrl ?? ""), name: String(input?.name ?? ""), into }, { allowLocal: deps.allowLocal });
	});
	deps.ipcMain.handle("sharedArtifact:getSession", (event, key: SharedArtifactSessionKey) => (requireMainFrame(event), deps.sessions.get(key)));
	deps.ipcMain.handle("sharedArtifact:isCopy", (event, sessionId: unknown, relativePath: unknown) => {
		requireMainFrame(event);
		return typeof sessionId === "string" && typeof relativePath === "string" ? deps.isCopy(sessionId, relativePath) : false;
	});
	deps.ipcMain.handle("sharedArtifact:setSession", (event, key: SharedArtifactSessionKey, record: SharedArtifactSessionRecord) => (requireMainFrame(event), deps.sessions.set(key, record)));
	deps.ipcMain.handle("sharedArtifact:removeSession", (event, key: SharedArtifactSessionKey) => (requireMainFrame(event), deps.sessions.remove(key)));
}
