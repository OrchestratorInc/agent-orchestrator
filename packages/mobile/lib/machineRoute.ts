/**
 * The address segment of a machine's page in Settings.
 *
 * A machine carried over from the single-server config, or connected by hand
 * when its identity probe failed, has no id until it first connects (see
 * adoptHostIdentity), and an offline one keeps it empty. An empty segment
 * resolves to /settings/machine/, which has no page, so that machine opened
 * expo-router's "Unmatched Route" instead. It is addressed by a stand-in that
 * no daemon issues: their ids are "h_" + a UUID.
 */
export const UNIDENTIFIED_MACHINE_ROUTE_ID = "unidentified";

export function machineRouteId(hostId: string): string {
	return hostId || UNIDENTIFIED_MACHINE_ROUTE_ID;
}

/** The saved machine id a page's segment names; null when it names none. */
export function hostIdFromMachineRoute(segment: string | undefined): string | null {
	if (!segment) return null;
	return segment === UNIDENTIFIED_MACHINE_ROUTE_ID ? "" : segment;
}
