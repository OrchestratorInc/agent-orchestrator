// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
	DAEMON_AUTO_RESTART_MAX_ATTEMPTS,
	DAEMON_AUTO_RESTART_WINDOW_MS,
	daemonExitWasUngraceful,
	planDaemonAutoRestart,
} from "./daemon-auto-restart";

describe("daemonExitWasUngraceful", () => {
	it("treats a left-behind run-file as a crash even on a clean exit code", () => {
		expect(daemonExitWasUngraceful({ runFilePresent: true, code: 0, signal: null })).toBe(true);
	});

	it("treats a clean exit that removed the run-file as graceful (ao stop)", () => {
		expect(daemonExitWasUngraceful({ runFilePresent: false, code: 0, signal: null })).toBe(false);
	});

	it("treats a non-zero exit as ungraceful", () => {
		expect(daemonExitWasUngraceful({ runFilePresent: false, code: 1, signal: null })).toBe(true);
	});

	it("treats a terminating signal as ungraceful", () => {
		expect(daemonExitWasUngraceful({ runFilePresent: false, code: null, signal: "SIGKILL" })).toBe(true);
	});

	it("treats an unknown exit with no signal and no run-file as graceful", () => {
		expect(daemonExitWasUngraceful({ runFilePresent: false, code: null, signal: null })).toBe(false);
	});
});

describe("planDaemonAutoRestart", () => {
	it("schedules the first restart with the base delay", () => {
		const { plan, recentExits } = planDaemonAutoRestart([], 0);

		expect(plan).toEqual({ action: "restart", attempt: 1, delayMs: 1_000 });
		expect(recentExits).toEqual([0]);
	});

	it("doubles the delay for each successive exit inside the window", () => {
		const delays: number[] = [];
		let recent: number[] = [];
		for (let i = 0; i < 5; i += 1) {
			const { plan, recentExits } = planDaemonAutoRestart(recent, i);
			if (plan.action === "restart") delays.push(plan.delayMs);
			recent = recentExits;
		}

		expect(delays).toEqual([1_000, 2_000, 4_000, 8_000, 16_000]);
	});

	it("caps the backoff delay instead of doubling past the maximum", () => {
		const now = 0;
		const { plan } = planDaemonAutoRestart([now, now, now, now, now], now);

		expect(plan).toEqual({ action: "restart", attempt: 6, delayMs: 30_000 });
	});

	it(`gives up after ${DAEMON_AUTO_RESTART_MAX_ATTEMPTS} exits inside the window`, () => {
		let recent: number[] = [];
		let last: ReturnType<typeof planDaemonAutoRestart>["plan"] | null = null;
		for (let i = 0; i < DAEMON_AUTO_RESTART_MAX_ATTEMPTS + 1; i += 1) {
			const { plan, recentExits } = planDaemonAutoRestart(recent, i);
			recent = recentExits;
			last = plan;
		}

		expect(last).toEqual({ action: "give_up" });
		expect(recent).toHaveLength(DAEMON_AUTO_RESTART_MAX_ATTEMPTS);
	});

	it("ages out exits older than the window so retries resume", () => {
		const stale = Array.from({ length: DAEMON_AUTO_RESTART_MAX_ATTEMPTS }, (_, i) => i);
		const now = DAEMON_AUTO_RESTART_WINDOW_MS + DAEMON_AUTO_RESTART_MAX_ATTEMPTS + 1;

		const { plan, recentExits } = planDaemonAutoRestart(stale, now);

		expect(plan).toEqual({ action: "restart", attempt: 1, delayMs: 1_000 });
		expect(recentExits).toEqual([now]);
	});
});
