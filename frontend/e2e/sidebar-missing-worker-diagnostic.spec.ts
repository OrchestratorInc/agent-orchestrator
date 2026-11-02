import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent, type FakeWorker } from "./support/fake-bridge";

// Opt-in diagnostics only: this fake bridge exercises renderer cache, event,
// and polling paths. It cannot reproduce Electron background throttling, is
// excluded from the default @T0/@P0 renderer CI gate, and is skipped by a plain
// `playwright test` unless AO_SIDEBAR_DIAG=1.
test.skip(process.env.AO_SIDEBAR_DIAG !== "1", "Opt-in #5038 diagnostics: set AO_SIDEBAR_DIAG=1 to run them");

type Call = (...args: unknown[]) => unknown;
// The parts of the dev build's QueryClient (`window.__qc`) these diagnostics use.
type FakeQc = {
	getQueryData: (key: readonly unknown[]) => unknown;
	getQueryState: (
		key: readonly unknown[],
	) => { status?: string; fetchStatus?: string; fetchFailureCount?: number } | undefined;
	getQueryCache: () => { onFocus: () => void };
	invalidateQueries: Call;
	refetchQueries: Call;
	fetchQuery: Call;
};

declare global {
	interface Window {
		__qc?: FakeQc;
		__workspaceFetchLog?: WorkspaceFetchLog;
		/** True while a setInterval callback runs (see markIntervalCallbacks). */
		__aoInIntervalCallback?: boolean;
	}
}

const sessionList = (page: Page) => page.locator('[data-testid="session-list-fake-proj"]');

const cacheState = (page: Page) =>
	page.evaluate(() => {
		const workspaces = window.__qc?.getQueryData(["workspaces"]) as
			| Array<{ id: string; sessions: Array<{ id: string; title?: string }> }>
			| undefined;
		const workspace = workspaces?.find((item) => item.id === "fake-proj");
		return {
			workspaceCount: workspaces?.length ?? 0,
			sessionCount: workspace?.sessions.length ?? 0,
			sessionIds: workspace?.sessions.map((session) => session.id) ?? [],
		};
	});

const sidebarRowState = (page: Page, title: string) =>
	page.evaluate((sessionTitle) => {
		const rows = [...document.querySelectorAll('[data-testid="session-list-fake-proj"] [data-session-row]')];
		const row = rows.find((candidate) => candidate.textContent?.includes(sessionTitle));
		if (!row) return { mounted: false, visible: false, opacity: null, display: null, rect: null };
		const style = getComputedStyle(row);
		const rect = row.getBoundingClientRect();
		return {
			mounted: true,
			visible: rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden",
			opacity: style.opacity,
			display: style.display,
			rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
		};
	}, title);

// TanStack schedules refetchInterval through the global setInterval (its
// timeoutManager looks it up at call time) and the tick runs the queryFn
// synchronously, so marking interval callbacks identifies poll fetches
// positively. Installed before the app loads so the poll's first timer is
// covered too.
const markIntervalCallbacks = (page: Page) =>
	page.addInitScript(() => {
		const setIntervalNative = window.setInterval.bind(window);
		window.setInterval = ((handler: TimerHandler, timeout?: number, ...args: unknown[]) => {
			if (typeof handler !== "function") return setIntervalNative(handler, timeout, ...args);
			const marked = (...callArgs: unknown[]) => {
				const outer = window.__aoInIntervalCallback;
				window.__aoInIntervalCallback = true;
				try {
					(handler as Call)(...callArgs);
				} finally {
					window.__aoInIntervalCallback = outer;
				}
			};
			return setIntervalNative(marked, timeout, ...args);
		}) as typeof window.setInterval;
	});

async function openLargeExpandedWorkspace(page: Page) {
	const workers: FakeWorker[] = Array.from({ length: 491 }, (_, index) => {
		const number = index + 1;
		return {
			id: `seed-${String(number).padStart(3, "0")}`,
			title: number === 443 ? "Index 443 target worker" : `Seed worker ${number}`,
			createdAt: new Date(Date.UTC(2024, 0, 1) + number * 1_000).toISOString(),
		};
	});
	await markIntervalCallbacks(page);
	await installFakeAgent(page, { workers, expandProject: true });
	await page.goto("/#/");
	const projectRow = page.locator('[data-sidebar="menu-button"]').filter({ hasText: "fake-proj" }).first();
	await expect(projectRow).toBeVisible();
	await projectRow.click();
	await expect.poll(async () => (await cacheState(page)).sessionCount, { timeout: 20_000 }).toBe(492);
	await expect(page.locator('[data-project-folder]')).toHaveAttribute("aria-expanded", "true");
}

