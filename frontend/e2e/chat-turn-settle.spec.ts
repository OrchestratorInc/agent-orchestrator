import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// A turn that finishes hands its live row (orb, step, timer) under the reply over to
// "Worked for" above it, and folds its steps away in the same commit. These run the
// real renderer in a browser, sample the turn's rows on every frame of that handover,
// and check that nothing jumps: the prompt and the scroll position hold still, the
// reply only ever moves up into the room its steps left, and the live row fades out
// where it was.

const sessionId = "turn-settle";
const at = "2026-11-05T10:00:00Z";
const replyText = [
	"I found the cause: the poller started before the fixture finished writing.",
	"",
	"- Waited for the fixture's ready event before starting the poller.",
	"- Added a regression test that writes slowly.",
].join("\n");

type Frame = { scrollTop: number; prompt: number; reply: number; live: number | null; worked: boolean };

function conversation(settled: boolean, withSteps: boolean, priorTurns: number) {
	const base = priorTurns * 2;
	const prior = Array.from({ length: priorTurns }, (_, index) => [
		{ kind: "message", id: `prior-user-${index}`, turnId: `prior-${index}`, sequence: index * 2 + 1, revision: 0, role: "user", origin: "human", text: "Look at the test runner config", streaming: false, createdAt: at },
		{ kind: "message", id: `prior-reply-${index}`, turnId: `prior-${index}`, sequence: index * 2 + 2, revision: 0, role: "assistant", origin: "provider", text: "The runner config is fine. ".repeat(30), streaming: false, createdAt: at },
	]).flat();
	const steps = withSteps
		? [
				{ kind: "activity", id: "run-1", turnId: "turn", sequence: base + 2, revision: 1, activityKind: "command", status: "completed", summary: "npm test -- poller", detail: { command: "npm test -- poller", exitCode: 1 }, createdAt: at },
				{ kind: "activity", id: "edit", turnId: "turn", sequence: base + 3, revision: 1, activityKind: "file_change", status: "completed", summary: "Edited poller.ts", detail: {}, createdAt: at },
				{ kind: "activity", id: "run-2", turnId: "turn", sequence: base + 4, revision: 1, activityKind: "command", status: "completed", summary: "npm test", detail: { command: "npm test", exitCode: 0 }, createdAt: at },
			]
		: [];
	return {
		conversationId: "conversation-turn-settle",
		sessionId,
		harness: "claude-code",
		mode: "chat",
		controller: settled ? "ready" : "busy",
		settings: {},
		oldestSequence: 1,
		latestSequence: base + (settled ? 6 : 5),
		hasMoreBefore: false,
		turns: [
			...Array.from({ length: priorTurns }, (_, index) => ({ id: `prior-${index}`, state: "completed", providerTurnId: `provider-prior-${index}`, requestedAt: at, startedAt: at, completedAt: "2026-11-05T10:00:20Z" })),
			settled
				? { id: "turn", state: "completed", providerTurnId: "provider-turn", requestedAt: at, startedAt: at, completedAt: "2026-11-05T10:00:42Z" }
				: { id: "turn", state: "running", providerTurnId: "provider-turn", requestedAt: at, startedAt: at },
		],
		messages: [
			...prior,
			{ kind: "message", id: "prompt", turnId: "turn", sequence: base + 1, revision: 0, role: "user", origin: "human", text: "Fix the flaky poller test", streaming: false, createdAt: at },
			{ kind: "message", id: "reply", turnId: "turn", sequence: base + 5, revision: 0, role: "assistant", origin: "provider", text: replyText, streaming: false, createdAt: at },
		],
		activities: steps,
	};
}

