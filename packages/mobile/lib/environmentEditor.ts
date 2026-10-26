import type { ServerConfig } from "./config";
import { isValidPort } from "./connectionFields";
import type { Host } from "./hosts";
import { machineAppearance, type MachineAppearance } from "./machineAppearance";

/** What the machine editor saves: the name and icon, and one route's address and credential. */
export type EnvironmentMachineDraft = {
	machineName: string;
	appearance: MachineAppearance;
	config: ServerConfig;
	endpointIndex: number;
};

/** The route being edited: the one the phone is connected over, else the first. */
export function editableEndpointIndex(host: Host, activeConfig?: ServerConfig | null): number {
	const activeIndex = activeConfig?.hostId === host.id
		? host.endpoints.findIndex((endpoint) => endpoint.host === activeConfig.host && endpoint.port === Number(activeConfig.httpPort) && endpoint.secure === Boolean(activeConfig.secure))
		: -1;
	return activeIndex < 0 ? 0 : activeIndex;
}

export function editConfig(host: Host, endpointIndex: number): ServerConfig {
	const endpoint = host.endpoints[endpointIndex];
	return endpoint ? {
		host: endpoint.host,
		httpPort: String(endpoint.port),
		muxPort: "14801",
		secure: endpoint.secure,
		password: host.token,
		hostId: host.id,
		endpointKind: endpoint.kind,
	} : {
		host: "",
		httpPort: "3011",
		muxPort: "14801",
		secure: false,
		password: host.token,
		hostId: host.id,
	};
}

/** The editor's starting point: the machine as saved. */
export function machineEditDraft(host: Host, activeConfig?: ServerConfig | null): EnvironmentMachineDraft {
	const endpointIndex = editableEndpointIndex(host, activeConfig);
	return { machineName: host.name, appearance: machineAppearance(host), config: editConfig(host, endpointIndex), endpointIndex };
}

/** Whether the draft differs from what it started from, so leaving would lose something. */
export function isDraftDirty(initial: EnvironmentMachineDraft, draft: EnvironmentMachineDraft): boolean {
	return draft.machineName !== initial.machineName ||
		draft.appearance.symbol !== initial.appearance.symbol ||
		draft.config.host !== initial.config.host ||
		draft.config.httpPort !== initial.config.httpPort ||
		Boolean(draft.config.secure) !== Boolean(initial.config.secure) ||
		draft.config.password !== initial.config.password;
}

export function canSaveDraft(draft: EnvironmentMachineDraft): boolean {
	return Boolean(draft.machineName.trim() && draft.config.host.trim() && isValidPort(draft.config.httpPort));
}

/** A save the machine refused, said against the field it is about. */
export class MachineSaveError extends Error {
	constructor(message: string, readonly field: "host" | "password") {
		super(message);
		this.name = "MachineSaveError";
	}
}
