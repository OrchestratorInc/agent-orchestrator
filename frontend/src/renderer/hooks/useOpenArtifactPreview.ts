import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { useUiStore } from "../stores/ui-store";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

/**
 * Opens an already-resolved artifact preview URL in the AO Browser panel.
 * Unlike useSessionBrowserLink, this does not gate on session liveness:
 * artifact files are static content the daemon serves from the session's
 * artifact directory the same way whether the session is running or
 * terminated, so a completed session's HTML output must stay openable.
 */
export function useOpenArtifactPreview(sessionId: string | undefined, hostId?: string) {
	const queryClient = useQueryClient();
	const setInspectorView = useUiStore((state) => state.setInspectorView);
	const setInspectorOpen = useUiStore((state) => state.setInspectorOpen);
	return useCallback(
		(url: string) => {
			if (!sessionId) return;
			const uiKey = sessionUiKey(sessionId, hostId);
			setInspectorView(uiKey, "browser");
			setInspectorOpen(uiKey, true);
			void (async () => {
				try {
					const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/preview", {
						params: { path: { sessionId } },
						body: { url },
					});
					if (error) {
						console.warn("Unable to open artifact preview in Browser tab", error);
						return;
					}
					await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				} catch (error) {
					console.warn("Unable to open artifact preview in Browser tab", error);
				}
			})();
		},
		[hostId, queryClient, sessionId, setInspectorOpen, setInspectorView],
	);
}
