import { MENTIONS_FEATURE } from "../lib/cloud-offering";
import { useCloudOffering } from "./useCloudOffering";

/**
 * Whether @mentions and comment threads are on: the offering lists
 * MENTIONS_FEATURE (the organization's, else the active one's). False until
 * the offering loads and against a control plane that does not list it, so no
 * mentions or threads surface shows there; everything else is unchanged.
 */
export function useMentionsOffered(enabled = true, orgId?: string): boolean {
	return useCloudOffering(enabled, orgId).features?.includes(MENTIONS_FEATURE) === true;
}
