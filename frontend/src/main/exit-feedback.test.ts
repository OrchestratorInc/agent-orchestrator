import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	EXIT_FEEDBACK_BACKSTOP_MS,
	EXIT_FEEDBACK_DRAW_TIMEOUT_MS,
	EXIT_FEEDBACK_FLUSH_MS,
	EXIT_FEEDBACK_SECOND_CLOSE_MS,
	createExitFeedbackFacts,
	createExitFeedbackGate,
	createExitFeedbackSender,
	decideExitFeedback,
	parseExitFeedbackOverride,
	validateExitFeedbackEvent,
	type ExitFeedbackFacts,
} from "./exit-feedback";

const NOW = Date.parse("2026-11-27T12:00:00Z");
const HOUR = 3_600_000;
const eligible: ExitFeedbackFacts = { installCreatedMs: NOW - HOUR, workSessions: 0, projectCount: 0, markerExists: false };
const context = { nowMs: NOW, launchedAtMs: NOW - 60_000, analyticsOn: true, override: { force: false } };

describe("decideExitFeedback", () => {
	it("shows version A with no project and version B with one", () => {
		expect(decideExitFeedback(eligible, context)).toBe("no_project");
		expect(decideExitFeedback({ ...eligible, projectCount: 2 }, context)).toBe("project_no_work");
	});

	it.each([
		["analytics off", eligible, { analyticsOn: false }],
		["work started", { ...eligible, workSessions: 1 }, {}],
		["daemon unreachable", { ...eligible, workSessions: undefined }, {}],
		["project count unknown", { ...eligible, projectCount: undefined }, {}],
		["already shown", { ...eligible, markerExists: true }, {}],
		["past the first day", { ...eligible, installCreatedMs: NOW - 24 * HOUR }, {}],
		["install time unknown", { ...eligible, installCreatedMs: undefined }, {}],
		["install time in the future", { ...eligible, installCreatedMs: NOW + HOUR }, {}],
		["open under 10 seconds", eligible, { launchedAtMs: NOW - 9_999 }],
	] as const)("does not show when %s", (_name, facts, overrides) => {
		expect(decideExitFeedback(facts, { ...context, ...overrides })).toBeNull();
	});

	it("lets the override skip the marker, first-day and 10 second rules only", () => {
		const force = { ...context, launchedAtMs: NOW, override: { force: true } };
		const stale = { ...eligible, markerExists: true, installCreatedMs: NOW - 30 * 24 * HOUR };
		expect(decideExitFeedback(stale, force)).toBe("no_project");
		expect(decideExitFeedback(stale, { ...force, analyticsOn: false })).toBeNull();
		expect(decideExitFeedback({ ...stale, workSessions: 3 }, force)).toBeNull();
		expect(decideExitFeedback({ ...stale, workSessions: undefined }, force)).toBeNull();
		expect(decideExitFeedback({ ...stale, projectCount: undefined }, { ...force, override: parseExitFeedbackOverride("b") })).toBe("project_no_work");
		expect(decideExitFeedback({ ...stale, projectCount: 4 }, { ...force, override: parseExitFeedbackOverride("a") })).toBe("no_project");
	});

	it("parses the override variable", () => {
		expect(parseExitFeedbackOverride(undefined)).toEqual({ force: false });
		expect(parseExitFeedbackOverride("0")).toEqual({ force: false });
		expect(parseExitFeedbackOverride("1")).toEqual({ force: true });
		expect(parseExitFeedbackOverride(" A ")).toEqual({ force: true, version: "no_project" });
		expect(parseExitFeedbackOverride("project_no_work")).toEqual({ force: true, version: "project_no_work" });
	});
});

