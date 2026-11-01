import type { ServerConfig } from "./config";
import type { EnvironmentStatus } from "./environmentView";
import type { Host } from "./hosts";
import type { ToastController } from "./Toast.types";

export type MachineDetailProps = {
	host: Host;
	activeConfig?: ServerConfig;
	isDefault: boolean;
	status: EnvironmentStatus;
	/** Making this machine the default is under way. */
	switching: boolean;
	/** Forgetting this machine is under way. */
	forgetting: boolean;
	/** Testing this machine's connection is under way. */
	testing: boolean;
	/** A test can run now: not with the phone offline. */
	canTest: boolean;
	/** Tests the connection and says what it found. */
	onTest: () => void;
	onMakeDefault: () => void;
	onReconnect: () => void;
	/** Asks before anything is forgotten. */
	onForget: () => void;
	onEdit: () => void;
	/** Says something briefly, such as a copied address, in the page's toast. */
	notify: ToastController["show"];
};

export const DEFAULT_MACHINE_NOTE = "New workers and reconnects use this machine.";
export const RECONNECT_NOTE = "Reconnect scans a new pairing code from this machine.";
export const FORGET_MACHINE_NOTE = "Removes this phone's saved connection. The machine's workers and files stay.";
