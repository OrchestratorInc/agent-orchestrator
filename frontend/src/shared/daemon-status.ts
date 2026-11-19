// DaemonStatus is the supervisor → renderer handshake payload, shared by the
// Electron main process (which derives it) and the preload bridge (which types
// the IPC surface). The renderer picks it up through the preload's AoBridge type.
// Machine-readable failure classification for telemetry. `message` is
// human-facing and may contain local paths; `code` is what gets reported.
// Statuses without a code (normal ready, user-initiated stop) are not failures.
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
	// Recent daemon stdout/stderr retained by the Electron supervisor for local
	// troubleshooting. It is never sent to telemetry.
	details?: string;
	code?: DaemonFailureCode;
	exitCode?: number | null;
	signal?: string | null;
	// The daemon also serves [::1] at its port (its probes' ipv6Loopback), so a
	// *.localhost name, which Chromium resolves to ::1 first, reaches it.
	ipv6Loopback?: boolean;
};
