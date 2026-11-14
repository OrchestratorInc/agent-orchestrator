import { useMemo } from "react";
import { daemonArtifactsBackend, type ArtifactsBackend } from "./artifactsBackend";
import { cloudArtifactsBackend, useCloudArtifactAccess } from "./cloud/artifacts";
import { useCloud } from "./cloud/CloudProvider";
import { isCloudHost } from "./cloud/mapping";
import { useApp } from "./store";

/**
 * The artifacts backend for one session on the host this screen is scoped to:
 * the paired machine's daemon, or Orchestrator Cloud. Null until it can be
 * reached; `unavailable` says why, for Cloud, when waiting will not help.
 */
export function useArtifactsBackend(sessionId: string): { backend: ArtifactsBackend | null; cloud: boolean; unavailable?: string } {
	const { config, currentHostId } = useApp();
	const { client, org, me, status, sessions, error: cloudError } = useCloud();
	const cloud = isCloudHost(currentHostId);
	const orgId = org?.id;
	const offered = Boolean((org?.offering ?? me?.offering)?.artifacts);
	const access = useCloudArtifactAccess(client, orgId, sessionId, cloud && offered && status === "ready");
	// The control plane lets only a session's creator delete or restore its files.
	const creator = sessions.find((session) => session.id === sessionId)?.createdByUserId;
	const canChange = !creator || creator === me?.user.id;
	const daemon = useMemo(() => !cloud && config ? daemonArtifactsBackend(config) : null, [cloud, config]);
	const { root, renew } = access;
	const remote = useMemo(
		() => cloud && orgId && root ? cloudArtifactsBackend({ client, orgId, sessionId, root, canChange, renew }) : null,
		[canChange, client, cloud, orgId, renew, root, sessionId],
	);
	const unavailable = !cloud ? undefined
		: status === "signed-out" ? "Sign in to Orchestrator Cloud in Settings to see these files."
			// Cloud couldn't load: waiting on the backend would spin with nothing to say.
			: status === "error" ? cloudError ?? "Couldn't reach Orchestrator Cloud. Try again in a moment."
				: status === "ready" && !offered ? "Artifacts aren't turned on for this organization."
					: access.error;
	return { backend: cloud ? remote : daemon, cloud, unavailable };
}
