import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { CloudCpBrowserProfileStatus, CloudCpBrowserProfileWiped } from "../lib/cloud-cp/types";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudCp } from "./useCloudCp";
import { useCloudOffering } from "./useCloudOffering";

export const cloudBrowserProfileQueryKey = ["cloud", "browser-profile"] as const;

/**
 * The signed-in person's saved Cloud browser sign-ins in the active org, and
 * a wipe. `available` is false while signed out and on a control plane
 * without browser profiles (`/me` browserProfiles): there is nothing to show.
 */
export function useCloudBrowserProfile() {
	const { status } = useCloudSession();
	const { client, ready } = useCloudCp();
	const offering = useCloudOffering();
	const orgId = offering.org?.id ?? "";
	const available = ready && status === "authenticated" && orgId !== "" && offering.browserProfiles;
	const queryClient = useQueryClient();
	const key = [...cloudBrowserProfileQueryKey, orgId];
	const profile = useQuery<CloudCpBrowserProfileStatus>({
		queryKey: key,
		enabled: available,
		queryFn: ({ signal }) => client.getMyBrowserProfile(orgId, { signal }),
		staleTime: 30_000,
	});
	const wipe = useMutation<CloudCpBrowserProfileWiped>({
		mutationFn: () => client.wipeMyBrowserProfile(orgId),
		onSuccess: () => queryClient.invalidateQueries({ queryKey: key }),
	});
	return { available, profile, wipe };
}
