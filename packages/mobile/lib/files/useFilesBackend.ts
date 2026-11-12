import { useMemo } from "react";
import { useCloud } from "../cloud/CloudProvider";
import { isCloudHost } from "../cloud/mapping";
import { useApp } from "../store";
import { daemonFilesBackend, type FilesBackend } from "./backend";
import { cloudFilesBackend } from "./cloudBackend";

/**
 * The Files backend for the host this screen is scoped to: the paired
 * machine's daemon, or Orchestrator Cloud for a cloud session. Null until it
 * can be reached; `signedOut` when it is Cloud and nobody is signed in, and
 * `unreachable` when Cloud failed to load, so the screen can say so and retry
 * rather than wait on a backend that is not coming.
 */
export function useFilesBackend(): { backend: FilesBackend | null; cloud: boolean; signedOut: boolean; unreachable?: { message: string; retry(): void } } {
	const { config, currentHostId } = useApp();
	const { client, org, status, error, refresh } = useCloud();
	const cloud = isCloudHost(currentHostId);
	const orgId = org?.id;
	// Apart, so a machine's backend never changes with the Cloud account's state.
	const daemon = useMemo(() => !cloud && config ? daemonFilesBackend(config) : null, [cloud, config]);
	const remote = useMemo(() => cloud && orgId ? cloudFilesBackend(client, orgId) : null, [client, cloud, orgId]);
	const unreachable = cloud && status === "error" && !remote
		? { message: error ?? "Couldn't reach Orchestrator Cloud. Try again in a moment.", retry: () => { void refresh(); } }
		: undefined;
	return { backend: cloud ? remote : daemon, cloud, signedOut: cloud && status === "signed-out", ...(unreachable ? { unreachable } : {}) };
}
