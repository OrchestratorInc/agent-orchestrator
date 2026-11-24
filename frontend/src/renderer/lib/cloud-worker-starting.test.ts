import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp/errors";
import {
	cloudWorkerReadRetry,
	cloudWorkerRequest,
	cloudWorkerStartingDelay,
	cloudWorkerStartingRemaining,
	cloudWorkerStartingWait,
	cloudWorkerStartingWaits,
	isCloudWorkerStarting,
	MAX_STARTING_TIMEOUTS,
	MAX_STARTING_WAIT_MS,
	recordCloudWorkspaceStartupEvent,
	resetCloudWorkerStartingForTests,
	subscribeCloudWorkerStarting,
	WORKER_STARTING_CODE,
} from "./cloud-worker-starting";

const starting = (retryAfterMs?: number) =>
	new CloudCpError("The session's workspace is still starting.", { status: 503, code: WORKER_STARTING_CODE, retryAfterMs });

describe("isCloudWorkerStarting", () => {
	it("reads a worker that cannot answer yet as starting, from today's control plane and the one that says so", () => {
		expect(isCloudWorkerStarting(starting())).toBe(true);
		expect(isCloudWorkerStarting(new CloudCpError("still starting", { status: 409, code: WORKER_STARTING_CODE }))).toBe(true);
		expect(isCloudWorkerStarting(new CloudCpError("timeout", { status: 504, code: "WORKER_TIMEOUT" }))).toBe(true);
		expect(isCloudWorkerStarting(new CloudCpError("gateway", { status: 504 }))).toBe(true);
		expect(isCloudWorkerStarting(new CloudCpError("queued", { status: 504, code: "TRANSPORT_TIMEOUT" }))).toBe(true);
		expect(isCloudWorkerStarting(new CloudCpError("absent", { status: 409, code: "WORKER_UNAVAILABLE" }))).toBe(true);
	});

	it("leaves real failures as failures", () => {
		expect(isCloudWorkerStarting(new CloudCpError("bad", { status: 400, code: "validation_error" }))).toBe(false);
		expect(isCloudWorkerStarting(new CloudCpError("boom", { status: 500, code: "internal_error" }))).toBe(false);
		expect(isCloudWorkerStarting(new Error("network"))).toBe(false);
	});
});

describe("cloudWorkerStartingDelay", () => {
	it("backs off from 5 seconds to a minute", () => {
		expect([1, 2, 3, 4, 5, 6, 9].map((attempt) => cloudWorkerStartingDelay(attempt))).toEqual([5_000, 10_000, 20_000, 40_000, 60_000, 60_000, 60_000]);
	});

	it("honours the control plane's Retry-After, within bounds", () => {
		expect(cloudWorkerStartingDelay(1, starting(15_000))).toBe(15_000);
		expect(cloudWorkerStartingDelay(4, starting(2_000))).toBe(2_000);
		expect(cloudWorkerStartingDelay(1, starting(0))).toBe(1_000);
		expect(cloudWorkerStartingDelay(1, starting(60 * 60_000))).toBe(5 * 60_000);
	});
});

describe("the session's starting backoff", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-11-23T10:00:00Z"));
		resetCloudWorkerStartingForTests();
	});
	afterEach(() => {
		resetCloudWorkerStartingForTests();
		vi.useRealTimers();
	});

	it("holds a session's reads after a starting answer, growing the wait, and lets them go once it answers", async () => {
		const listener = vi.fn();
		const unsubscribe = subscribeCloudWorkerStarting(listener);
		const send = vi.fn().mockRejectedValueOnce(starting()).mockRejectedValueOnce(starting()).mockResolvedValue({ ok: true });
		await expect(cloudWorkerRequest("org", "s", send)).rejects.toMatchObject({ code: WORKER_STARTING_CODE });
		expect(cloudWorkerStartingWait("org", "s")).toBe(true);
		expect(cloudWorkerStartingRemaining("org", "s")).toBe(5_000);
		// Another session is not held back.
		expect(cloudWorkerStartingWait("org", "other")).toBe(false);
		listener.mockClear();
		vi.advanceTimersByTime(5_000);
		// The window's end re-renders the readers so they ask again.
		expect(listener).toHaveBeenCalled();
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
		await expect(cloudWorkerRequest("org", "s", send)).rejects.toBeInstanceOf(CloudCpError);
		expect(cloudWorkerStartingRemaining("org", "s")).toBe(10_000);
		vi.advanceTimersByTime(10_000);
		await expect(cloudWorkerRequest("org", "s", send)).resolves.toEqual({ ok: true });
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
		unsubscribe();
	});

	it("waits as long as Retry-After says", async () => {
		await expect(cloudWorkerRequest("org", "s", () => Promise.reject(starting(30_000)))).rejects.toBeInstanceOf(CloudCpError);
		expect(cloudWorkerStartingRemaining("org", "s")).toBe(30_000);
	});

	it("does not hold reads back after a failure that is not a starting worker", async () => {
		await expect(cloudWorkerRequest("org", "s", () => Promise.reject(new CloudCpError("bad", { status: 400 })))).rejects.toBeInstanceOf(CloudCpError);
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
	});

	it("holds reads for a worker that has just connected, and lets them go once the workspace is ready", () => {
		const event = (type: string, createdAt = "2026-11-23T09:59:50Z") => ({ sessionId: "s", sequence: 1, type, payload: {}, createdAt });
		// An old connect is an earlier worker's.
		recordCloudWorkspaceStartupEvent("org", "s", event("worker.ready", "2026-11-23T09:00:00Z"));
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
		recordCloudWorkspaceStartupEvent("org", "s", event("worker.connected"));
		expect(cloudWorkerStartingWait("org", "s")).toBe(true);
		recordCloudWorkspaceStartupEvent("org", "s", event("agent.ready"));
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
		// Another session's events are not this one's.
		recordCloudWorkspaceStartupEvent("org", "s", { ...event("worker.ready"), sessionId: "other" });
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
	});
});

