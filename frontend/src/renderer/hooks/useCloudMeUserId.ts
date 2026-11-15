import { useQuery } from "@tanstack/react-query";
import { cloudMeQueryKey } from "../lib/cloud-offering";
import type { CloudCpMeResponse } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";

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