/** Serves a running turn until `settle()`, which finishes it and tells the app, as the daemon would. */
async function openTurn(page: Page, { withSteps, priorTurns }: { withSteps: boolean; priorTurns: number }) {
	await page.setViewportSize({ width: 900, height: 700 });
	await installFakeAgent(page, { workers: [{ id: sessionId, title: "Turn settle", mode: "chat" }] });
	// Keep the fake CDC streams reachable, to announce the finished turn on them.
	await page.addInitScript(() => {
		const Base = window.EventSource as unknown as new (url: string) => object;
		const sources: object[] = [];
		(window as unknown as { __settleSources: object[] }).__settleSources = sources;
		(window as unknown as { EventSource: unknown }).EventSource = class extends Base {
			constructor(url: string) {
				super(url);
				sources.push(this);
			}
		};
	});
	let settled = false;
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation") && route.request().method() === "GET") return route.fulfill({ json: conversation(settled, withSteps, priorTurns) });
		if (path.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
		if (path.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
		if (path.endsWith("/workspace/files")) return route.fulfill({ json: { files: [], truncated: false } });
		if (path.endsWith("/interface-transition")) return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible();
	await expect(page.locator("[data-turn-live-row]")).toBeVisible();
	// Past the opening snap, so the follow behaves as it does mid-session.
	await page.waitForTimeout(1_500);
	return {
		settle: async () => {
			settled = true;
			await page.evaluate((id) => {
				const sources = (window as unknown as { __settleSources: { url: string; _dispatch: (type: string, data: string) => void }[] }).__settleSources;
				for (const source of sources) {
					if (!source.url.includes("/api/v1/events")) continue;
					source._dispatch("session_updated", JSON.stringify({ seq: 9_999, projectId: "fake-proj", sessionId: id, type: "session_updated", payload: { conversationId: "conversation-turn-settle" }, createdAt: new Date().toISOString() }));
				}
			}, sessionId);
		},
	};
}

/**
 * Samples the latest turn's rows on every animation frame until the returned stop is
 * called. Returns once the first frame is in, so a frame that runs late still sees
 * the turn as it was before the caller settles it.
 */
async function recordFrames(page: Page) {
	await page.evaluate(() => new Promise<void>((resolve) => {
		const state = window as unknown as { __settleFrames: Frame[]; __settleStop: boolean };
		state.__settleFrames = [];
		state.__settleStop = false;
		const log = document.querySelector<HTMLElement>('[role="log"]')!;
		const tick = () => {
			const turn = Array.from(document.querySelectorAll("[data-chat-turn]")).at(-1)?.firstElementChild;
			const top = (element: Element | null | undefined) => (element ? element.getBoundingClientRect().top - log.getBoundingClientRect().top : NaN);
			const live = turn?.querySelector(":scope > [data-turn-live-row]");
			state.__settleFrames.push({
				scrollTop: log.scrollTop,
				prompt: top(turn?.firstElementChild),
				reply: top(Array.from(turn?.querySelectorAll("[data-chat-message-body]") ?? []).at(-1)),
				live: live ? top(live) : null,
				worked: Boolean(turn?.querySelector(":scope > [data-turn-worked]")),
			});
			resolve();
			if (!state.__settleStop) requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
	}));
	return async () => {
		await page.evaluate(() => {
			(window as unknown as { __settleStop: boolean }).__settleStop = true;
		});
		return page.evaluate(() => (window as unknown as { __settleFrames: Frame[] }).__settleFrames);
	};
}

function expectSteady(frames: Frame[], key: "scrollTop" | "prompt") {
	const values = frames.map((frame) => frame[key]);
	expect(Math.max(...values) - Math.min(...values), `${key} across ${frames.length} frames: ${values.join(", ")}`).toBeLessThan(1);
}

function expectOnlyRising(frames: Frame[]) {
	for (let index = 1; index < frames.length; index += 1) {
		expect(frames[index]!.reply, `reply top, frame ${index}: ${frames.map((frame) => frame.reply.toFixed(1)).join(", ")}`).toBeLessThanOrEqual(frames[index - 1]!.reply + 0.5);
	}
}

for (const priorTurns of [1, 30]) {
	test(`a finished turn folds its steps under Worked for without a jump (${priorTurns > 20 ? "windowed" : "short"} chat) @T0`, async ({ page }) => {
		test.setTimeout(60_000);
		const turn = await openTurn(page, { withSteps: true, priorTurns });
		const stop = await recordFrames(page);
		await turn.settle();
		await expect(page.getByRole("button", { name: /^Worked for/ })).toBeVisible();
		await page.waitForTimeout(600);
		const frames = await stop();
		const settled = frames.findIndex((frame) => frame.worked);
		expect(settled).toBeGreaterThan(0);

		expectSteady(frames, "scrollTop");
		expectSteady(frames, "prompt");
		// The reply rises into the room its steps left, and only rises. The steps are
		// already folded to one summary line while the turn runs, so that room is one row.
		expectOnlyRising(frames);
		expect(frames.at(-1)!.reply).toBeLessThan(frames[settled - 1]!.reply - 20);
		// The live row fades out where it was rather than jumping away.
		const live = frames.slice(settled).flatMap((frame) => (frame.live === null ? [] : [frame.live]));
		for (const top of live) expect(Math.abs(top - frames[settled - 1]!.live!)).toBeLessThan(1);
	});
}

test("a turn that went straight to its answer gets Worked for as plain text, with nothing moving @T0", async ({ page }) => {
	test.setTimeout(60_000);
	const turn = await openTurn(page, { withSteps: false, priorTurns: 1 });
	const stop = await recordFrames(page);
	await turn.settle();
	// Every finished turn has one; the earlier turn's says 20s.
	const worked = page.locator("[data-chat-turn]").last().locator("[data-turn-worked]");
	await expect(worked).toHaveText("Worked for 42s");
	await expect(page.locator("[data-turn-worked]").first()).toHaveText("Worked for 20s");
	await page.waitForTimeout(600);
	const frames = await stop();

	// Nothing to open: no control and no chevron.
	await expect(page.getByRole("button", { name: /^Worked for/ })).toHaveCount(0);
	await expect(worked.locator("svg")).toHaveCount(0);
	expectSteady(frames, "scrollTop");
	expectSteady(frames, "prompt");
	const replies = frames.map((frame) => frame.reply);
	expect(Math.max(...replies) - Math.min(...replies)).toBeLessThan(1);
});

test("opening and closing Worked for in a windowed chat keeps the view still @T0", async ({ page }) => {
	test.setTimeout(60_000);
	const turn = await openTurn(page, { withSteps: true, priorTurns: 30 });
	await turn.settle();
	const toggle = page.getByRole("button", { name: /^Worked for/ });
	await expect(toggle).toBeVisible();
	await page.waitForTimeout(600);
	for (const opening of [true, false]) {
		const stop = await recordFrames(page);
		await toggle.click();
		await page.waitForTimeout(500);
		const frames = await stop();
		expectSteady(frames, "scrollTop");
		expectSteady(frames, "prompt");
		// The reply moves one way only: down while the steps open, back up as they close.
		for (let index = 1; index < frames.length; index += 1) {
			const step = frames[index]!.reply - frames[index - 1]!.reply;
			expect(opening ? step : -step, `reply top, frame ${index}`).toBeGreaterThanOrEqual(-0.5);
		}
	}
});