describe("the session's starting budget", () => {
	const startingAnswer = () => new CloudCpError("The session is still starting. Try again shortly.", { status: 409, code: WORKER_STARTING_CODE, retryAfterMs: 5_000 });
	const timeouts = [
		() => new CloudCpError("The worker did not complete the request in time.", { status: 504, code: "WORKER_TIMEOUT" }),
		() => new CloudCpError("gateway", { status: 504 }),
		() => new CloudCpError("queued", { status: 504, code: "TRANSPORT_TIMEOUT" }),
	];
	const ask = (error: unknown) => cloudWorkerRequest("org", "s", () => Promise.reject(error)).catch((thrown: unknown) => thrown);

	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-11-23T10:00:00Z"));
		resetCloudWorkerStartingForTests();
	});
	afterEach(() => {
		resetCloudWorkerStartingForTests();
		vi.useRealTimers();
	});

	it("waits out WORKER_STARTING at its Retry-After for 30 minutes from the first answer, then lets the read fail", async () => {
		expect(cloudWorkerStartingWaits(await ask(startingAnswer()))).toBe(true);
		expect(cloudWorkerStartingRemaining("org", "s")).toBe(5_000);
		for (let elapsed = 5_000; elapsed < MAX_STARTING_WAIT_MS; elapsed += 5_000) {
			vi.advanceTimersByTime(5_000);
			expect(cloudWorkerStartingWaits(await ask(startingAnswer()))).toBe(true);
			expect(cloudWorkerStartingRemaining("org", "s")).toBe(5_000);
		}
		vi.advanceTimersByTime(5_000);
		const late = await ask(startingAnswer());
		expect(cloudWorkerStartingWaits(late)).toBe(false);
		// Past the budget the session's other reads are not held back either.
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
	});

	it("reads timeouts as a starting worker only three times in a row, then they fail as before", async () => {
		for (let answer = 1; answer <= MAX_STARTING_TIMEOUTS; answer++) {
			expect(cloudWorkerStartingWaits(await ask(timeouts[answer - 1]()))).toBe(true);
			vi.advanceTimersByTime(cloudWorkerStartingRemaining("org", "s"));
		}
		const fourth = await ask(timeouts[0]());
		expect(cloudWorkerStartingWaits(fourth)).toBe(false);
		expect(cloudWorkerStartingWait("org", "s")).toBe(false);
		// The control plane saying the worker is starting is still waited out.
		expect(cloudWorkerStartingWaits(await ask(startingAnswer()))).toBe(true);
		// An answer starts the count again.
		vi.advanceTimersByTime(cloudWorkerStartingRemaining("org", "s"));
		await cloudWorkerRequest("org", "s", () => Promise.resolve({ ok: true }));
		expect(cloudWorkerStartingWaits(await ask(timeouts[1]()))).toBe(true);
	});

	it("starts a new wait for a starting answer long after the last one", async () => {
		expect(cloudWorkerStartingWaits(await ask(startingAnswer()))).toBe(true);
		vi.advanceTimersByTime(MAX_STARTING_WAIT_MS - 60_000);
		// Nothing was asked for 29 minutes, so this answer starts a new wait...
		expect(cloudWorkerStartingWaits(await ask(startingAnswer()))).toBe(true);
		vi.advanceTimersByTime(4 * 60_000);
		// ...and 33 minutes after the first answer it is still waited out.
		expect(cloudWorkerStartingWaits(await ask(startingAnswer()))).toBe(true);
	});

	it("gives a read retry options that wait out a starting answer and leave other errors to the read's own rule", async () => {
		const ownRetry = vi.fn((failureCount: number) => failureCount < 1);
		const options = cloudWorkerReadRetry("org", "s", ownRetry);
		const waiting = await ask(startingAnswer());
		expect(options.retry(40, waiting as Error)).toBe(true);
		expect(options.retryDelay(40, waiting as Error)).toBe(5_000);
		expect(ownRetry).not.toHaveBeenCalled();
		const failure = new CloudCpError("bad", { status: 400 });
		expect(options.retry(0, failure)).toBe(true);
		expect(options.retry(1, failure)).toBe(false);
		expect(options.retryDelay(1, failure)).toBe(2_000);
	});
});
