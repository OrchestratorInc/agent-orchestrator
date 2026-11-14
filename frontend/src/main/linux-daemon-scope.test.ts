// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import type { DaemonLaunchSpec } from "../shared/daemon-launch";
import { daemonScopeMode, DAEMON_SCOPE_SLICE, planDaemonScope, raiseOomScoreAdj, wrapLaunchInScope, type DaemonScopeProbe } from "./linux-daemon-scope";

const launch: DaemonLaunchSpec = { command: "/tmp/.mount_ao/resources/daemon/ao", args: ["daemon"], cwd: "/home/u/.ao/data", shell: false, source: "bundled" };

function probe(overrides: Partial<DaemonScopeProbe> = {}): DaemonScopeProbe {
	return {
		platform: "linux",
		env: { AO_DAEMON_SCOPE: "systemd" },
		exists: () => false,
		findExecutable: (name) => (name === "systemd-run" ? "/usr/bin/systemd-run" : null),
		runProbe: vi.fn(async () => true),
		...overrides,
	};
}

describe("daemonScopeMode", () => {
	it.each([[undefined, "off"], ["", "off"], ["1", "off"], ["systemd", "systemd"], [" SystemD ", "systemd"]])("%s -> %s", (value, mode) => {
		expect(daemonScopeMode({ AO_DAEMON_SCOPE: value })).toBe(mode);
	});
});

describe("planDaemonScope", () => {
	const identity = { pid: 1234, nowMs: 36 ** 3 };

	it("wraps the daemon in a transient user scope when systemd is reachable", async () => {
		const p = probe();
		const plan = await planDaemonScope(launch, p, identity);
		expect(plan).toEqual({
			kind: "scope",
			unit: "ao-daemon-1234-1000",
			slice: DAEMON_SCOPE_SLICE,
			launch: {
				...launch,
				command: "/usr/bin/systemd-run",
				args: ["--user", "--scope", "--quiet", "--collect", "--unit=ao-daemon-1234-1000", "--slice=app-ao.slice", "--", launch.command, "daemon"],
			},
		});
		expect(p.runProbe).toHaveBeenCalledWith("/usr/bin/systemd-run", ["--user", "--scope", "--quiet", "--collect", "--slice=app-ao.slice", "--", "true"]);
	});

	it("probes with the real launch's memory properties", async () => {
		const p = probe({ env: { AO_DAEMON_SCOPE: "systemd", AO_DAEMON_MEMORY_MAX: "8G" } });
		await planDaemonScope(launch, p, identity);
		expect(p.runProbe).toHaveBeenCalledWith("/usr/bin/systemd-run", ["--user", "--scope", "--quiet", "--collect", "--slice=app-ao.slice", "--property", "MemoryMax=8G", "--", "true"]);
	});

	it.each([
		["not_linux", probe({ platform: "darwin" })],
		["not_linux", probe({ platform: "win32" })],
		["disabled", probe({ env: {} })],
	])("leaves the launch unchanged: %s", async (reason, p) => {
		expect(await planDaemonScope(launch, p, identity)).toEqual({ kind: "unchanged", reason });
		expect(p.runProbe).not.toHaveBeenCalled();
	});

	it("leaves a shell launch unchanged", async () => {
		expect(await planDaemonScope({ ...launch, shell: true }, probe(), identity)).toEqual({ kind: "unchanged", reason: "shell_launch" });
	});

	it.each([
		["flatpak", probe({ env: { AO_DAEMON_SCOPE: "systemd", FLATPAK_ID: "dev.ao" } })],
		["flatpak", probe({ exists: (file) => file === "/.flatpak-info" })],
		["systemd_run_missing", probe({ findExecutable: () => null })],
		["user_manager_unavailable", probe({ runProbe: async () => false })],
		["scope_launch_failed", probe({ scopeLaunchFailed: true })],
	])("falls back to an OOM score adjustment: %s", async (reason, p) => {
		expect(await planDaemonScope(launch, p, identity)).toEqual({ kind: "oom_score_adj", reason, adjustment: 300 });
	});
});

describe("wrapLaunchInScope", () => {
	it("passes only well-formed memory limits", () => {
		const wrapped = wrapLaunchInScope(launch, "/usr/bin/systemd-run", "u", { AO_DAEMON_MEMORY_HIGH: "12G", AO_DAEMON_MEMORY_MAX: "80%" });
		expect(wrapped.args).toContain("MemoryHigh=12G");
		expect(wrapped.args).toContain("MemoryMax=80%");
		const rejected = wrapLaunchInScope(launch, "/usr/bin/systemd-run", "u", { AO_DAEMON_MEMORY_MAX: "8G --property=Delegate=yes" });
		expect(rejected.args.join(" ")).not.toContain("MemoryMax");
		expect(rejected.args.slice(-3)).toEqual(["--", launch.command, "daemon"]);
	});
});

describe("raiseOomScoreAdj", () => {
	it("raises a lower value", () => {
		const write = vi.fn();
		expect(raiseOomScoreAdj(7, 300, { read: () => "0\n", write })).toBe(300);
		expect(write).toHaveBeenCalledWith("/proc/7/oom_score_adj", "300");
	});

	it("never lowers a higher value", () => {
		const write = vi.fn();
		expect(raiseOomScoreAdj(7, 300, { read: () => "500", write })).toBe(500);
		expect(write).not.toHaveBeenCalled();
	});

	it("reports failure as null", () => {
		expect(raiseOomScoreAdj(7, 300, { read: () => { throw new Error("EACCES"); }, write: vi.fn() })).toBeNull();
	});
});
