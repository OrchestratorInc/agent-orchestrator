import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { getApiBaseUrl } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { attachmentURL } from "../components/chat/messageAttachments";
import { isPotentialWorkspaceFileLink, isWebLink, workspaceFilePath } from "../lib/external-link-policy";
import { isAbsoluteFileReference, normalizeWorkspaceFileReference } from "../lib/workspace-file-path";
import { useUiStore } from "../stores/ui-store";
import { sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { resolveSessionWorkspacePath } from "./useSessionWorkspaceFiles";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

export function useSessionBrowserLink(
	session?: WorkspaceSession,
	openInBrowser?: (uri: string) => Promise<void>,
	workspacePaths: string[] = [],
	onUnownedFile?: (reference: string) => void,
): (uri: string) => void {
	const queryClient = useQueryClient();
	const setInspectorView = useUiStore((state) => state.setInspectorView);
	const setInspectorOpen = useUiStore((state) => state.setInspectorOpen);
	const active = session ? sessionIsActive(session) : false;
	const hostId = session?.hostId;
	const cloud = Boolean(session?.cloud);

	return useCallback(
		(uri: string) => {
			if (!session?.id || !active) return;
			const sessionId = session.id;
			const webLink = isWebLink(uri);
			if (cloud && !(webLink && openInBrowser)) return;
			const openPreview = (target: string, workspacePath: string | undefined) => {
				const uiKey = sessionUiKey(sessionId, hostId);
				setInspectorView(uiKey, "browser");
				setInspectorOpen(uiKey, true);
				if (!hostId && openInBrowser && webLink) {
					void openInBrowser(target).catch((error) => {
						console.warn("Unable to open link in Browser tab", error);
					});
					return;
				}
				if (!hostId && openInBrowser && workspacePath) {
					void openInBrowser(attachmentURL(getApiBaseUrl(), sessionId, workspacePath)).catch((error) => {
						console.warn("Unable to open workspace file in Browser tab", error);
					});
					return;
				}
				void (async () => {
					try {
						const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/preview", {
							params: { path: { sessionId } },
							body: webLink ? { url: target } : { url: target, requireWorkspaceFile: true },
						});
						if (error) {
							console.warn("Unable to open link in Browser preview", error);
							return;
						}
						await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
					} catch (error) {
						console.warn("Unable to open link in Browser preview", error);
					}
				})();
			};
			const reference = webLink || uri.trim().startsWith("//") ? "" : normalizeWorkspaceFileReference(uri);
			if (isAbsoluteFileReference(reference)) {
				void (async () => {
					const resolved = await resolveSessionWorkspacePath(sessionId, reference, hostId);
					if (resolved?.kind === "workspace") openPreview(resolved.path, resolved.path);
					else if (resolved?.kind) onUnownedFile?.(reference);
					else openPreview(uri, undefined);
				})();
				return;
			}
			const workspacePath = workspaceFilePath(uri, workspacePaths);
			if (!webLink && !workspacePath && !isPotentialWorkspaceFileLink(uri)) return;
			openPreview(uri, workspacePath);
		},
		[active, cloud, hostId, onUnownedFile, openInBrowser, queryClient, session?.id, session?.kind, setInspectorOpen, setInspectorView, workspacePaths],
	);
}
