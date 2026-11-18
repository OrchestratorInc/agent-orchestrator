import { useQuery } from "@tanstack/react-query";
import type { CloudCpSharedArtifact } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

export const sharedArtifactsQueryKey = ["shared-artifacts"] as const;

/**
 * The links other people published that open for the signed-in person: ones
 * shared with their email, and public ones they opened in the app. Empty
 * while signed out of Cloud, or where the control plane has no share links.
 */
export function useSharedArtifacts(enabled = true) {
	const { client, ready, baseUrl } = useCloudCp(enabled);
	return useQuery<CloudCpSharedArtifact[]>({
		queryKey: [...sharedArtifactsQueryKey, baseUrl],
		enabled: enabled && ready,
		staleTime: 30_000,
		retry: 1,
		queryFn: async ({ signal }) => (await client.listSharedArtifacts({ signal })).artifacts,
	});
}