describe("createExitFeedbackFacts", () => {
	it("keeps the facts ready and stops polling once work has started", async () => {
		let work = 0;
		const clearInterval = vi.fn();
		const facts = createExitFeedbackFacts({
			readInstallCreatedMs: async () => NOW - HOUR,
			markerExists: async () => false,
			readDaemon: async () => ({ workSessions: work, projectCount: 1 }),
			now: () => NOW,
			override: { force: false },
			setInterval: () => ({}),
			clearInterval,
		});
		facts.start();
		await facts.refresh();
		expect(facts.get()).toEqual({ installCreatedMs: NOW - HOUR, markerExists: false, workSessions: 0, projectCount: 1 });
		work = 1;
		await facts.refresh();
		expect(facts.get().workSessions).toBe(1);
		expect(clearInterval).toHaveBeenCalledTimes(1);
	});

	it("does not publish a zero read before a session was created", async () => {
		let answer!: (facts: { workSessions: number; projectCount: number }) => void;
		const readDaemon = vi.fn(() => new Promise<{ workSessions: number; projectCount: number }>((resolve) => { answer = resolve; }));
		const facts = createExitFeedbackFacts({
			readInstallCreatedMs: async () => NOW - HOUR,
			markerExists: async () => false,
			readDaemon,
			now: () => NOW,
			override: { force: false },
			setInterval: () => ({}),
			clearInterval: () => undefined,
		});
		const poll = facts.refresh();
		await Promise.resolve();
		// A task is created while the poll still waits on the daemon.
		const afterCreate = facts.refresh(true);
		expect(facts.get().workSessions).toBeUndefined();
		answer({ workSessions: 0, projectCount: 1 });
		await vi.waitFor(() => expect(readDaemon).toHaveBeenCalledTimes(2));
		expect(facts.get().workSessions).toBeUndefined();
		answer({ workSessions: 1, projectCount: 1 });
		await Promise.all([poll, afterCreate]);
		expect(facts.get().workSessions).toBe(1);
	});

	it("reads a failed daemon check as unknown, never as no work", async () => {
		const facts = createExitFeedbackFacts({
			readInstallCreatedMs: async () => NOW - HOUR,
			markerExists: async () => false,
			readDaemon: async () => { throw new Error("daemon down"); },
			now: () => NOW,
			override: { force: false },
			setInterval: () => ({}),
			clearInterval: () => undefined,
		});
		await facts.refresh();
		expect(facts.get().workSessions).toBeUndefined();
		expect(decideExitFeedback(facts.get(), context)).toBeNull();
	});
});

