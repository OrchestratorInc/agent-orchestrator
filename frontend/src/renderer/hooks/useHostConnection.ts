import { useSyncExternalStore } from "react";
import { connectedHost, connectedHosts, subscribeConnectedHosts } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";

/** Keep the selected host separate from its optional live connection. */
export function useHostConnection(hostId?: HostId) {
	const connection = useSyncExternalStore(subscribeConnectedHosts, () => hostId ? connectedHost(hostId) : undefined);
	return {
		hostId,
		isRemote: Boolean(hostId && hostId !== LOCAL_HOST),
		baseUrl: connection?.base,
		label: connection?.label,
	};
}

export function useConnectedHosts(): HostId[] {
	return useSyncExternalStore(subscribeConnectedHosts, connectedHosts, connectedHosts);
}
