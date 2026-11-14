// Daemon failures happen in the Electron main process, which has no PostHog
// client. Main stamps a machine-readable `code` on every failing DaemonStatus
// (shared/daemon-status.ts); this module rides the existing daemon:status IPC
// push and reports those failures through the renderer's telemetry client.
import type { DaemonStatus } from "../../shared/daemon-status";
import { aoBridge } from "./bridge";
import { captureRendererEvent } from "./telemetry";

let started = false;
let lastFailureKey: string | null = null;

function failureKey(status: DaemonStatus): string | null {
	if (status.state === "ready") return null;
	if (!status.code) return null;
	return [status.state, status.code, status.exitCode ?? "", status.signal ?? ""].join("|");
}

function reportStatus(status: DaemonStatus): void {
	const key = failureKey(status);
	if (!key) {
		// Healthy status resets dedupe so a repeat failure after recovery is a new event.
		lastFailureKey = null;
		return;
	}
	if (key === lastFailureKey) return;
	lastFailureKey = key;
	void captureRendererEvent("ao.daemon.failed", {
		daemon_state: status.state,
		code: status.code,
		exit_code: typeof status.exitCode === "number" ? status.exitCode : undefined,
		signal: status.signal ?? undefined,
	});
}

/** Idempotent; returns a stop function (used by tests). */
export function startDaemonFailureTelemetry(): () => void {
	if (started) return () => undefined;
	started = true;
	void aoBridge.daemon
		.getStatus()
		.then(reportStatus)
		.catch(() => undefined);
	let stopListener: () => void = () => undefined;
	try {
		stopListener = aoBridge.daemon.onStatus(reportStatus);
	} catch {
		// Preload bridge unavailable (browser preview): initial getStatus already handled.
	}
	return () => {
		started = false;
		lastFailureKey = null;
		stopListener();
	};
}
