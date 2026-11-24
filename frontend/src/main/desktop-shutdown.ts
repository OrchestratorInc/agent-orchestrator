import type { RunFileInfo } from "../shared/daemon-discovery";
import type { DaemonStatus } from "../shared/daemon-status";

export async function requestDesktopShutdown(options: {
	keepAlive: boolean;
	appRunId: string;
	status: DaemonStatus;
	preserveAgents(): boolean;
	/** Last synchronous cancellation point, after ownership I/O and before POST. */
	beforeShutdown?(): boolean;
	readRunInfo(): Promise<RunFileInfo | null>;
	requestShutdown(port: number, body: { stopAgents: boolean; appRunId: string }): Promise<{ ok: boolean; status: number }>;
}): Promise<boolean | null> {
	if (options.keepAlive || options.status.state !== "ready" || !options.status.port) return false;
	const info = await options.readRunInfo();
	if (!info || info.port !== options.status.port) {
		throw new Error("Daemon ownership changed before desktop quit");
	}
	if (info.owner !== "app") return false;
	if (!info.appRunId || info.appRunId !== options.appRunId) {
		throw new Error("Daemon app ownership changed before desktop quit");
	}
	// null distinguishes a canceled quit from a daemon this desktop does not own.
	if (options.beforeShutdown && !options.beforeShutdown()) return null;
	const response = await options.requestShutdown(info.port, {
		stopAgents: !options.preserveAgents(),
		appRunId: options.appRunId,
	});
	if (!response.ok) throw new Error(`Agent shutdown returned HTTP ${response.status}`);
	return true;
}
