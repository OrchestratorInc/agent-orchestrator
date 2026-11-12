import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { useHostConnection } from "./useHostConnection";
import { readBoundedResponseBytes } from "./useSessionWorkspaceFiles";

export const MAX_FILE_DOWNLOAD_BYTES = 64 * 1024 * 1024;

export class FileTooLargeError extends Error {}

/** Resolves false when the user cancels the save dialog. */
export async function downloadWorkspaceFile(baseUrl: string, sessionId: string, path: string): Promise<boolean> {
	const route = `/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${path.split("/").map(encodeURIComponent).join("/")}?raw=true`;
	const response = await fetch(`${baseUrl.replace(/\/+$/, "")}${route}`);
	if (!response.ok) throw new Error(`Download failed (${response.status})`);
	if (Number(response.headers.get("content-length")) > MAX_FILE_DOWNLOAD_BYTES) throw new FileTooLargeError();
	const { bytes, truncated } = await readBoundedResponseBytes(response, MAX_FILE_DOWNLOAD_BYTES);
	if (truncated) throw new FileTooLargeError();
	return aoBridge.chatImage.save({ name: path.slice(path.lastIndexOf("/") + 1), bytes });
}

/** Undefined unless the session runs on a connected remote host. */
export function useWorkspaceFileDownload(sessionId: string, hostId?: string): ((path: string) => void) | undefined {
	const { t } = useTranslation();
	const { isRemote, baseUrl } = useHostConnection(hostId);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const download = useCallback((path: string) => {
		if (!baseUrl) return;
		downloadWorkspaceFile(baseUrl, sessionId, path).catch((error: unknown) => {
			const detail = error instanceof FileTooLargeError ? t("files.downloadTooLarge") : error instanceof Error ? error.message : undefined;
			showGlobalToast(t("files.downloadFailed"), detail, "error");
		});
	}, [baseUrl, sessionId, showGlobalToast, t]);
	return isRemote && baseUrl ? download : undefined;
}
