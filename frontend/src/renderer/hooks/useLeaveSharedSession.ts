import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";
import { sharedCloudSessionsQueryKey } from "./useWorkspaceQuery";

/**
 * Removes a session someone shared with you from your own list. This revokes
 * only your grant: the owner's session keeps running and other collaborators
 * keep their access. Getting it back needs a fresh share link.
 */
export function useLeaveSharedSession({ onLeft }: { onLeft?: (session: WorkspaceSession) => void } = {}) {
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (session: WorkspaceSession) => {
			const grantId = session.cloud?.shareGrantId;
			if (!grantId) throw new Error("This session was not shared with you.");
			await client.leaveSharedSession(grantId);
		},
		onSuccess: async (_result, session) => {
			onLeft?.(session);
			await queryClient.invalidateQueries({ queryKey: sharedCloudSessionsQueryKey });
		},
	});
}
