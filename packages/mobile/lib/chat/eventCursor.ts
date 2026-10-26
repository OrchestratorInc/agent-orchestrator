import AsyncStorage from "@react-native-async-storage/async-storage";
import { DEFAULT_CONFIG, machineIdentity, type ServerConfig } from "../config";
import type { HostMetadata } from "../hosts";

/**
 * Where the event stream starts, and where older builds stored its cursor.
 *
 * Replaying the change log is expensive: measured against a real daemon, a
 * cursor of zero delivers 14,338 events and 3 MB, which takes roughly twenty
 * seconds to parse on device. During that time a reply the agent produced in
 * two seconds sits at the end of the queue, unrendered. The stream therefore
 * always opens at head (see useConversationEventTransport).
 */

/**
 * Asks the daemon for the current head.
 *
 * The stream clamps any cursor beyond head back to head and reports the
 * resolved value in X-AO-Event-After, so this is the supported way to say
 * "only live events" without a separate round trip.
 */
export const HEAD_CURSOR = Number.MAX_SAFE_INTEGER;

/**
 * Storage key for a machine's event cursor, as older builds persisted it.
 * Still cleared when a machine is forgotten.
 *
 * Keyed on the machine's identity, not the address it happened to answer on —
 * see machineIdentity, which owns that rule. Keying by address gave one daemon
 * a cursor per path, so every network change started from zero and replayed the
 * entire backlog. The prefix keeps the key shape this shipped with.
 */
export function eventCursorKey(cfg: ServerConfig): string {
	return `ao.chat.events.${machineIdentity(cfg)}`;
}

/** Remove every replay key a machine may have used. Identified hosts converge
 * on one host key; migrated hosts use one address key per stored endpoint. */
export async function clearEventCursorsForHost(host: HostMetadata): Promise<void> {
	const keys = new Set<string>();
	if (host.id) {
		keys.add(eventCursorKey({ ...DEFAULT_CONFIG, hostId: host.id }));
	}
	for (const endpoint of host.endpoints) {
		keys.add(
			eventCursorKey({
				...DEFAULT_CONFIG,
				host: endpoint.host,
				httpPort: String(endpoint.port),
				secure: endpoint.secure,
				...(host.id ? { hostId: host.id } : {}),
			}),
		);
	}
	await Promise.all([...keys].map((key) => AsyncStorage.removeItem(key)));
}
