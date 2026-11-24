import { DEFAULT_CONFIG, hostMachineIdentity, machineIdentity } from "../config";
import type { HostMetadata } from "../hosts";

// Which machines' chats may be saved on the phone (see savedConversations).
// Forgetting a machine deletes its saved chats, but a read of one of its chats
// can still be under way then, and answer after: the chat screen keeps its
// connection until the app catches up with the forget. Its page must not be
// saved again. So a forgotten machine stays forgotten here until it is paired
// again (saveHost), and the saver checks this before it saves a page and again
// before it writes one. Kept in memory: after a restart no read of a forgotten
// machine is left to answer. Kept apart from savedConversations, which reaches
// native modules, so hosts.ts can open a machine again without loading them.

const forgotten = new Set<string>();
/** Every machine was forgotten (no machine was named): only those paired since may be saved. */
let everyMachineForgotten = false;
const pairedSince = new Set<string>();

const anyOf = (machines: ReadonlySet<string>, key: string) => [...machines].some((machine) => key.startsWith(`${machine}/`));

/** Every key a machine's chats may be saved under (see conversationPageCacheKey). */
export function savedConversationMachines(host: HostMetadata): string[] {
	const machines = new Set<string>();
	if (host.id) machines.add(hostMachineIdentity(host.id));
	for (const endpoint of host.endpoints) {
		machines.add(machineIdentity({ ...DEFAULT_CONFIG, host: endpoint.host, httpPort: String(endpoint.port), secure: endpoint.secure }));
	}
	return [...machines];
}

/** No chat of `machines` (every machine for null) is saved until it is paired again. */
export function forgetSavedChatMachines(machines: readonly string[] | null): void {
	if (machines === null) {
		everyMachineForgotten = true;
		forgotten.clear();
		pairedSince.clear();
		return;
	}
	for (const machine of machines) {
		forgotten.add(machine);
		pairedSince.delete(machine);
	}
}

/** A machine was paired (again): its chats may be saved. */
export function pairSavedChatMachines(machines: readonly string[]): void {
	for (const machine of machines) {
		forgotten.delete(machine);
		if (everyMachineForgotten) pairedSince.add(machine);
	}
}

/** Whether the chat saved under `key` belongs to a machine forgotten since it was last paired. */
export function savedChatForgotten(key: string): boolean {
	return anyOf(forgotten, key) || (everyMachineForgotten && !anyOf(pairedSince, key));
}

/** Every machine's chats may be saved again; for tests, which start from nothing forgotten. */
export function resetSavedChatMachines(): void {
	forgotten.clear();
	pairedSince.clear();
	everyMachineForgotten = false;
}
