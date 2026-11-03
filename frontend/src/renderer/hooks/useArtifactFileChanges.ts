import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { subscribeArtifactFileChanges } from "../lib/artifact-file-events";

/**
 * While enabled, rereads a local session's open artifact as soon as its file
 * changes on disk, so an agent's edits show without reopening it. A cloud
 * file needs none of this: its URL changes with the file.
 */
export function useArtifactFileChanges(sessionId: string, enabled: boolean) {
	const queryClient = useQueryClient();
	useEffect(() => {
		if (!enabled || !sessionId) return;
		return subscribeArtifactFileChanges(sessionId, queryClient);
	}, [enabled, queryClient, sessionId]);
}
