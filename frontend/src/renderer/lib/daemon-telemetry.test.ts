import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DaemonStatus } from "../../shared/daemon-status";
import { aoBridge } from "./bridge";
import { startDaemonFailureTelemetry } from "./daemon-telemetry";
import { captureRendererEvent } from "./telemetry";

vi.mock("./telemetry", () => ({
	captureRendererEvent: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("./bridge", () => ({
	aoBridge: {
		daemon: {
			getStatus: vi.fn(),
			onStatus: vi.fn(),
		},
	},
}));

const captureMock = vi.mocked(captureRendererEvent);
const getStatusMock = vi.mocked(aoBridge.daemon.getStatus);
const onStatusMock = vi.mocked(aoBridge.daemon.onStatus);

describe("daemon failure telemetry", () => {
	let pushStatus!: (status: DaemonStatus) => void;
	let stop: () => void = () => undefined;

	beforeEach(() => {
		captureMock.mockClear();
		onStatusMock.mockClear();
		getStatusMock.mockResolvedValue({ state: "starting" });
		onStatusMock.mockImplementation((listener) => {
			pushStatus = listener;
			return () => undefined;
		});
	});

	afterEach(() => {
		stop();
	});

	it("reports a failing status with coarse fields only", () => {
		stop = startDaemonFailureTelemetry();

		pushStatus({ state: "error", message: "spawn /Users/alice/ao failed", code: "spawn_failed" });

		expect(captureMock).toHaveBeenCalledTimes(1);
		expect(captureMock).toHaveBeenCalledWith("ao.daemon.failed", {
			daemon_state: "error",
			code: "spawn_failed",
			exit_code: undefined,
			signal: undefined,
		});
	});

	it("includes exit code and signal for daemon exits", () => {
		stop = startDaemonFailureTelemetry();

		pushStatus({ state: "stopped", code: "exited", exitCode: 1, signal: "SIGKILL" });

		expect(captureMock).toHaveBeenCalledWith("ao.daemon.failed", {
			daemon_state: "stopped",
			code: "exited",
			exit_code: 1,
			signal: "SIGKILL",
		});
	});

	it("ignores statuses without a failure code (healthy or user-initiated stop)", () => {
		stop = startDaemonFailureTelemetry();

		pushStatus({ state: "ready", port: 3037 });
		pushStatus({ state: "stopped" });

		expect(captureMock).not.toHaveBeenCalled();
	});

	it("does not count ready-state daemon warnings as failures", () => {
		stop = startDaemonFailureTelemetry();

		pushStatus({ state: "ready", code: "port_unconfirmed" });

		expect(captureMock).not.toHaveBeenCalled();
	});

	it("dedupes identical consecutive failures and resets on recovery", () => {
		stop = startDaemonFailureTelemetry();

		pushStatus({ state: "error", code: "daemon_unreachable" });
		pushStatus({ state: "error", code: "daemon_unreachable" });
		expect(captureMock).toHaveBeenCalledTimes(1);

		// A different failure is a new event.
		pushStatus({ state: "stopped", code: "exited", exitCode: 137 });
		expect(captureMock).toHaveBeenCalledTimes(2);

		// Recovery resets dedupe: the same failure recurring is reported again.
		pushStatus({ state: "ready", port: 3037 });
		pushStatus({ state: "error", code: "daemon_unreachable" });
		expect(captureMock).toHaveBeenCalledTimes(3);
	});

	it("reports the initial status from getStatus on start", async () => {
		getStatusMock.mockResolvedValue({ state: "error", code: "binary_missing" });

		stop = startDaemonFailureTelemetry();
		await vi.waitFor(() => expect(captureMock).toHaveBeenCalledTimes(1));

		expect(captureMock).toHaveBeenCalledWith("ao.daemon.failed", {
			daemon_state: "error",
			code: "binary_missing",
			exit_code: undefined,
			signal: undefined,
		});
	});

	it("is idempotent while started and restartable after stop", () => {
		stop = startDaemonFailureTelemetry();
		const noop = startDaemonFailureTelemetry();
		expect(onStatusMock).toHaveBeenCalledTimes(1);
		noop();

		stop();
		stop = startDaemonFailureTelemetry();
		expect(onStatusMock).toHaveBeenCalledTimes(2);
	});
});
