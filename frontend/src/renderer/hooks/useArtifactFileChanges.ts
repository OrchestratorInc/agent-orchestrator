import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { subscribeArtifactFileChanges } from "../lib/artifact-file-events";
import type { WorkspaceSession } from "../types/workspace";
import { cloudArtifactSessionFilesQueryKey } from "./useCloudArtifacts";
import { useCloudCp } from "./useCloudCp";
import { useCloudSessionEventListener } from "./useCloudSessionEvents";

/** The control plane's event for a commit or delete of a session's artifacts. */
export const CLOUD_ARTIFACT_CHANGED_EVENT = "artifact.changed";
/** How long a burst of cloud artifact changes (a replayed history, several commits) is folded into one reread. */
const CLOUD_ARTIFACT_REFRESH_MS = 250;

/**
 * While enabled, rereads a session's open artifact as soon as its file
 * changes, so an agent's edits show without reopening it. A local session's
 * files are followed through the daemon's artifact stream. A cloud session's
 * ride its shared event stream: an artifact.changed event rereads the
 * session's file list, whose new updatedAt changes the open file's URL.
 */
export function useArtifactFileChanges(session: Pick<WorkspaceSession, "id" | "cloud">, enabled: boolean) {
	const queryClient = useQueryClient();
	const cloud = Boolean(session.cloud);
	useEffect(() => {
		if (!enabled || cloud || !session.id) return;
		return subscribeArtifactFileChanges(session.id, queryClient);
	}, [cloud, enabled, queryClient, session.id]);
	useCloudArtifactChanges(session.cloud?.orgId, session.id, enabled && cloud);
}

function useCloudArtifactChanges(orgId: string | undefined, sessionId: string, enabled: boolean) {
	const queryClient = useQueryClient();
	const { baseUrl } = useCloudCp(enabled);
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(timer.current), []);
	useCloudSessionEventListener({
		baseUrl,
		orgId,
		sessionId,
		enabled,
		onEvent: (event) => {
			if (event.type !== CLOUD_ARTIFACT_CHANGED_EVENT || !orgId || timer.current) return;
			timer.current = setTimeout(() => {
				timer.current = undefined;
				void queryClient.invalidateQueries({ queryKey: cloudArtifactSessionFilesQueryKey(baseUrl, orgId, sessionId) });
			}, CLOUD_ARTIFACT_REFRESH_MS);
		},
	});
}
