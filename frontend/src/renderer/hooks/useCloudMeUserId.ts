import { useQuery } from "@tanstack/react-query";
import { cloudMeQueryKey } from "../lib/cloud-offering";
import type { CloudCpMeResponse } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";

/**
 * The signed-in person's control-plane user ID, as GET /me reports it (the
 * desktop's own account ID is the identity provider's, not this one). It
 * shares useCloudOffering's cached /me. Undefined until known.
 */
export function useCloudMeUserId(enabled = true): string | undefined {
	const { client, ready, baseUrl, userId } = useCloudCp(enabled);
	const me = useQuery<CloudCpMeResponse>({
		queryKey: [...cloudMeQueryKey, baseUrl, userId],
		enabled: ready,
		staleTime: 30_000,
		refetchOnWindowFocus: true,
		retry: 1,
		queryFn: ({ signal }) => client.me({ signal }),
	});
	return me.data?.user.id;
}
