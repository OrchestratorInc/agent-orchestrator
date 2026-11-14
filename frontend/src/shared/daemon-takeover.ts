import type { DaemonProbe } from "./daemon-attach";
import type { RunFileInfo } from "./daemon-discovery";

export const RUN_FILE_START_SKEW_MS = 5_000;

export type PortHolderEvidence = {
	probe: DaemonProbe | null;
	runFile: Pick<RunFileInfo, "pid" | "startedAtMs"> | null;
	processStartMs: (pid: number) => Promise<number | null>;
};

export type PortHolderDecision =
	| { pid: number }
	| { pid: null; reason: string };

function isSignalablePid(pid: number): boolean {
	return Number.isSafeInteger(pid) && pid > 1;
}

export async function portHolderPidToReplace({
	probe,
	runFile,
	processStartMs,
}: PortHolderEvidence): Promise<PortHolderDecision> {
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

export function browserDaemonOwnershipDecision(
	currentAppRunId: string,
	existing: { owner?: string; appRunId?: string },
): BrowserDaemonOwnershipDecision {
	if (existing.appRunId === currentAppRunId) return { action: "attach" };
	return { action: "replace", keepAlive: existing.owner !== "app" };
}

export type BrowserRuntimeRecoveryDecision =
	| { action: "wait" }
	| { action: "replace"; reason: string };

export const HANDSHAKE_CLOSED_BEFORE_ACCEPT = "closed-before-accept";

const UNRECOVERABLE_HANDSHAKE_REASONS: ReadonlySet<string> = new Set([
	"invalid-token",
	HANDSHAKE_CLOSED_BEFORE_ACCEPT,
]);

export function browserRuntimeRecoveryDecision(input: {
	rejectedReason: string | null;
	spawnedHere: boolean;
	attempted: boolean;
}): BrowserRuntimeRecoveryDecision {
	if (input.rejectedReason === null) return { action: "wait" };
	if (!UNRECOVERABLE_HANDSHAKE_REASONS.has(input.rejectedReason)) return { action: "wait" };
	if (input.spawnedHere) return { action: "wait" };
	if (input.attempted) return { action: "wait" };
	return { action: "replace", reason: input.rejectedReason };
}

export function shouldReplaceDaemonForStartup(input: {
	ownership: BrowserDaemonOwnershipDecision;
	forceRequested: boolean;
}): boolean {
	return input.forceRequested || input.ownership.action === "replace";
}

export function keepAliveForReplacement(input: {
	ownership: BrowserDaemonOwnershipDecision;
	forceRequested: boolean;
}): boolean {
	return input.forceRequested ? true : input.ownership.action === "replace" && input.ownership.keepAlive;
}
