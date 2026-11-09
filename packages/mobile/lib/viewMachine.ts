import { hostMachineIdentity, isConfigured, machineIdentity, type ServerConfig } from "./config";

/**
 * Which machine a view is about, kept apart from whether it can be reached.
 *
 * `config` is the verified connection: it is dropped in the background and
 * while endpoints are raced, so a LAN address that now belongs to another
 * machine never gets the bearer. Deciding what to render, or keying display
 * state, on it tore the chat screen down on every app switch and rebuilt it
 * on return (the resume flicker). The selected machine does not change in
 * those moments, so this is what screens, caches and component keys use.
 *
 * Kept out of store.tsx, which imports React Native and so cannot be loaded
 * by vitest, so the derivation is testable on its own.
 */
export type ViewMachine = {
	/** `config` is set and names an endpoint. */
	configured: boolean;
	/** `machineIdentity` of the machine, or "" when none is selected. */
	machine: string;
	/** A machine is paired. Null only until the first resolution has said. */
	paired: boolean | null;
};

export function viewMachine(args: {
	config: ServerConfig | null;
	selectedHostId: string | null;
	configResolved: boolean;
}): ViewMachine {
	const configured = !!args.config && isConfigured(args.config);
	const machine = args.config && configured
		? machineIdentity(args.config)
		: args.selectedHostId ? hostMachineIdentity(args.selectedHostId) : "";
	const paired = configured || args.selectedHostId !== null ? true : args.configResolved ? false : null;
	return { configured, machine, paired };
}