const queryMetrics = (page: Page) =>
	page.evaluate(() => {
		const state = window.__qc?.getQueryState(["workspaces"]);
		return {
			queryStatus: state?.status ?? "missing",
			fetchStatus: state?.fetchStatus ?? "missing",
			fetchFailureCount: state?.fetchFailureCount ?? 0,
		};
	});

const replayProgress = (page: Page) => page.evaluate(() => window.__aoFakeAgent!.replayProgress());
const eventStreams = (page: Page) => page.evaluate(() => window.__aoFakeAgent!.eventStreams());

// A browser fires visibilitychange at the document and it bubbles to window:
// app hooks listen on document, TanStack Query's focusManager on window.
const setDocumentVisibility = (page: Page, visibility: "visible" | "hidden") =>
	page.evaluate((nextVisibility) => {
		Object.defineProperty(document, "visibilityState", { configurable: true, get: () => nextVisibility });
		Object.defineProperty(document, "hidden", { configurable: true, get: () => nextVisibility === "hidden" });
		document.dispatchEvent(new Event("visibilitychange", { bubbles: true }));
	}, visibility);

type FetchTrigger = "invalidate" | "refetch" | "fetchQuery" | "focus" | "interval" | "retry" | "other";
type WorkspaceFetchLog = {
	fetches: Record<FetchTrigger, number>;
	/** The trigger of the first workspace fetch whose snapshot held each new session. */
	firstFetchedBy: Record<string, FetchTrigger>;
	/** `Date.now()` of that fetch: the session enters the cache when it resolves. */
	firstFetchedAt: Record<string, number>;
	workspaceInvalidations: number;
	/** `Date.now()` of the latest workspace invalidation, or 0. */
	lastWorkspaceInvalidationAt: number;
};

// Attributes each workspace snapshot read to what started it. TanStack runs the
// queryFn synchronously inside the call that starts a fetch, so a read inside
// invalidateQueries (CDC events, the stream's open catch-up), refetchQueries
// (the stream's error health check), fetchQuery (route loaders) or the query
// cache's focus fan-out belongs to that entry point, and a read inside a
// setInterval callback is the poll. A retry is the only other read made while
// the query records a failure, since every new fetch resets fetchFailureCount
// first. Anything left, such as a newly mounted observer's refetchOnMount, is
// `other`. Every wrapper forwards all arguments; event-transport relies on
// `{ cancelRefetch: false }`.
const instrumentWorkspaceFetches = (page: Page) =>
	page.evaluate(() => {
		const client = window.__qc!;
		const log: WorkspaceFetchLog = {
			fetches: { invalidate: 0, refetch: 0, fetchQuery: 0, focus: 0, interval: 0, retry: 0, other: 0 },
			firstFetchedBy: {},
			firstFetchedAt: {},
			workspaceInvalidations: 0,
			lastWorkspaceInvalidationAt: 0,
		};
		let trigger: "invalidate" | "refetch" | "fetchQuery" | "focus" | undefined;
		const attribute = (name: NonNullable<typeof trigger>, call: Call): Call => (...args) => {
			if (trigger) return call(...args);
			trigger = name;
			try {
				return call(...args);
			} finally {
				trigger = undefined;
			}
		};
		const invalidate = attribute("invalidate", client.invalidateQueries.bind(client));
		client.invalidateQueries = (...args) => {
			if ((args[0] as { queryKey?: readonly unknown[] } | undefined)?.queryKey?.[0] === "workspaces") {
				log.workspaceInvalidations += 1;
				log.lastWorkspaceInvalidationAt = Date.now();
			}
			return invalidate(...args);
		};
		client.refetchQueries = attribute("refetch", client.refetchQueries.bind(client));
		// prefetchQuery and ensureQueryData call through this.fetchQuery.
		client.fetchQuery = attribute("fetchQuery", client.fetchQuery.bind(client));
		const cache = client.getQueryCache();
		cache.onFocus = attribute("focus", cache.onFocus.bind(cache));

		type Snapshot = Array<{ sessions: Array<{ id: string }> }>;
		const seen = new Set(
			((client.getQueryData(["workspaces"]) ?? []) as Snapshot).flatMap((workspace) => workspace.sessions.map((session) => session.id)),
		);
		const fake = window.__aoFakeAgent!;
		const snapshot = fake.snapshot;
		fake.snapshot = () => {
			const failed = (client.getQueryState(["workspaces"])?.fetchFailureCount ?? 0) > 0;
			const source: FetchTrigger = trigger ?? (window.__aoInIntervalCallback ? "interval" : failed ? "retry" : "other");
			log.fetches[source] += 1;
			const result = snapshot() as Snapshot;
			for (const session of result.flatMap((workspace) => workspace.sessions)) {
				if (seen.has(session.id)) continue;
				seen.add(session.id);
				log.firstFetchedBy[session.id] = source;
				log.firstFetchedAt[session.id] = Date.now();
			}
			return result;
		};
		window.__workspaceFetchLog = log;
	});

