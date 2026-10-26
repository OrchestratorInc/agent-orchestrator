import type { ServerConfig } from "./config";
import type { EnvironmentStatus } from "./environmentView";
import type { Host } from "./hosts";

export type MachineDetailProps = {
	host: Host;
	activeConfig?: ServerConfig;
	isDefault: boolean;
	status: EnvironmentStatus;
	/** Making this machine the default is under way. */
	switching: boolean;
	/** Forgetting this machine is under way. */
	forgetting: boolean;
	onMakeDefault: () => void;
	onReconnect: () => void;
	/** Asks before anything is forgotten. */
	onForget: () => void;
	onEdit: () => void;
};

export const DEFAULT_MACHINE_NOTE = "New workers and reconnects use this machine.";
export const RECONNECT_NOTE = "Reconnect scans a new pairing code from this machine.";
export const FORGET_MACHINE_NOTE = "Removes this phone's saved connection. The machine's workers and files stay.";
