import { useQuery } from "@tanstack/react-query";
import { countPublished, type PublishedCheck } from "../lib/unpublish-artifacts";

/**
 * How many links a delete would stop, for its confirm: the known local ones
 * plus the cloud files found published once looked up. Counts only the known
 * ones until the lookup answers, or while `enabled` is false.
 */
export function usePublishedLinkCount(baseUrl: string, check: PublishedCheck, enabled: boolean): number {
	const lookup = useQuery({
		queryKey: ["published-link-count", baseUrl, check.lookup],
		queryFn: () => countPublished(baseUrl, check.lookup),
		enabled: enabled && baseUrl !== "" && check.lookup.length > 0,
		staleTime: 0,
	});
	return check.known + (enabled ? lookup.data ?? 0 : 0);
}
