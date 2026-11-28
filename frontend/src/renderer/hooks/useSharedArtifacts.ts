import { useQuery } from "@tanstack/react-query";
import { CloudCpError, type CloudCpSharedArtifact } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

export const sharedArtifactsQueryKey = ["shared-artifacts"] as const;

/** How often the shared list is read again, the shared-sessions interval. */
export const SHARED_ARTIFACTS_REFRESH_MS = 15_000;

// What a control plane without share links answers (404: an older one, or the
// flag off): nothing shared, and nothing to read again until the app restarts.
const NOT_OFFERED: CloudCpSharedArtifact[] = [];

/**
 * The links other people shared with the signed-in person, by their email or
 * a team they are in. Read again every 15 seconds while the app is open, as
 * shared sessions are, so a newly shared link appears without opening it.
 * Empty while signed out of Cloud, or where the control plane has no share links.
 */
export function useSharedArtifacts(enabled = true) {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	return useQuery<CloudCpSharedArtifact[]>({
		// Per account, so one account's list never shows for another signed in after it.
		queryKey: [...sharedArtifactsQueryKey, baseUrl, userId],
		enabled: enabled && ready,
		staleTime: SHARED_ARTIFACTS_REFRESH_MS,
		refetchInterval: (query) => (query.state.data === NOT_OFFERED ? false : SHARED_ARTIFACTS_REFRESH_MS),
		retry: 1,
		queryFn: async ({ signal }) => {
			try {
				return (await client.listSharedArtifacts({ signal })).artifacts;
			} catch (error) {
				if (notOffered(error)) return NOT_OFFERED;
				throw error;
			}
		},
	});
}

function notOffered(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 404;
}
