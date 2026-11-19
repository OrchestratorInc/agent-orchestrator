import type { CloudCpSessionLive } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { useCloudSessionLiveState } from "./useCloudSessionEvents";

/**
 * A cloud session's live state while it is on screen: who else has it open
 * and who holds its composer. Having it open lists this person as a viewer
 * for everyone else, once however many components ask: it rides the
 * session's one shared event stream (session-events-hub), which reconnects
 * with backoff. Undefined until the first frame and again after the stream
 * drops.
 */
export function useCloudSessionLive(orgId: string | undefined, sessionId: string | undefined, enabled: boolean): CloudCpSessionLive | undefined {
	const { baseUrl, ready } = useCloudCp(enabled);
	return useCloudSessionLiveState({ baseUrl, orgId, sessionId, enabled: enabled && ready });
}
