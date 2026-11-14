import type { DaemonLaunchSpec } from "../shared/daemon-launch";

export type DaemonScopeMode = "off" | "systemd";

export type DaemonScopeEnv = Record<string, string | undefined>;

export type DaemonScopeProbe = {
	platform: NodeJS.Platform;
	env: DaemonScopeEnv;
	exists: (file: string) => boolean;
	runProbe: (command: string, args: string[]) => Promise<boolean>;
	findExecutable: (name: string) => string | null;
	scopeLaunchFailed?: boolean;
};

export type DaemonScopePlan =
	| { kind: "scope"; launch: DaemonLaunchSpec; unit: string; slice: string }
	| { kind: "oom_score_adj"; reason: string; adjustment: number }
	| { kind: "unchanged"; reason: string };

export const DAEMON_SCOPE_SLICE = "app-ao.slice";
export const FALLBACK_OOM_SCORE_ADJ = 300;
const MEMORY_VALUE = /^(?:\d{1,15}[KMGT]?|\d{1,3}%|infinity)$/;

export function daemonScopeMode(env: DaemonScopeEnv): DaemonScopeMode {
	return env.AO_DAEMON_SCOPE?.trim().toLowerCase() === "systemd" ? "systemd" : "off";
}

function memoryProperty(name: "MemoryHigh" | "MemoryMax", value: string | undefined): string[] {
	const trimmed = value?.trim();
	return trimmed && MEMORY_VALUE.test(trimmed) ? ["--property", `${name}=${trimmed}`] : [];
}

export function scopeUnitName(pid: number, nowMs: number): string {
	return `ao-daemon-${pid}-${nowMs.toString(36)}`;
}

export function wrapLaunchInScope(launch: DaemonLaunchSpec, systemdRun: string, unit: string | null, env: DaemonScopeEnv): DaemonLaunchSpec {
	return {
		...launch,
		command: systemdRun,
		args: [
			"--user",
			"--scope",
			"--quiet",
			"--collect",
			...(unit ? [`--unit=${unit}`] : []),
			`--slice=${DAEMON_SCOPE_SLICE}`,
			...memoryProperty("MemoryHigh", env.AO_DAEMON_MEMORY_HIGH),
			...memoryProperty("MemoryMax", env.AO_DAEMON_MEMORY_MAX),
			"--",
			launch.command,
			...launch.args,
		],
	};
}

export function insideFlatpak(env: DaemonScopeEnv, exists: (file: string) => boolean): boolean {
	return Boolean(env.FLATPAK_ID) || exists("/.flatpak-info");
}

export async function planDaemonScope(
	launch: DaemonLaunchSpec,
	probe: DaemonScopeProbe,
	identity: { pid: number; nowMs: number },
): Promise<DaemonScopePlan> {
	if (probe.platform !== "linux") return { kind: "unchanged", reason: "not_linux" };
	if (daemonScopeMode(probe.env) === "off") return { kind: "unchanged", reason: "disabled" };
	if (launch.shell) return { kind: "unchanged", reason: "shell_launch" };
	const fallback = (reason: string): DaemonScopePlan => ({ kind: "oom_score_adj", reason, adjustment: FALLBACK_OOM_SCORE_ADJ });
	if (insideFlatpak(probe.env, probe.exists)) return fallback("flatpak");
	if (probe.scopeLaunchFailed) return fallback("scope_launch_failed");
	const systemdRun = probe.findExecutable("systemd-run");
	if (!systemdRun) return fallback("systemd_run_missing");
	const probeArgs = wrapLaunchInScope({ ...launch, command: "true", args: [] }, systemdRun, null, probe.env).args;
	if (!(await probe.runProbe(systemdRun, probeArgs))) {
		return fallback("user_manager_unavailable");
	}
	const unit = scopeUnitName(identity.pid, identity.nowMs);
	return { kind: "scope", launch: wrapLaunchInScope(launch, systemdRun, unit, probe.env), unit, slice: DAEMON_SCOPE_SLICE };
}

export function raiseOomScoreAdj(
	pid: number,
	adjustment: number,
	io: { read: (file: string) => string; write: (file: string, value: string) => void },
): number | null {
	const file = `/proc/${pid}/oom_score_adj`;
	try {
		const current = Number.parseInt(io.read(file).trim(), 10);
		if (Number.isFinite(current) && current >= adjustment) return current;
		io.write(file, String(adjustment));
		return adjustment;
	} catch {
		return null;
	}
}
