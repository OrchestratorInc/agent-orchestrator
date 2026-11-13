import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { useHostConnection } from "./useHostConnection";

/** Resolves false when the user cancels the save dialog. */
export async function downloadWorkspaceFile(baseUrl: string, sessionId: string, path: string, onSaving?: () => void): Promise<boolean> {
	const route = `/api/v1/sessions/${encodeURIComponent(sessionId)}/preview/files/${path.split("/").map(encodeURIComponent).join("/")}?raw=true`;
	const response = await fetch(`${baseUrl.replace(/\/+$/, "")}${route}`);
	if (!response.ok) throw new Error(`Download failed (${response.status})`);
	const id = await aoBridge.fileDownload.start(path.slice(path.lastIndexOf("/") + 1));
	if (!id) {
		await response.body?.cancel().catch(() => undefined);
		return false;
	}
	onSaving?.();
	const reader = response.body?.getReader();
	let finished = false;
	try {
		while (reader) {
			const { done, value } = await reader.read();
			if (done) break;
			await aoBridge.fileDownload.append(id, value);
		}
		await aoBridge.fileDownload.finish(id);
		finished = true;
		return true;
	} finally {
		if (!finished) {
			await reader?.cancel().catch(() => undefined);
			await aoBridge.fileDownload.cancel(id).catch(() => undefined);
		}
	}
}

/** Undefined unless the session runs on a connected remote host. */
export function useWorkspaceFileDownload(sessionId: string, hostId?: string): ((path: string) => void) | undefined {
	const { t } = useTranslation();
	const { isRemote, baseUrl } = useHostConnection(hostId);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const download = useCallback((path: string) => {
		if (!baseUrl) return;
		const name = path.slice(path.lastIndexOf("/") + 1);
		downloadWorkspaceFile(baseUrl, sessionId, path, () => showGlobalToast(t("files.downloading", { name }), undefined, "info"))
			.then((saved) => {
				if (saved) showGlobalToast(t("files.downloaded", { name }), undefined, "success");
			})
			.catch((error: unknown) => {
				showGlobalToast(t("files.downloadFailed"), error instanceof Error ? error.message : undefined, "error");
			});
	}, [baseUrl, sessionId, showGlobalToast, t]);
	return isRemote && baseUrl ? download : undefined;
}
