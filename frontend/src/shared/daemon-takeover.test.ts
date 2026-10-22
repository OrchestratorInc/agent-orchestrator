// Unit tests for portHolderPidToReplace. Run with:
//   cd frontend && npx vitest run src/shared/daemon-takeover.test.ts
import { describe, expect, it, vi } from "vitest";
import { DAEMON_SERVICE_NAME, type DaemonProbe } from "./daemon-attach";
import {
	RUN_FILE_START_SKEW_MS,
	browserDaemonOwnershipDecision,
	portHolderPidToReplace,
} from "./daemon-takeover";

// A minimal valid DaemonProbe (non-null means the AO daemon answered /healthz).
const healthyProbe: DaemonProbe = {
	status: "ok",
	service: DAEMON_SERVICE_NAME,
	pid: 1234,
};

// The daemon recorded PID 4242 in running.json at this instant.
const runFile = { pid: 4242, startedAtMs: 1_700_000_000_000 };

// Declined, with a reason that matches: nothing is signalled and the log says why.
const declined = (reason: RegExp) => ({ pid: null, reason: expect.stringMatching(reason) });

describe("portHolderPidToReplace", () => {
	it("replaces a wedged daemon that still answers the probe, using the PID it reported", async () => {
		const processStartMs = vi.fn(async () => null);
		await expect(portHolderPidToReplace({ probe: healthyProbe, runFile: null, processStartMs })).resolves.toEqual({
			pid: 1234,
		});
		expect(processStartMs).not.toHaveBeenCalled();
	});

	it("signals the PID the probe reported, never a different live PID named by a stale run-file", async () => {
		// The run-file names 4242 and that process is alive and old enough to look
		// like the daemon, but the daemon that actually holds the port is 1234.
		const processStartMs = vi.fn(async () => runFile.startedAtMs - 60_000);
		await expect(portHolderPidToReplace({ probe: healthyProbe, runFile, processStartMs })).resolves.toEqual({
			pid: 1234,
		});
		expect(processStartMs).not.toHaveBeenCalled();
	});

	it("does not signal a live unrelated process that reused the run-file PID after the daemon died", async () => {
		// Nothing answered the probe. PID 4242 is alive, but it started an hour after
		// the daemon wrote running.json, so it cannot be the daemon that wrote it.
		const processStartMs = vi.fn(async () => runFile.startedAtMs + 3_600_000);
		await expect(portHolderPidToReplace({ probe: null, runFile, processStartMs })).resolves.toEqual(
			declined(/started 3600 s after running\.json was written, so it is not the daemon that wrote it/),
		);
		expect(processStartMs).toHaveBeenCalledWith(4242);
	});

	it("signals the run-file PID when the process began before the run-file was written (wedged, silent daemon)", async () => {
		// The daemon stamps startedAt after it boots, so the writer is always older.
		const processStartMs = vi.fn(async () => runFile.startedAtMs - 8_000);
		await expect(portHolderPidToReplace({ probe: null, runFile, processStartMs })).resolves.toEqual({ pid: 4242 });
	});

	it("allows the measurement skew, and no more", async () => {
		const atSkew = async () => runFile.startedAtMs + RUN_FILE_START_SKEW_MS;
		const pastSkew = async () => runFile.startedAtMs + RUN_FILE_START_SKEW_MS + 1;
		await expect(portHolderPidToReplace({ probe: null, runFile, processStartMs: atSkew })).resolves.toEqual({
			pid: 4242,
		});
		await expect(portHolderPidToReplace({ probe: null, runFile, processStartMs: pastSkew })).resolves.toEqual(
			declined(/started 5 s after running\.json was written/),
		);
	});

	it("does not signal when the process start cannot be read, and says so", async () => {
		const processStartMs = vi.fn(async () => null);
		await expect(portHolderPidToReplace({ probe: null, runFile, processStartMs })).resolves.toEqual(
			declined(/start time could not be read/),
		);
	});

	it("does not signal on a start time that is not a number", async () => {
		const processStartMs = vi.fn(async () => Number.NaN);
		await expect(portHolderPidToReplace({ probe: null, runFile, processStartMs })).resolves.toMatchObject({
			pid: null,
		});
	});

	it("does not signal, or ask the OS, when there is neither a probe answer nor a run-file", async () => {
		const processStartMs = vi.fn(async () => 0);
		await expect(portHolderPidToReplace({ probe: null, runFile: null, processStartMs })).resolves.toEqual(
			declined(/no readable running\.json/),
		);
		expect(processStartMs).not.toHaveBeenCalled();
	});

	it("does not trust a run-file that has no usable start time", async () => {
		const processStartMs = vi.fn(async () => 0);
		for (const startedAtMs of [0, -62_135_596_800_000, Number.NaN]) {
			await expect(
				portHolderPidToReplace({ probe: null, runFile: { pid: 4242, startedAtMs }, processStartMs }),
			).resolves.toEqual(declined(/no usable pid or start time/));
		}
		expect(processStartMs).not.toHaveBeenCalled();
	});

	it.each([0, 1, -1, 1.5, Number.NaN])("never names PID %s, which no daemon has and kill() would misread", async (pid) => {
		const processStartMs = vi.fn(async () => 0);
		await expect(
			portHolderPidToReplace({ probe: { ...healthyProbe, pid }, runFile: null, processStartMs }),
		).resolves.toEqual(declined(/unusable pid/));
		await expect(
			portHolderPidToReplace({ probe: null, runFile: { pid, startedAtMs: runFile.startedAtMs }, processStartMs }),
		).resolves.toEqual(declined(/no usable pid or start time/));
		expect(processStartMs).not.toHaveBeenCalled();
	});
});

describe("browserDaemonOwnershipDecision", () => {
	it("reuses a daemon authenticated by this app launch", () => {
		expect(browserDaemonOwnershipDecision("run-1", { owner: "app", appRunId: "run-1" })).toEqual({
			action: "attach",
		});
	});

	it("replaces an older app daemon without making it persistent", () => {
		expect(browserDaemonOwnershipDecision("run-2", { owner: "app", appRunId: "run-1" })).toEqual({
			action: "replace",
			keepAlive: false,
		});
	});

	it("preserves persistent and headless daemon lifetime during replacement", () => {
		expect(browserDaemonOwnershipDecision("run-2", { owner: "persistent", appRunId: "run-1" })).toEqual({
			action: "replace",
			keepAlive: true,
		});
		expect(browserDaemonOwnershipDecision("run-2", {})).toEqual({ action: "replace", keepAlive: true });
	});
});
