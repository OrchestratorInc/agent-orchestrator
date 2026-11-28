import type { DaemonLaunchSpec } from "../shared/daemon-launch";

// Linux prototype (opt-in, AO_DAEMON_SCOPE=systemd): run the daemon, and so
// tmux and every agent it starts, in its own transient systemd user scope
// instead of the desktop app's scope.
//
// Why: on a systemd desktop the launcher puts the app in a scope such as
// app-Hyprland-ao-1234.scope, and everything the daemon starts inherits that
// cgroup. A runaway agent (a test suite's headless Chromium) then shares one
// memory and OOM domain with the app: a cgroup OOM kill with memory.oom.group,
// systemd-oomd acting on the scope, or the launcher stopping the scope takes
// every process in it, the app included. A separate scope in app-ao.slice
// gives agents their own domain, and lets a memory limit apply to them alone.
//
// `systemd-run --scope` registers its own PID in the new scope and then execs
// the command, so the spawned child keeps its PID, pipes, process group,
// exit code and signal: the rest of the daemon supervision is unchanged.
//
// Fallback when no systemd user manager is reachable (non-systemd distros,
// containers, Flatpak, a broken user bus): raise the daemon's oom_score_adj so
// its descendants inherit a higher score and the kernel OOM killer picks a
// runaway agent before the app. Raising is allowed without privileges. The
// same fallback applies for the rest of the run once a scoped daemon exits
// before it is ready (main.ts retries that start once without the scope).

export type DaemonScopeMode = "off" | "systemd";

export type DaemonScopeEnv = Record<string, string | undefined>;

export type DaemonScopeProbe = {
	platform: NodeJS.Platform;
	env: DaemonScopeEnv;
	exists: (file: string) => boolean;
	/** Resolves true when `systemd-run --user --scope -- true` exits 0. */
	runProbe: (command: string, args: string[]) => Promise<boolean>;
	findExecutable: (name: string) => string | null;
	/** A scoped daemon already failed to start in this run: use the fallback. */
	scopeLaunchFailed?: boolean;
};

export type DaemonScopePlan =
	| { kind: "scope"; launch: DaemonLaunchSpec; unit: string; slice: string }
	| { kind: "oom_score_adj"; reason: string; adjustment: number }
	| { kind: "unchanged"; reason: string };

export const DAEMON_SCOPE_SLICE = "app-ao.slice";
export const FALLBACK_OOM_SCORE_ADJ = 300;
// systemd resource values: bytes with an optional K/M/G/T suffix, a
// percentage of physical memory, or "infinity".
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

/** Wraps a direct (non-shell) daemon launch in `systemd-run --user --scope`. */
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
	// A Flatpak sandbox cannot create host scopes without escaping it.
	if (insideFlatpak(probe.env, probe.exists)) return fallback("flatpak");
	if (probe.scopeLaunchFailed) return fallback("scope_launch_failed");
	const systemdRun = probe.findExecutable("systemd-run");
	if (!systemdRun) return fallback("systemd_run_missing");
	// Containers and non-systemd sessions fail here: no user manager or bus.
	// The probe passes the real launch's slice and memory properties, so a
	// refused slice or limit falls back here instead of failing the daemon.
	const probeArgs = wrapLaunchInScope({ ...launch, command: "true", args: [] }, systemdRun, null, probe.env).args;
	if (!(await probe.runProbe(systemdRun, probeArgs))) {
		return fallback("user_manager_unavailable");
	}
	const unit = scopeUnitName(identity.pid, identity.nowMs);
	return { kind: "scope", launch: wrapLaunchInScope(launch, systemdRun, unit, probe.env), unit, slice: DAEMON_SCOPE_SLICE };
}

/**
 * Raises a process's OOM score adjustment, never lowers it. Children forked
 * afterwards inherit the value. Returns the value written, or null.
 */
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