const workspaceFetchLog = (page: Page) =>
	page.evaluate(() => {
		const log = window.__workspaceFetchLog!;
		return { ...log, fetches: { ...log.fetches }, firstFetchedBy: { ...log.firstFetchedBy }, firstFetchedAt: { ...log.firstFetchedAt } };
	});

// The fake fires `open` from a timer, and the app's catch-up refresh can trail
// it by an invalidation window. Wait until the newest CDC stream has opened and
// a workspace invalidation since then has finished fetching.
const waitForStreamCatchUp = (page: Page) =>
	expect
		.poll(
			() =>
				page.evaluate(() => {
					const openedAt = window.__aoFakeAgent!.eventStreams().at(-1)?.openedAt;
					return (
						openedAt != null &&
						window.__workspaceFetchLog!.lastWorkspaceInvalidationAt >= openedAt &&
						window.__qc!.getQueryState(["workspaces"])?.fetchStatus === "idle"
					);
				}),
			{ timeout: 15_000 },
		)
		.toBe(true);

const fetchesSince = (before: WorkspaceFetchLog, after: WorkspaceFetchLog) =>
	Object.fromEntries(
		Object.entries(after.fetches).map(([source, count]) => [source, count - before.fetches[source as FetchTrigger]]),
	) as Record<FetchTrigger, number>;

