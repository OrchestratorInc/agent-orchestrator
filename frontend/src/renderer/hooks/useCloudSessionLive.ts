import type { CloudCpSessionLive } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";
import { useCloudSessionLiveState } from "./useCloudSessionEvents";

export function useCloudSessionLive(orgId: string | undefined, sessionId: string | undefined, enabled: boolean): CloudCpSessionLive | undefined {
	const { baseUrl, ready } = useCloudCp(enabled);
	return useCloudSessionLiveState({ baseUrl, orgId, sessionId, enabled: enabled && ready });
}
