import { useQuery, type QueryClient } from "@tanstack/react-query";
import type { CloudCpInterfaceMode } from "./cloud-cp";

export interface CloudOpening {
	clientMessageId: string;
	text: string;
	createdAt: string;
	interfaceMode?: CloudCpInterfaceMode;
}

const openingKey = (orgId: string, sessionId: string) => ["cloud-opening", orgId, sessionId] as const;

export function seedCloudOpening(queryClient: QueryClient, orgId: string, sessionId: string, opening: CloudOpening): void {
	queryClient.setQueryDefaults(["cloud-opening"], { gcTime: 30 * 60_000, staleTime: Infinity });
	queryClient.setQueryData(openingKey(orgId, sessionId), opening);
}

export function forgetCloudOpening(queryClient: QueryClient, orgId: string, sessionId: string): void {
	queryClient.removeQueries({ queryKey: openingKey(orgId, sessionId), exact: true });
}

export function useCloudOpening(orgId: string | undefined, sessionId: string): CloudOpening | undefined {
	const query = useQuery<CloudOpening | null>({
		queryKey: openingKey(orgId ?? "", sessionId),
		queryFn: () => null,
		enabled: false,
		staleTime: Infinity,
		gcTime: 30 * 60_000,
	});
	return query.data ?? undefined;
}
