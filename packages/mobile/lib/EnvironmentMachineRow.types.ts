import type { ServerConfig } from "./config";
import type { EnvironmentStatus } from "./environmentView";
import type { Host } from "./hosts";

/** One paired machine as the Environments list shows it. */
export type EnvironmentMachineSummary = {
	host: Host;
	activeConfig?: ServerConfig;
	isDefault: boolean;
	status: EnvironmentStatus;
};

export type EnvironmentMachineListProps = {
	machines: readonly EnvironmentMachineSummary[];
	/** Said under the list. */
	footer: string;
	/** Said under the list instead, in the error colour, when the machines couldn't be read. */
	error?: string;
	refreshing: boolean;
	onRefresh: () => Promise<void>;
	/** Opens the machine's page. */
	onOpen: (hostId: string) => void;
};
