export type DaemonFailureCode =
	| "not_configured"
	| "daemon_unreachable"
	| "binary_missing"
	| "spawn_failed"
	| "exited"
	| "port_unconfirmed"
	| "not_ready"
	| "identity_mismatch"
	| "datadir_unwritable";

export type DaemonStatus = {
	state: "starting" | "ready" | "stopped" | "error";
	port?: number;
	pid?: number;
	executablePath?: string;
	workingDirectory?: string;
	message?: string;
	details?: string;
	code?: DaemonFailureCode;
	exitCode?: number | null;
	signal?: string | null;
	ipv6Loopback?: boolean;
};
