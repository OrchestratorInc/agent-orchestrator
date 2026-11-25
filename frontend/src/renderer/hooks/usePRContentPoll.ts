import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { sessionScmSummaryQueryKey } from "./useSessionScmSummary";

/** How often an open PR tab re-reads the stored PR. */
export const PR_CONTENT_POLL_MS = 30_000;

/**
 * Keeps an open PR tab's title and description current. A title or
 * description changed on the provider reaches the daemon on its next
 * observation but emits no change event, so the tab re-reads the stored PR on
 * a slow poll while the window is visible. It is a local read: the daemon's
 * own observation is what asks the provider.
 */
export function usePRContentPoll(sessionId: string, hostId: string | undefined, enabled: boolean) {
	const queryClient = useQueryClient();
	useEffect(() => {
		// A remote host's summary already refetches on its own interval.
		if (!enabled || hostId) return;
		const timer = window.setInterval(() => {
			if (document.visibilityState !== "visible") return;
			void queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(sessionId, hostId) });
		}, PR_CONTENT_POLL_MS);
		return () => window.clearInterval(timer);
	}, [enabled, hostId, queryClient, sessionId]);
}
