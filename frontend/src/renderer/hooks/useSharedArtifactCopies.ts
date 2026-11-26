import { useQuery } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";

export const sharedArtifactCopiesQueryKey = ["shared-artifact-copies"] as const;

/**
 * Whether a local session's artifact file is a copy of an artifact someone
 * else shared, which is never published as the person's own. Asked of the
 * desktop shell, which knows both its own record of copies and the daemon's
 * marker. True until it answers, and if it cannot: Share then stays hidden
 * rather than flashing on a copy (the shell refuses the publish regardless).
 */
export function useIsSharedArtifactCopy(sessionId: string | undefined, path: string | undefined): boolean {
	const query = useQuery({
		queryKey: [...sharedArtifactCopiesQueryKey, sessionId, path],
		enabled: Boolean(sessionId && path),
		staleTime: 60_000,
		queryFn: () => aoBridge.sharedArtifact.isCopy(sessionId ?? "", path ?? ""),
	});
	if (!sessionId || !path) return false;
	return query.data !== false;
}