test("diagnostic: large workspace snapshot, refetch race, and SSE replay keep the worker row in sync @SIDEBAR_DIAG", async ({ page }) => {
	test.setTimeout(240_000);
	page.on("pageerror", (error) => console.log("SIDEBAR_DIAGNOSTIC pageerror", error.message));
	await openLargeExpandedWorkspace(page);
	await instrumentWorkspaceFetches(page);

	const initialCache = await cacheState(page);
	expect(initialCache.sessionIds[443]).toBe("seed-443");
	// A project lists its 6 most recent agents until Show more lifts the cap.
	const sessionRows = sessionList(page).locator("[data-session-row]");
	await expect(sessionRows).toHaveCount(6);
	await page.getByRole("button", { name: "Show 485 more agents" }).click();
	await expect(sessionRows).toHaveCount(491);
	await expect(sessionList(page).getByText("Index 443 target worker", { exact: true })).toBeVisible();
	// Return to the default cap: the phases below time cache refreshes, which a
	// dev-build rerender of 491 rows would otherwise dominate. New workers sort first.
	await page.getByRole("button", { name: "Show fewer agents" }).click();
	await expect(sessionRows).toHaveCount(6);

	// Arrange a new-worker event to arrive from inside the next fetchWorkspaces
	// snapshot. The returned snapshot is deliberately pre-creation, so the event
	// transport sees an event while the workspace query is fetching.
	await page.evaluate(() => {
		window.__aoFakeAgent!.createWorkerDuringNextSnapshot({ id: "race-worker", title: "Race worker" });
		void window.__qc!.invalidateQueries({ queryKey: ["workspaces"] });
	});
	await expect.poll(async () => (await cacheState(page)).sessionIds.includes("race-worker"), { timeout: 10_000 }).toBe(true);
	await expect(sessionList(page).getByText("Race worker", { exact: true })).toBeVisible();
	const raceCache = await cacheState(page);
	const raceCapture = {
		cacheSessionCount: raceCache.sessionCount,
		workerIndex: raceCache.sessionIds.indexOf("race-worker"),
		firstFetchedBy: (await workspaceFetchLog(page)).firstFetchedBy["race-worker"],
		row: await sidebarRowState(page, "Race worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC race", JSON.stringify(raceCapture));

	// Drop the stream so EventSource reconnects with Last-Event-ID, the only path
	// on which the daemon replays a gap. 378k frames matches the backlog measured
	// for #5038; a worker created during the replay has its event queued behind it.
	await page.evaluate(() => window.__aoFakeAgent!.reconnectEventsWithReplayBacklog(378_000));
	await expect.poll(async () => (await replayProgress(page)).phase, { timeout: 15_000 }).toBe("replaying");
	const logAtReplayStart = await workspaceFetchLog(page);
	// While the stream replays, the fake queues new events behind the gap.
	await page.evaluate(() => window.__aoFakeAgent!.createWorker({ id: "replay-worker", title: "Replay worker" }));
	const replayCacheAtCapture = await cacheState(page);
	const duringReplay = {
		cacheSessionCount: replayCacheAtCapture.sessionCount,
		workerIndex: replayCacheAtCapture.sessionIds.indexOf("replay-worker"),
		row: await sidebarRowState(page, "Replay worker"),
		replay: await replayProgress(page),
	};
	console.log("SIDEBAR_DIAGNOSTIC during-replay", JSON.stringify(duringReplay));
	// Replayed session_updated frames invalidate the workspace list as well, so the
	// worker can arrive long before its own event. Record how much of the gap remained.
	await expect
		.poll(async () => (await cacheState(page)).sessionIds.includes("replay-worker"), { timeout: 60_000, intervals: [100] })
		.toBe(true);
	const replayWhenCached = await replayProgress(page);
	const logWhenCached = await workspaceFetchLog(page);
	await expect(sessionList(page).getByText("Replay worker", { exact: true })).toBeVisible();
	// Draining the rest takes minutes in the dev build. End it as a failed reconnect
	// does: the app's replacement stream opens at `?after=latest`, so the rest of the
	// gap, the worker's own event included, is never delivered.
	const streamCountBeforeClose = (await eventStreams(page)).length;
	await page.evaluate(() => window.__aoFakeAgent!.closeEventsStream());
	await expect.poll(async () => (await replayProgress(page)).phase, { timeout: 15_000 }).toBe("idle");
	await expect.poll(async () => (await eventStreams(page)).length, { timeout: 45_000 }).toBe(streamCountBeforeClose + 1);
	const afterReplayCache = await cacheState(page);
	const afterReplay = {
		cacheSessionCount: afterReplayCache.sessionCount,
		workerIndex: afterReplayCache.sessionIds.indexOf("replay-worker"),
		firstFetchedBy: logWhenCached.firstFetchedBy["replay-worker"],
		fetchesDuringReplay: fetchesSince(logAtReplayStart, logWhenCached),
		replayWhenCached,
		replay: await replayProgress(page),
		streams: await eventStreams(page),
		row: await sidebarRowState(page, "Replay worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC after-replay", JSON.stringify(afterReplay));
	expect(afterReplay.replayWhenCached.phase).toBe("replaying");
	expect(afterReplay.fetchesDuringReplay.invalidate).toBeGreaterThan(0);
	expect(afterReplay.replay.abandoned).toBeGreaterThan(0);
	expect(afterReplay.streams.at(-1)?.url).toContain("/api/v1/events?after=latest");

	// Model the event being missed entirely while keeping the renderer mounted, so
	// only the 15-second poll can recover the worker. Create it only after the
	// replacement stream's open catch-up has run, or that fetch would find it.
	await waitForStreamCatchUp(page);
	const pollStartedAt = await page.evaluate(() => {
		window.__aoFakeAgent!.createWorkerWithoutEvent({ id: "poll-worker", title: "Poll worker" });
		return Date.now();
	});
	const pollCacheBefore = await cacheState(page);
	const beforePoll = {
		cacheSessionCount: pollCacheBefore.sessionCount,
		workerIndex: pollCacheBefore.sessionIds.indexOf("poll-worker"),
		row: await sidebarRowState(page, "Poll worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC before-poll", JSON.stringify(beforePoll));
	expect(beforePoll.workerIndex).toBe(-1);
	expect(beforePoll.row.mounted).toBe(false);
	await expect.poll(async () => (await cacheState(page)).sessionIds.includes("poll-worker"), { timeout: 30_000 }).toBe(true);
	await expect(sessionList(page).getByText("Poll worker", { exact: true })).toBeVisible();
	const pollCacheAfter = await cacheState(page);
	const logAfterPoll = await workspaceFetchLog(page);
	const afterPoll = {
		cacheSessionCount: pollCacheAfter.sessionCount,
		workerIndex: pollCacheAfter.sessionIds.indexOf("poll-worker"),
		firstFetchedBy: logAfterPoll.firstFetchedBy["poll-worker"],
		row: await sidebarRowState(page, "Poll worker"),
		missingMs: logAfterPoll.firstFetchedAt["poll-worker"] - pollStartedAt,
	};
	console.log("SIDEBAR_DIAGNOSTIC after-poll", JSON.stringify(afterPoll));
	expect(afterPoll.firstFetchedBy).toBe("interval");
	expect(afterPoll.row.mounted).toBe(true);
	expect(afterPoll.row.visible).toBe(true);
});

test("diagnostic: continuous SSE pressure does not starve workspace invalidation for two minutes @SIDEBAR_DIAG", async ({ page }) => {
	test.setTimeout(240_000);
	await openLargeExpandedWorkspace(page);
	await instrumentWorkspaceFetches(page);

	const before = await workspaceFetchLog(page);
	const startTime = await page.evaluate(() => Date.now());
	// The daemon sends 100 session_updated CDC envelopes per 50 ms (2,000/s). They
	// run through event-transport's real handlers, per-session invalidations
	// included. A backlog that builds while the main thread is busy is handed
	// over a tick's worth per task, so the renderer can fetch and paint between.
	await page.evaluate(() => window.__aoFakeAgent!.startContinuousWorkspaceEvents(50, 100));
	await page.waitForTimeout(1_000);
	// The worker's own session_created would queue behind the undelivered backlog,
	// so create it without one: only a later pressure frame's invalidation can
	// bring it into the cache.
	const workerCreatedAt = await page.evaluate(() => {
		window.__aoFakeAgent!.createWorkerWithoutEvent({ id: "continuous-worker", title: "Continuous worker" });
		return Date.now();
	});
	await page.waitForTimeout(120_000);
	await page.evaluate(() => window.__aoFakeAgent!.stopContinuousWorkspaceEvents());
	const elapsedMs = await page.evaluate((startedAt) => Date.now() - startedAt, startTime);
	const events = await page.evaluate(() => window.__aoFakeAgent!.continuousWorkspaceEvents());
	const after = await workspaceFetchLog(page);
	const capturedCache = await cacheState(page);
	const capture = {
		elapsedMs,
		sentEvents: events.sent,
		deliveredEvents: events.delivered,
		workspaceInvalidations: after.workspaceInvalidations - before.workspaceInvalidations,
		fetches: fetchesSince(before, after),
		firstFetchedBy: after.firstFetchedBy["continuous-worker"],
		cachedAfterCreateMs: after.firstFetchedAt["continuous-worker"] - workerCreatedAt,
		cacheSessionCount: capturedCache.sessionCount,
		workerIndex: capturedCache.sessionIds.indexOf("continuous-worker"),
		row: await sidebarRowState(page, "Continuous worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC continuous-pressure", JSON.stringify(capture));
	expect(elapsedMs).toBeGreaterThanOrEqual(120_000);
	expect(events.delivered).toBeGreaterThanOrEqual(5_000);
	expect(capture.workspaceInvalidations).toBeGreaterThan(0);
	expect(capture.fetches.invalidate).toBeGreaterThan(0);
	expect(capture.workerIndex).toBeGreaterThanOrEqual(0);
	expect(capture.row.mounted).toBe(true);
	expect(capture.row.visible).toBe(true);
});

test("diagnostic: exhausted snapshot retries do not stop the 15-second workspace poll @SIDEBAR_DIAG", async ({ page }) => {
	test.setTimeout(150_000);
	await openLargeExpandedWorkspace(page);
	await instrumentWorkspaceFetches(page);
	const baseline = await page.evaluate(() => window.__aoFakeAgent!.snapshotCallCount());
	const logBeforeFailure = await workspaceFetchLog(page);
	await page.evaluate(() => {
		window.__aoFakeAgent!.createWorkerWithoutEvent({ id: "failed-poll-worker", title: "Failed poll worker" });
		window.__aoFakeAgent!.setSnapshotFailures(2);
	});
	const failureStart = await page.evaluate(() => Date.now());
	await expect.poll(async () => (await page.evaluate(() => window.__aoFakeAgent!.snapshotCallCount())) - baseline, { timeout: 45_000 }).toBeGreaterThanOrEqual(2);
	await expect.poll(async () => (await queryMetrics(page)).queryStatus, { timeout: 5_000 }).toBe("error");
	const failedCapture = {
		elapsedMs: await page.evaluate((startedAt) => Date.now() - startedAt, failureStart),
		snapshotCalls: (await page.evaluate(() => window.__aoFakeAgent!.snapshotCallCount())) - baseline,
		fetches: fetchesSince(logBeforeFailure, await workspaceFetchLog(page)),
		query: await queryMetrics(page),
		workerIndex: (await cacheState(page)).sessionIds.indexOf("failed-poll-worker"),
		row: await sidebarRowState(page, "Failed poll worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC failed-poll", JSON.stringify(failedCapture));
	expect(failedCapture.fetches.retry).toBe(1);
	expect(failedCapture.workerIndex).toBe(-1);
	expect(failedCapture.row.mounted).toBe(false);

	const nextPollStart = await page.evaluate(() => Date.now());
	await expect.poll(async () => (await page.evaluate(() => window.__aoFakeAgent!.snapshotCallCount())) - baseline, { timeout: 45_000 }).toBeGreaterThan(failedCapture.snapshotCalls);
	await expect.poll(async () => (await cacheState(page)).sessionIds.includes("failed-poll-worker"), { timeout: 5_000 }).toBe(true);
	const recoveredCache = await cacheState(page);
	const recoveryCapture = {
		elapsedMs: await page.evaluate((startedAt) => Date.now() - startedAt, nextPollStart),
		snapshotCalls: (await page.evaluate(() => window.__aoFakeAgent!.snapshotCallCount())) - baseline,
		firstFetchedBy: (await workspaceFetchLog(page)).firstFetchedBy["failed-poll-worker"],
		query: await queryMetrics(page),
		cacheSessionCount: recoveredCache.sessionCount,
		workerIndex: recoveredCache.sessionIds.indexOf("failed-poll-worker"),
		row: await sidebarRowState(page, "Failed poll worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC poll-recovery", JSON.stringify(recoveryCapture));
	await expect(sessionList(page).getByText("Failed poll worker", { exact: true })).toBeVisible();
	expect(recoveryCapture.firstFetchedBy).toBe("interval");
});

test("diagnostic: a delivered SSE event updates the cache while document is hidden @SIDEBAR_DIAG", async ({ page }) => {
	test.setTimeout(165_000);
	await openLargeExpandedWorkspace(page);
	await instrumentWorkspaceFetches(page);
	const hiddenStartedAt = await page.evaluate(() => Date.now());
	await setDocumentVisibility(page, "hidden");
	await page.waitForTimeout(1_000);
	const logAtHiddenStart = await workspaceFetchLog(page);
	await page.evaluate(() => window.__aoFakeAgent!.createWorker({ id: "hidden-event-worker", title: "Hidden event worker" }));
	await expect.poll(async () => (await cacheState(page)).sessionIds.includes("hidden-event-worker"), { timeout: 5_000 }).toBe(true);
	const eventCache = await cacheState(page);
	const eventCapture = {
		cacheSessionCount: eventCache.sessionCount,
		workerIndex: eventCache.sessionIds.indexOf("hidden-event-worker"),
		firstFetchedBy: (await workspaceFetchLog(page)).firstFetchedBy["hidden-event-worker"],
		row: await sidebarRowState(page, "Hidden event worker"),
		visibility: await page.evaluate(() => document.visibilityState),
	};
	console.log("SIDEBAR_DIAGNOSTIC hidden-sse-event", JSON.stringify(eventCapture));
	expect(eventCapture.firstFetchedBy).toBe("invalidate");
	await page.waitForTimeout(120_000);
	const hiddenDurationMs = await page.evaluate((startedAt) => Date.now() - startedAt, hiddenStartedAt);
	const endHiddenCache = await cacheState(page);
	const hiddenCompleteCapture = {
		hiddenDurationMs,
		fetchesWhileHidden: fetchesSince(logAtHiddenStart, await workspaceFetchLog(page)),
		cacheSessionCount: endHiddenCache.sessionCount,
		workerIndex: endHiddenCache.sessionIds.indexOf("hidden-event-worker"),
		row: await sidebarRowState(page, "Hidden event worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC hidden-sse-event-after-two-minutes", JSON.stringify(hiddenCompleteCapture));
	expect(hiddenDurationMs).toBeGreaterThanOrEqual(120_000);
	expect(hiddenCompleteCapture.fetchesWhileHidden.interval).toBe(0);
	expect(hiddenCompleteCapture.workerIndex).toBeGreaterThanOrEqual(0);
	expect(hiddenCompleteCapture.row.mounted).toBe(true);
	await setDocumentVisibility(page, "visible");
});

test("diagnostic: a missed hidden SSE event waits for the next 15-second poll after visibility returns @SIDEBAR_DIAG", async ({ page }) => {
	test.setTimeout(180_000);
	await openLargeExpandedWorkspace(page);
	// The app's QueryClient defaults to refetchOnWindowFocus: false and the
	// workspace query keeps it, so returning visibility fetches nothing by itself:
	// the poll is the only recovery path. Measure how long the worker stays missing.
	await instrumentWorkspaceFetches(page);
	await setDocumentVisibility(page, "hidden");
	await page.waitForTimeout(1_000);
	const logAtHiddenStart = await workspaceFetchLog(page);
	const hiddenStartedAt = await page.evaluate(() => Date.now());
	await page.evaluate(() => window.__aoFakeAgent!.createWorkerWithoutEvent({ id: "hidden-missed-worker", title: "Hidden missed worker" }));
	await page.waitForTimeout(120_000);
	const cacheAfterTwoMinutesHidden = await cacheState(page);
	const hiddenCapture = {
		hiddenDurationMs: await page.evaluate((startedAt) => Date.now() - startedAt, hiddenStartedAt),
		fetchesWhileHidden: fetchesSince(logAtHiddenStart, await workspaceFetchLog(page)),
		cacheSessionCount: cacheAfterTwoMinutesHidden.sessionCount,
		workerIndex: cacheAfterTwoMinutesHidden.sessionIds.indexOf("hidden-missed-worker"),
		row: await sidebarRowState(page, "Hidden missed worker"),
		query: await queryMetrics(page),
	};
	console.log("SIDEBAR_DIAGNOSTIC hidden-missed-event", JSON.stringify(hiddenCapture));
	expect(hiddenCapture.hiddenDurationMs).toBeGreaterThanOrEqual(120_000);
	expect(Object.values(hiddenCapture.fetchesWhileHidden).every((count) => count === 0)).toBe(true);
	expect(hiddenCapture.workerIndex).toBe(-1);
	expect(hiddenCapture.row.mounted).toBe(false);

	const restoreStartedAt = await page.evaluate(() => Date.now());
	const logBeforeRestore = await workspaceFetchLog(page);
	await setDocumentVisibility(page, "visible");
	await expect.poll(async () => (await cacheState(page)).sessionIds.includes("hidden-missed-worker"), { timeout: 30_000 }).toBe(true);
	const recoveredCache = await cacheState(page);
	const logAfterRecovery = await workspaceFetchLog(page);
	const recoveryCapture = {
		missingAfterRestoreMs: logAfterRecovery.firstFetchedAt["hidden-missed-worker"] - restoreStartedAt,
		fetchesSinceRestore: fetchesSince(logBeforeRestore, logAfterRecovery),
		firstFetchedBy: logAfterRecovery.firstFetchedBy["hidden-missed-worker"],
		cacheSessionCount: recoveredCache.sessionCount,
		workerIndex: recoveredCache.sessionIds.indexOf("hidden-missed-worker"),
		row: await sidebarRowState(page, "Hidden missed worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC hidden-missed-event-recovery", JSON.stringify(recoveryCapture));
	await expect(sessionList(page).getByText("Hidden missed worker", { exact: true })).toBeVisible();
	// Nothing but the poll fetched the workspace list after visibility returned.
	expect(recoveryCapture.fetchesSinceRestore).toEqual({ invalidate: 0, refetch: 0, fetchQuery: 0, focus: 0, interval: 1, retry: 0, other: 0 });
	expect(recoveryCapture.firstFetchedBy).toBe("interval");
	// The poll keeps its phase while hidden, so the wait is up to one interval.
	expect(recoveryCapture.missingAfterRestoreMs).toBeLessThanOrEqual(15_000 + 1_000);
});

test("diagnostic: the app's replacement EventSource catches up a missed event on open @SIDEBAR_DIAG", async ({ page }) => {
	test.setTimeout(100_000);
	await openLargeExpandedWorkspace(page);
	await instrumentWorkspaceFetches(page);
	// While hidden TanStack does not poll (and the workspace query never refetches
	// on focus), so only the stream's own callbacks can fetch the workspace list.
	await setDocumentVisibility(page, "hidden");
	const streamsBeforeClose = await eventStreams(page);
	const logBeforeClose = await workspaceFetchLog(page);
	const closedAt = await page.evaluate(() => Date.now());
	// The error callback's health-check refetch reads the snapshot synchronously,
	// before the worker exists. The app then retries with a fresh `?after=latest`
	// stream, which starts at head: the worker's event is never delivered.
	await page.evaluate(() => window.__aoFakeAgent!.closeEventsStream());
	await page.evaluate(() => window.__aoFakeAgent!.createWorkerWithoutEvent({ id: "reopen-catchup-worker", title: "Reopen catch-up worker" }));
	expect((await cacheState(page)).sessionIds).not.toContain("reopen-catchup-worker");
	await expect
		.poll(async () => (await eventStreams(page)).length, { timeout: 45_000, intervals: [100] })
		.toBe(streamsBeforeClose.length + 1);
	const reopenedAfterCloseMs = await page.evaluate((startedAt) => Date.now() - startedAt, closedAt);
	await expect.poll(async () => (await cacheState(page)).sessionIds.includes("reopen-catchup-worker"), { timeout: 5_000, intervals: [100] }).toBe(true);
	const caughtUp = await cacheState(page);
	const logAfterCatchUp = await workspaceFetchLog(page);
	const capture = {
		reopenedAfterCloseMs,
		cachedAfterCloseMs: await page.evaluate((startedAt) => Date.now() - startedAt, closedAt),
		streams: await eventStreams(page),
		fetchesSinceClose: fetchesSince(logBeforeClose, logAfterCatchUp),
		firstFetchedBy: logAfterCatchUp.firstFetchedBy["reopen-catchup-worker"],
		cacheSessionCount: caughtUp.sessionCount,
		workerIndex: caughtUp.sessionIds.indexOf("reopen-catchup-worker"),
		row: await sidebarRowState(page, "Reopen catch-up worker"),
	};
	console.log("SIDEBAR_DIAGNOSTIC hidden-event-reopen-catchup", JSON.stringify(capture));
	await expect(sessionList(page).getByText("Reopen catch-up worker", { exact: true })).toBeVisible();
	expect(capture.streams).toHaveLength(streamsBeforeClose.length + 1);
	expect(capture.streams.at(-1)?.url).toContain("/api/v1/events?after=latest");
	expect(capture.fetchesSinceClose.refetch).toBe(1);
	expect(capture.firstFetchedBy).toBe("invalidate");
	await setDocumentVisibility(page, "visible");
});