describe("createExitFeedbackGate", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	function setup(overrides: Partial<Parameters<typeof createExitFeedbackGate>[0]> = {}) {
		const deps = {
			decide: vi.fn(() => "no_project" as const),
			request: vi.fn(() => true),
			markShown: vi.fn(),
			notifySecondClose: vi.fn(),
			flush: vi.fn(async () => undefined),
			setTimeout: (fn: () => void, ms: number) => setTimeout(fn, ms),
			clearTimeout: (handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>),
			...overrides,
		};
		const gate = createExitFeedbackGate(deps);
		const quit = vi.fn();
		return { gate, deps, quit };
	}

	it("holds the quit, writes the marker on show, and quits after delivery", async () => {
		const { gate, deps, quit } = setup();
		expect(gate.intercept(quit)).toBe(true);
		expect(deps.request).toHaveBeenCalledWith("no_project");
		expect(deps.markShown).not.toHaveBeenCalled();
		gate.rendererState("shown");
		expect(deps.markShown).toHaveBeenCalledTimes(1);
		expect(gate.accepting()).toBe(true);
		gate.rendererState("done");
		await vi.advanceTimersByTimeAsync(0);
		expect(deps.flush).toHaveBeenCalledWith(EXIT_FEEDBACK_FLUSH_MS);
		expect(quit).toHaveBeenCalledTimes(1);
		// The resumed quit, and every later one, goes straight through.
		expect(gate.intercept(quit)).toBe(false);
		expect(deps.request).toHaveBeenCalledTimes(1);
	});

	it("lets the quit through when the install is not eligible, and never asks later in that quit", () => {
		const decide = vi.fn<() => "no_project" | null>(() => null);
		const { gate, deps, quit } = setup({ decide });
		expect(gate.intercept(quit)).toBe(false);
		decide.mockReturnValue("no_project");
		expect(gate.intercept(quit)).toBe(false);
		expect(deps.request).not.toHaveBeenCalled();
	});

	it.each([
		["the check throws", { decide: () => { throw new Error("boom"); } }],
		["the shell cannot be reached", { request: () => false }],
		["the request throws", { request: () => { throw new Error("gone"); } }],
	])("lets the quit through when %s", (_name, overrides) => {
		const { gate, quit } = setup(overrides as never);
		expect(gate.intercept(quit)).toBe(false);
	});

	it("quits when the dialog is never drawn", async () => {
		const { gate, deps, quit } = setup();
		gate.intercept(quit);
		await vi.advanceTimersByTimeAsync(EXIT_FEEDBACK_DRAW_TIMEOUT_MS);
		expect(quit).toHaveBeenCalledTimes(1);
		expect(deps.markShown).not.toHaveBeenCalled();
	});

	it("quits when the renderer declines (analytics off or a kill switch)", async () => {
		const { gate, quit } = setup();
		gate.intercept(quit);
		gate.rendererState("declined");
		await vi.advanceTimersByTimeAsync(0);
		expect(quit).toHaveBeenCalledTimes(1);
	});

	it("never holds the quit past the backstop unless the user is typing", async () => {
		const { gate, quit } = setup();
		gate.intercept(quit);
		gate.rendererState("shown");
		await vi.advanceTimersByTimeAsync(EXIT_FEEDBACK_BACKSTOP_MS);
		expect(quit).toHaveBeenCalledTimes(1);

		const typing = setup();
		typing.gate.intercept(typing.quit);
		typing.gate.rendererState("shown");
		typing.gate.rendererState("typing");
		await vi.advanceTimersByTimeAsync(10 * EXIT_FEEDBACK_BACKSTOP_MS);
		expect(typing.quit).not.toHaveBeenCalled();
		typing.gate.rendererState("done");
		await vi.advanceTimersByTimeAsync(0);
		expect(typing.quit).toHaveBeenCalledTimes(1);
	});

	it("asks the renderer to record a second close and quits at once", async () => {
		const { gate, deps, quit } = setup();
		gate.intercept(quit);
		gate.rendererState("shown");
		gate.rendererState("typing");
		const second = vi.fn();
		expect(gate.intercept(second)).toBe(true);
		expect(deps.notifySecondClose).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(EXIT_FEEDBACK_SECOND_CLOSE_MS);
		expect(second).toHaveBeenCalledTimes(1);
		expect(quit).not.toHaveBeenCalled();
	});

	it("still quits when delivery fails", async () => {
		const { gate, quit } = setup({ flush: async () => { throw new Error("offline"); } });
		gate.intercept(quit);
		gate.rendererState("shown");
		gate.rendererState("done");
		await vi.advanceTimersByTimeAsync(0);
		expect(quit).toHaveBeenCalledTimes(1);
	});

	it("still quits when the marker cannot be written", async () => {
		const { gate, quit } = setup({ markShown: () => { throw new Error("EACCES"); } });
		gate.intercept(quit);
		gate.rendererState("shown");
		gate.rendererState("done");
		await vi.advanceTimersByTimeAsync(0);
		expect(quit).toHaveBeenCalledTimes(1);
	});

	it("lets a forced quit through at once while the events are still flushing", async () => {
		const { gate, quit } = setup({ flush: () => new Promise<void>(() => undefined) });
		gate.intercept(quit);
		gate.rendererState("shown");
		gate.rendererState("done");
		await vi.advanceTimersByTimeAsync(0);
		expect(gate.phase()).toBe("closing");
		gate.bypass();
		expect(quit).toHaveBeenCalledTimes(1);
		expect(gate.intercept(vi.fn())).toBe(false);
	});

	it("never holds an update restart, shutdown or forced quit", async () => {
		const { gate, deps, quit } = setup();
		gate.bypass();
		expect(gate.intercept(quit)).toBe(false);
		expect(deps.request).not.toHaveBeenCalled();

		const open = setup();
		open.gate.intercept(open.quit);
		open.gate.rendererState("shown");
		open.gate.bypass();
		await vi.advanceTimersByTimeAsync(0);
		expect(open.quit).toHaveBeenCalledTimes(1);
	});

	it("quits when the renderer goes away while the dialog is open", async () => {
		const { gate, quit } = setup();
		gate.intercept(quit);
		gate.rendererState("shown");
		gate.abort();
		await vi.advanceTimersByTimeAsync(0);
		expect(quit).toHaveBeenCalledTimes(1);
	});
});

