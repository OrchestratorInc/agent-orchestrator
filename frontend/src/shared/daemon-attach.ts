import type { DaemonStatus } from "./daemon-status";
import { parseRunFile } from "./daemon-discovery";

export const DEFAULT_DAEMON_PORT = 3001;
export const DAEMON_SERVICE_NAME = "agent-orchestrator-daemon";

export type DaemonProbe = {
	status: string;
	service: string;
	pid: number;
	executablePath?: string;
	workingDirectory?: string;
	startupWorkingDirectory?: string;
	appImagePath?: string;
	ipv6Loopback?: boolean;
};

export type DaemonProber = (port: number, endpoint: "healthz" | "readyz") => Promise<DaemonProbe | null>;

export function expectedDaemonPort(env: Record<string, string | undefined>): number {
	const configured = env.AO_PORT ? Number(env.AO_PORT) : NaN;
	return Number.isInteger(configured) && configured >= 1 && configured <= 65535 ? configured : DEFAULT_DAEMON_PORT;
}

export function parseDaemonProbe(endpoint: "healthz" | "readyz", body: unknown): DaemonProbe | null {
	if (typeof body !== "object" || body === null) return null;
	const candidate = body as Partial<DaemonProbe>;
	if (candidate.status !== (endpoint === "healthz" ? "ok" : "ready")) return null;
	if (candidate.service !== DAEMON_SERVICE_NAME) return null;
	if (typeof candidate.pid !== "number" || !Number.isInteger(candidate.pid)) return null;
	return {
		status: candidate.status,
		service: candidate.service,
		pid: candidate.pid,
		executablePath: typeof candidate.executablePath === "string" ? candidate.executablePath : undefined,
		workingDirectory: typeof candidate.workingDirectory === "string" ? candidate.workingDirectory : undefined,
		startupWorkingDirectory:
			typeof candidate.startupWorkingDirectory === "string" ? candidate.startupWorkingDirectory : undefined,
		appImagePath: typeof candidate.appImagePath === "string" ? candidate.appImagePath : undefined,
		...(candidate.ipv6Loopback === true ? { ipv6Loopback: true } : {}),
	};
}

export type RunFileResolveDeps = {
	runFileContents: string | null;
	isProcessAlive: (pid: number) => boolean;
	probe: DaemonProber;
	identityError: (probe: DaemonProbe) => string | null;
};

export async function resolveDaemonFromRunFile(deps: RunFileResolveDeps): Promise<DaemonStatus | null> {
	const { runFileContents, isProcessAlive, probe, identityError } = deps;
	if (runFileContents === null) return null;
	const info = parseRunFile(runFileContents);
	if (!info || !isProcessAlive(info.pid)) return null;

	const health = await probe(info.port, "healthz");
	if (!health || health.pid !== info.pid) return null;
	return readinessStatus(info.port, info.pid, health, probe, identityError);
}

export type PortProbeResolveDeps = {
	expectedPort: number;
	probe: DaemonProber;
	identityError: (probe: DaemonProbe) => string | null;
};

export async function resolveDaemonFromPort(deps: PortProbeResolveDeps): Promise<DaemonStatus | null> {
	const { expectedPort, probe, identityError } = deps;
	const health = await probe(expectedPort, "healthz");
	if (!health) return null;
	return readinessStatus(expectedPort, health.pid, health, probe, identityError);
}

async function readinessStatus(
	port: number,
	pid: number,
	health: DaemonProbe,
	probe: DaemonProber,
	identityError: (probe: DaemonProbe) => string | null,
): Promise<DaemonStatus> {
	const ready = await probe(port, "readyz");
	if (!ready || ready.pid !== pid) {
		return {
			state: "error",
			port,
			pid,
			executablePath: health.executablePath,
			workingDirectory: health.workingDirectory,
			message: "An Orchestrator daemon is already running, but it is not ready yet.",
			code: "not_ready",
		};
	}

	const message = identityError(ready);
	if (message) {
		return {
			state: "error",
			port,
			pid,
			executablePath: ready.executablePath,
			workingDirectory: ready.workingDirectory,
			message,
			code: "identity_mismatch",
		};
	}

	return {
		state: "ready",
		port,
		pid,
		executablePath: ready.executablePath,
		workingDirectory: ready.workingDirectory,
		...(ready.ipv6Loopback === true ? { ipv6Loopback: true } : {}),
	};
}
