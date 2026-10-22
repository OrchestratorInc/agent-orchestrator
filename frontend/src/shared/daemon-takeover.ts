// Pure decision helper for the wedged-orphan kill+replace path.
//
// Context: on app launch, after both attach attempts fail (inspectExistingDaemon
// and resolveDaemonFromPort both returned null/non-ready), a process may still
// be holding the daemon port. Leaving it there is not harmless: a new daemon
// does not exit on a busy port, it falls back to an ephemeral one, and the only
// guard against a second daemon is the old one answering /healthz with the PID
// in running.json. A hung daemon answers nothing, so the new one would start
// beside it on the same data dir (or contend for a database lock the hung one
// holds). This helper names the process to kill, and only ever names one it can
// identify as the AO daemon.
//
// A running.json PID that is merely alive proves nothing: the file outlives the
// daemon after a crash, hard kill, or reboot, and the OS then hands that PID to
// an unrelated process (Windows reuses PIDs aggressively). Signalling it would
// SIGTERM that process, and its whole process group. So a PID is only named when:
//   - the probe answered: an AO daemon is serving the port, and its own PID is
//     the one to signal (whatever a stale run-file says), or
//   - nothing answered but the run-file's PID is a process that began before the
//     run-file was written. The daemon stamps startedAt once it has booted, so the
//     process that wrote the file is always older than it; a process that reused
//     the PID after the daemon died is always younger. That keeps recovery of a
//     hung daemon that bound the port and stopped answering /healthz.
//
// Kept side-effect free and dependency-injected (no node:* or electron imports)
// so it can be exercised in vitest without the Electron polyfill layer.

import type { DaemonProbe } from "./daemon-attach";
import type { RunFileInfo } from "./daemon-discovery";

/**
 * How much younger than the run-file's startedAt a process may read and still
 * count as the daemon that wrote it. A real writer is never younger; this covers
 * ps's one-second resolution, the gap between reading the clock and the process
 * table, and ordinary clock jitter between the Go daemon and Electron.
 */
export const RUN_FILE_START_SKEW_MS = 5_000;

export type PortHolderEvidence = {
	/** What /healthz on the expected port answered, or null when nothing valid did. */
	probe: DaemonProbe | null;
	/** The daemon recorded in running.json, or null when there is no readable run-file. */
	runFile: Pick<RunFileInfo, "pid" | "startedAtMs"> | null;
	/** When the live process with this PID started (epoch ms); null when it is gone or unreadable. */
	processStartMs: (pid: number) => Promise<number | null>;
};

export type PortHolderDecision =
	/** The AO daemon to signal. */
	| { pid: number }
	/** Nothing to signal; `reason` says why, for the log. */
	| { pid: null; reason: string };

// The caller signals -pid (the daemon's process group) and falls back to pid. For
// 0 that is the caller's own group, for -1 every process the user may signal, and
// 1 is init/launchd. None of those is ever the daemon, whatever the file says.
function isSignalablePid(pid: number): boolean {
	return Number.isSafeInteger(pid) && pid > 1;
}

/**
 * The AO daemon to kill before spawning, or no PID when none can be identified
 * (spawn immediately; never signal a process we cannot vouch for).
 *
 * When a PID is returned the caller should signal it, wait for the port to free,
 * clear the stale run-file, then spawn a fresh daemon.
 */
export async function portHolderPidToReplace({
	probe,
	runFile,
	processStartMs,
}: PortHolderEvidence): Promise<PortHolderDecision> {
	// An AO daemon answered. By the time this runs both attach paths already
	// returned null, so that is unexpected (e.g. a race); replace it rather than
	// colliding on spawn, and signal the PID it reported itself, not the file's.
	if (probe) {
		return isSignalablePid(probe.pid)
			? { pid: probe.pid }
			: { pid: null, reason: "the daemon that answered the probe reported an unusable pid" };
	}

	if (!runFile) return { pid: null, reason: "nothing answered the probe and there is no readable running.json" };
	if (!isSignalablePid(runFile.pid) || !(runFile.startedAtMs > 0)) {
		return { pid: null, reason: "running.json has no usable pid or start time" };
	}
	const startMs = await processStartMs(runFile.pid);
	if (startMs === null) {
		return { pid: null, reason: "its start time could not be read (ps missing, slow or failed; Windows has no reader)" };
	}
	if (startMs <= runFile.startedAtMs + RUN_FILE_START_SKEW_MS) return { pid: runFile.pid };
	return {
		pid: null,
		reason: `it started ${Math.round((startMs - runFile.startedAtMs) / 1000)} s after running.json was written, so it is not the daemon that wrote it`,
	};
}

export type BrowserDaemonOwnershipDecision =
	| { action: "attach" }
	| { action: "replace"; keepAlive: boolean };

/**
 * A daemon can authenticate to only the Electron launch that handed it the
 * memory-only browser runtime token. Reuse within that launch is safe. Across
 * launches we replace it gracefully, preserving the original persistence mode:
 * app-owned daemons remain app-owned; persistent/headless daemons remain alive
 * after the window closes.
 */
export function browserDaemonOwnershipDecision(
	currentAppRunId: string,
	existing: { owner?: string; appRunId?: string },
): BrowserDaemonOwnershipDecision {
	if (existing.appRunId === currentAppRunId) return { action: "attach" };
	return { action: "replace", keepAlive: existing.owner !== "app" };
}