describe("createExitFeedbackSender", () => {
	const KEY = "phc_test";
	const event = (overrides: Record<string, unknown> = {}) => ({
		uuid: "0193",
		event: "ao.exit_feedback.answered",
		properties: { token: KEY, distinct_id: "ins_1", version: "no_project", answer: "just_looking", has_text: false, seconds_open: 3 },
		timestamp: "2026-11-27T12:00:00.000Z",
		...overrides,
	});

	it("accepts only the three exit feedback events for this project", () => {
		expect(validateExitFeedbackEvent(event(), KEY)).not.toBeNull();
		expect(validateExitFeedbackEvent(event({ event: "ao.app.loaded" }), KEY)).toBeNull();
		expect(validateExitFeedbackEvent(event({ properties: { token: "phc_other" } }), KEY)).toBeNull();
		const huge = Object.fromEntries(Array.from({ length: 200 }, (_, i) => [`$k${i}`, "x".repeat(256)]));
		expect(validateExitFeedbackEvent(event({ properties: { ...event().properties, ...huge } }), KEY)).toBeNull();
		expect(validateExitFeedbackEvent(event({ timestamp: {} }), KEY)).toBeNull();
		expect(validateExitFeedbackEvent(event({ timestamp: "yesterday" }), KEY)).toBeNull();
		expect(validateExitFeedbackEvent("ao.exit_feedback.shown", KEY)).toBeNull();
	});

	it("rebuilds the event from an allowlist and never trusts the renderer's cleanup", () => {
		const out = validateExitFeedbackEvent(event({
			properties: {
				token: KEY, distinct_id: "ins_1", install_id: "ins_1", app_version: "1.2.3", $os: "Mac OS X",
				version: "no_project", answer: "not_a_fit", has_text: true, seconds_open: 3.9,
				text: "lost C:/work/private-repo/file.txt and /Users/alex/x",
				extra: "rides along", $nested: { path: "/Users/alex" }, how: "skip",
			},
			surprise: "top-level",
		}), KEY)!;
		expect(out).toEqual({
			event: "ao.exit_feedback.answered",
			uuid: "0193",
			timestamp: "2026-11-27T12:00:00.000Z",
			properties: {
				token: KEY, distinct_id: "ins_1", install_id: "ins_1", app_version: "1.2.3", $os: "Mac OS X",
				// The text is cleaned again here; how belongs to dismissals only.
				version: "no_project", answer: "not_a_fit", has_text: true, seconds_open: 3,
				text: "lost [redacted-local-path] and [redacted-local-path]",
			},
		});
	});

	it("drops an answered event whose answer is not one of its version's", () => {
		const properties = { token: KEY, distinct_id: "ins_1", version: "no_project", answer: "not_comfortable", text: "orphaned" };
		expect(validateExitFeedbackEvent(event({ properties }), KEY)).toBeNull();
	});

	it("posts the event from main and waits for it before the quit goes on", async () => {
		let deliver!: () => void;
		const fetch = vi.fn((_url: string | URL | Request, _init?: RequestInit) => new Promise<Response>((resolve) => {
			deliver = () => resolve(new Response("{}", { status: 200 }));
		}));
		const sender = createExitFeedbackSender({ host: "https://ph.test/", projectKey: KEY, fetch: fetch as typeof globalThis.fetch });
		expect(sender.send(event())).toBe(true);
		const [url, init] = fetch.mock.calls[0]!;
		expect(url).toBe("https://ph.test/e/");
		const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
		expect(body.event).toBe("ao.exit_feedback.answered");
		expect(new Date(body.timestamp as string).toISOString()).toBe(body.timestamp);

		let flushed = false;
		const flush = sender.flush(EXIT_FEEDBACK_FLUSH_MS).then(() => { flushed = true; });
		await Promise.resolve();
		expect(flushed).toBe(false);
		deliver();
		await flush;
		expect(flushed).toBe(true);
	});

	it("also waits for an event admitted while the flush is already waiting", async () => {
		const deliveries: Array<() => void> = [];
		const fetch = vi.fn(() => new Promise<Response>((resolve) => deliveries.push(() => resolve(new Response("{}")))));
		const sender = createExitFeedbackSender({ host: "https://ph.test", projectKey: KEY, fetch: fetch as unknown as typeof globalThis.fetch });
		sender.send(event({ event: "ao.exit_feedback.shown" }));
		let flushed = false;
		const flush = sender.flush(EXIT_FEEDBACK_FLUSH_MS).then(() => { flushed = true; });
		sender.send(event({ event: "ao.exit_feedback.dismissed", properties: { token: KEY, distinct_id: "ins_1", version: "no_project", how: "second_close", seconds_open: 1 } }));
		deliveries[0]!();
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(flushed).toBe(false);
		deliveries[1]!();
		await flush;
		expect(flushed).toBe(true);
	});

	it("caps the wait at about 2 seconds when PostHog does not answer", async () => {
		vi.useFakeTimers();
		try {
			const sender = createExitFeedbackSender({ host: "https://ph.test", projectKey: KEY, fetch: (() => new Promise<Response>(() => undefined)) as typeof fetch });
			sender.send(event());
			let flushed = false;
			void sender.flush(EXIT_FEEDBACK_FLUSH_MS).then(() => { flushed = true; });
			await vi.advanceTimersByTimeAsync(EXIT_FEEDBACK_FLUSH_MS - 1);
			expect(flushed).toBe(false);
			await vi.advanceTimersByTimeAsync(1);
			expect(flushed).toBe(true);
		} finally {
			vi.useRealTimers();
		}
	});

	it("drops anything else without a request", () => {
		const fetch = vi.fn();
		const sender = createExitFeedbackSender({ host: "https://ph.test", projectKey: KEY, fetch: fetch as unknown as typeof globalThis.fetch });
		expect(sender.send(event({ event: "ao.app.active" }))).toBe(false);
		expect(fetch).not.toHaveBeenCalled();
	});
});
