import { useQuery, type QueryClient } from "@tanstack/react-query";
import type { CloudCpInterfaceMode } from "./cloud-cp";

// The brief a new Cloud task was started with, kept from the moment its session
// was created until the session's own chat holds it. The session view shows it
// at once, with the start under it, instead of an empty chat while the control
// plane's first chat read is on its way.

export interface CloudOpening {
	clientMessageId: string;
	text: string;
	/** When the brief was sent: the session's start is timed from here. */
	createdAt: string;
	/** The interface the session was created in, known before the session list has it. */
	interfaceMode?: CloudCpInterfaceMode;
}

const openingKey = (orgId: string, sessionId: string) => ["cloud-opening", orgId, sessionId] as const;

/** Records a new Cloud session's brief for its view to show before the control plane's chat read lands. */
export function seedCloudOpening(queryClient: QueryClient, orgId: string, sessionId: string, opening: CloudOpening): void {
	// Kept while the view is closed: the page navigates to the session just after this.
	queryClient.setQueryDefaults(["cloud-opening"], { gcTime: 30 * 60_000, staleTime: Infinity });
	queryClient.setQueryData(openingKey(orgId, sessionId), opening);
}

export function forgetCloudOpening(queryClient: QueryClient, orgId: string, sessionId: string): void {
	queryClient.removeQueries({ queryKey: openingKey(orgId, sessionId), exact: true });
}

/** The brief a Cloud session was started with from this device, while it is still opening. */
export function useCloudOpening(orgId: string | undefined, sessionId: string): CloudOpening | undefined {
	const query = useQuery<CloudOpening | null>({
		queryKey: openingKey(orgId ?? "", sessionId),
		// Only ever seeded; nothing to fetch.
		queryFn: () => null,
		enabled: false,
		staleTime: Infinity,
		gcTime: 30 * 60_000,
	});
	return query.data ?? undefined;
}
