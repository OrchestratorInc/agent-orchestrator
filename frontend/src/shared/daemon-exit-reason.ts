// Why a daemon process exited, read from the tail of its own output. The daemon
// prints its fatal error as the last line of stderr before exiting, and a Go
// panic prints "panic:" ahead of the stack. Telemetry gets only this fixed
// reason, never the output itself, which can hold local paths.
export const DAEMON_EXIT_REASONS = [
	"already_running",
	"data_dir_locked",
	"store_open_failed",
	"run_file_unreadable",
	"working_dir_failed",
	"port_in_use",
	"disk_full",
	"permission_denied",
	"panic",
	"unknown",
] as const;

export type DaemonExitReason = (typeof DAEMON_EXIT_REASONS)[number];

// The daemon's exit code for "another AO process holds the data dir" (cli.exitDataDirLocked).
const EXIT_DATA_DIR_LOCKED = 75;

// Checked in order against the last line of output; the first match wins.
const LAST_LINE_REASONS: readonly [RegExp, DaemonExitReason][] = [
	[/daemon already running/i, "already_running"],
	[/data dir is in use by another/i, "data_dir_locked"],
	[/no space left on device|disk full|not enough space on the disk/i, "disk_full"],
	[/address already in use|only one usage of each socket address/i, "port_in_use"],
	[/open store:|database disk image is malformed|database is locked|sqlite/i, "store_open_failed"],
	[/inspect run-file/i, "run_file_unreadable"],
	[/daemon working directory/i, "working_dir_failed"],
	[/permission denied|access is denied|operation not permitted/i, "permission_denied"],
];

/** The reason a daemon exited, from its exit code and the output it left. */
export function daemonExitReason(output: string | undefined, exitCode: number | null | undefined): DaemonExitReason {
	if (exitCode === EXIT_DATA_DIR_LOCKED) return "data_dir_locked";
	const text = output ?? "";
	if (/(?:^|\n)(?:panic: |fatal error: )/.test(text)) return "panic";
	// Only the last line: earlier lines are routine logs, and a warning that
	// mentions a path or permission is not why the process stopped.
	const lastLine = text.trimEnd().split(/\r?\n/).at(-1) ?? "";
	for (const [pattern, reason] of LAST_LINE_REASONS) {
		if (pattern.test(lastLine)) return reason;
	}
	return "unknown";
}
