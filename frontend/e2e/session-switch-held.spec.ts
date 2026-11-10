import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// A session switch renders the destination in the background while the
// previous session stays on screen, inert, until the destination commits.
// These cover what the hook tests cannot reach: replies streaming through
// switches, typing straight after a keyboard switch, and a quick round trip
// through the board. The CPU is throttled so the held window is wide enough
// for a keystroke or a stale frame to land in it.

const sessions = ["alpha", "bravo", "charlie"].map((name) => ({
	id: `held-${name}`,
	title: `Held ${name}`,
	mode: "chat" as const,
	activity: "active" as const,
}));
type HeldSession = (typeof sessions)[number];

// isMacPlatform() reads platform, userAgent and userAgentData together; see
// cross-platform-focus.spec.ts. The session-jump chord is ⌘ on macOS and Ctrl
// elsewhere. (Next/previous session arrive through the Electron menu, which a
// browser run does not have.)
const PLATFORMS = [
	{ name: "macOS", navigator: "MacIntel", userAgentData: "macOS", userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36", jump: "Meta" },
	{ name: "Windows", navigator: "Win32", userAgentData: "Windows", userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36", jump: "Control" },
] as const;

const at = "2026-11-09T10:00:00Z";

/** Every reply grows by one chunk per refetch while `streaming` is on. */
function conversation(session: HeldSession, chunks: number) {
	const turns = Array.from({ length: 6 }, (_, i) => ({
		id: `${session.id}-turn-${i}`,
		state: i === 5 ? "running" : "completed",
		requestedAt: at,
		startedAt: at,
		...(i === 5 ? {} : { completedAt: at }),
	}));
	const messages = turns.flatMap((turn, i) => [
		{ kind: "message", id: `${session.id}-user-${i}`, turnId: turn.id, sequence: i * 2 + 1, revision: 0, role: "user", origin: "human", text: `Ask ${session.title} ${i}`, streaming: false, createdAt: at },
		{
			kind: "message", id: `${session.id}-reply-${i}`, turnId: turn.id, sequence: i * 2 + 2, revision: i === 5 ? chunks : 0,
			role: "assistant", origin: "agent", streaming: i === 5, createdAt: at,
			text: i === 5
				? `Streaming ${session.title}: ${Array.from({ length: chunks }, (_, n) => `chunk-${n}`).join(" ")}`
				: `Answer from ${session.title} ${i}. `.repeat(12),
		},
	]);
	return {
		conversationId: `conversation-${session.id}`, sessionId: session.id, harness: "codex", mode: "chat",
		controller: "busy", latestSequence: 12, oldestSequence: 1, hasMoreBefore: false,
		turns, messages, activities: [], settings: {},
	};
}

async function heldHarness(page: Page, platform: (typeof PLATFORMS)[number] = PLATFORMS[0]) {
	await page.setViewportSize({ width: 1280, height: 800 });
	await page.addInitScript(({ nav, uad, ua }) => {
		Object.defineProperty(window.navigator, "platform", { configurable: true, value: nav });
		Object.defineProperty(window.navigator, "userAgent", { configurable: true, value: ua });
		Object.defineProperty(window.navigator, "userAgentData", { configurable: true, value: { platform: uad } });
	}, { nav: platform.navigator, uad: platform.userAgentData, ua: platform.userAgent });
	await installFakeAgent(page, { workers: sessions, expandProject: true });
	// Keep the fake CDC streams reachable, to announce new reply chunks on them.
	await page.addInitScript(() => {
		const Base = window.EventSource as unknown as new (url: string) => object;
		const sources: object[] = [];
		(window as unknown as { __heldSources: object[] }).__heldSources = sources;
		(window as unknown as { EventSource: unknown }).EventSource = class extends Base {
			constructor(url: string) {
				super(url);
				sources.push(this);
			}
		};
	});
	const chunks = new Map(sessions.map((session) => [session.id, 1]));
	const drafts: { sessionId: string; text: string }[] = [];
	const requests: { sessionId: string; sent: number; answered?: number }[] = [];
	await page.route("**/api/v1/sessions/held-*/**", async (route) => {
		const request = route.request();
		const path = new URL(request.url()).pathname;
		const session = sessions.find((candidate) => path.includes(`/sessions/${candidate.id}/`));
		if (!session) return route.fallback();
		if (path.endsWith("/conversation") && request.method() === "GET") {
			const entry: (typeof requests)[number] = { sessionId: session.id, sent: Date.now() };
			requests.push(entry);
			await route.fulfill({ json: conversation(session, chunks.get(session.id) ?? 1) });
			entry.answered = Date.now();
			return;
		}
		if (path.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
		if (path.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
		if (path.endsWith("/workspace/files")) return route.fulfill({ json: { files: [], truncated: false } });
		if (path.endsWith("/interface-transition")) return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		if (path.endsWith("/shell-terminals")) return route.fulfill({ json: { terminals: [] } });
		if (request.method() !== "GET") drafts.push({ sessionId: session.id, text: request.postData() ?? "" });
		return route.fulfill({ json: { status: "ok" } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessions[0].id}`);
	await expectShowing(page, sessions[0]);
	await page.waitForTimeout(1_000);

	let streaming: ReturnType<typeof setInterval> | undefined;
	let dispatching = false;
	return {
		drafts,
		requests,
		/** Grows every session's reply and tells the app, as the daemon does while agents stream. */
		startStreaming() {
			streaming = setInterval(() => {
				// Bound CDP delivery while the throttled page is busy.
				if (dispatching) return;
				dispatching = true;
				for (const session of sessions) chunks.set(session.id, (chunks.get(session.id) ?? 1) + 1);
				void page.evaluate((ids) => {
					const sources = (window as unknown as { __heldSources: { url: string; readyState: number; _dispatch: (type: string, data: string) => void }[] }).__heldSources;
					for (const source of sources) {
						if (!source.url.includes("/api/v1/events") || source.readyState !== 1) continue;
						for (const id of ids) {
							source._dispatch("session_updated", JSON.stringify({ seq: Date.now(), projectId: "fake-proj", sessionId: id, type: "session_updated", payload: { conversationId: `conversation-${id}` }, createdAt: new Date().toISOString() }));
						}
					}
				}, sessions.map((session) => session.id)).catch(() => undefined).finally(() => { dispatching = false; });
			}, 120);
		},
		stop() {
			if (streaming) clearInterval(streaming);
		},
	};
}

async function throttle(page: Page, rate: number) {
	const cdp = await page.context().newCDPSession(page);
	await cdp.send("Emulation.setCPUThrottlingRate", { rate });
}

function conversationLog(page: Page) {
	return page.getByRole("log", { name: "Conversation" });
}

/**
 * The highest reply chunk the conversation shows, or -1 before any. The last
 * chunk runs into the turn's elapsed time in the log's text ("chunk-128h 7m"),
 * so only chunks followed by a space count.
 */
async function latestShownChunk(page: Page) {
	const text = await conversationLog(page).textContent() ?? "";
	return Math.max(-1, ...Array.from(text.matchAll(/chunk-(\d+) /g), (match) => Number(match[1])));
}

async function expectShowing(page: Page, session: HeldSession) {
	await expect(page).toHaveURL(new RegExp(`/sessions/${session.id}$`));
	await expect(conversationLog(page)).toContainText(`Answer from ${session.title} 0.`, { timeout: 15_000 });
	await expect(page.locator("#session-workspace")).not.toHaveAttribute("inert");
}

/** Samples which session each painted frame shows, until stopped. */
async function recordShown(page: Page) {
	await page.evaluate((titles) => {
		const state = window as unknown as { __heldShown: string[]; __heldStop: boolean };
		state.__heldShown = [];
		state.__heldStop = false;
		const tick = () => {
			const text = document.querySelector('[role="log"][aria-label="Conversation"]')?.textContent ?? "";
			state.__heldShown.push(titles.find((title) => text.includes(`Answer from ${title} 0.`)) ?? "none");
			if (!state.__heldStop) requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
	}, sessions.map((session) => session.title));
	return () => page.evaluate(() => {
		const state = window as unknown as { __heldShown: string[]; __heldStop: boolean };
		state.__heldStop = true;
		return state.__heldShown;
	});
}

type SwitchFrame = { at: number; route: string; inert: boolean; log: string };

/** Records, from the next frame on, each change in route, held state and shown session. */
async function recordSwitch(page: Page): Promise<() => Promise<SwitchFrame[]>> {
	await page.evaluate((titles) => {
		const state = window as unknown as { __switchFrames: SwitchFrame[]; __switchStop: boolean };
		const started = performance.now();
		state.__switchFrames = [];
		state.__switchStop = false;
		let last = "";
		const tick = () => {
			const text = document.querySelector('[role="log"][aria-label="Conversation"]')?.textContent ?? "";
			const frame = {
				at: Math.round(performance.now() - started),
				route: location.hash.split("/").pop() ?? "",
				inert: document.getElementById("session-workspace")?.hasAttribute("inert") ?? false,
				log: titles.find((title) => text.includes(`Answer from ${title} 0.`)) ?? "none",
			};
			const key = `${frame.route}|${frame.inert}|${frame.log}`;
			if (key !== last) state.__switchFrames.push(frame);
			last = key;
			if (!state.__switchStop) requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
	}, sessions.map((session) => session.title));
	return () => page.evaluate(() => {
		const state = window as unknown as { __switchFrames: SwitchFrame[]; __switchStop: boolean };
		state.__switchStop = true;
		return state.__switchFrames;
	});
}

test.afterEach(async ({ page }) => {
	// Leave the next test an unthrottled browser even if this one failed.
	await throttle(page, 1).catch(() => undefined);
});

test("renderer: streaming replies keep updating through rapid session switches @T0", async ({ page }) => {
	const errors: string[] = [];
	page.on("pageerror", (error) => errors.push(error.message));
	// Seven switches on a 4x throttled dev build outlast the default timeout.
	test.setTimeout(120_000);
	const harness = await heldHarness(page);
	harness.startStreaming();
	await throttle(page, 4);
	try {
		let previous = sessions[0];
		for (const session of [sessions[1], sessions[2], sessions[0], sessions[2], sessions[1], sessions[0], sessions[1]]) {
			const clicked = Date.now();
			const firstRequest = harness.requests.length;
			const timeline = await recordSwitch(page);
			await page.getByRole("button", { name: `Open ${session.title}`, exact: true }).click();
			await expectShowing(page, session);
			const frames = await timeline();
			// A streaming session on screen must not stall the switch: every
			// streamed update restarts the destination's background render, so
			// the hold gives way after a bounded time. Routing before the hold
			// and the destination's own render are a throttled dev build's speed,
			// not the hold's, so only the hold itself is bounded: its 500 ms limit
			// plus one forced render, which CI measured at about 1.6 s here. A
			// stalled hold, the failure this guards, lasted over 15 s.
			const heldFrom = frames.find((frame) => frame.inert)?.at;
			const heldUntil = heldFrom === undefined ? undefined : frames.find((frame) => frame.at > heldFrom && !frame.inert)?.at;
			const fetches = harness.requests.slice(firstRequest)
				.map((entry) => `${entry.sessionId === session.id ? "dest" : "other"} ${entry.sent - clicked}->${entry.answered === undefined ? "pending" : entry.answered - clicked}`);
			const report = `frames (ms since click | route | inert | log): ${frames.map((frame) => `${frame.at} ${frame.route} ${frame.inert ? "inert" : "-"} ${frame.log}`).join("; ")}; conversation fetches: ${fetches.join(", ")}`;
			if (heldFrom !== undefined) expect((heldUntil ?? Infinity) - heldFrom, `the hold outlasted its limit; ${report}`).toBeLessThan(5_000);
			// Once the destination shows, the previous session never comes back.
			const shownAt = frames.find((frame) => frame.log === session.title)?.at ?? Infinity;
			expect(frames.filter((frame) => frame.at > shownAt && frame.log === previous.title), report).toEqual([]);
			previous = session;
			// The shown session keeps streaming after the switch. Its reply grows
			// faster than a throttled dev build repaints it, so wait for the
			// shown reply to advance rather than to catch up with the stream.
			const shownChunk = await latestShownChunk(page);
			await expect.poll(() => latestShownChunk(page), { timeout: 10_000 }).toBeGreaterThan(shownChunk);
		}
	} finally {
		harness.stop();
	}
	expect(errors).toEqual([]);
});

for (const platform of PLATFORMS) {
	test(`renderer: typing right after a keyboard switch never reaches the previous session on ${platform.name} @T0`, async ({ page }) => {
		const harness = await heldHarness(page, platform);
		const composer = page.getByRole("combobox", { name: "Message the agent" });
		await composer.click();
		await page.keyboard.type("kept");
		await expect(composer).toHaveText("kept");
		await throttle(page, 6);

		// ⌘/Ctrl+1–9 opens the nth session in sidebar order; aim at one that is not this one.
		const order = await page.getByRole("button", { name: /^Open Held / }).evaluateAll((buttons) => buttons.map((button) => button.getAttribute("aria-label")));
		const target = order.findIndex((label) => label !== `Open ${sessions[0].title}`);
		expect(target).toBeGreaterThanOrEqual(0);
		await page.keyboard.press(`${platform.jump}+${target + 1}`);
		// Straight away, while the destination may still be rendering.
		await page.keyboard.type("zz");
		await expect(page).not.toHaveURL(new RegExp(`/sessions/${sessions[0].id}$`));
		await expect(page.locator("#session-workspace")).not.toHaveAttribute("inert");
		await throttle(page, 1);

		await page.getByRole("button", { name: `Open ${sessions[0].title}`, exact: true }).click();
		await expectShowing(page, sessions[0]);
		await expect(composer).toHaveText("kept");
		expect(harness.drafts.filter((draft) => draft.sessionId === sessions[0].id && draft.text.includes("zz"))).toEqual([]);
	});
}

test("renderer: a quick trip through the board never shows the earlier session again @T0", async ({ page }) => {
	await heldHarness(page);
	await throttle(page, 4);
	const stop = await recordShown(page);

	await page.goto("/#/projects/fake-proj");
	await page.getByRole("button", { name: `Open ${sessions[2].title}`, exact: true }).click();
	await expectShowing(page, sessions[2]);

	const shown = await stop();
	const destination = shown.indexOf(sessions[2].title);
	expect(destination).toBeGreaterThanOrEqual(0);
	// Once the board replaced it, the first session never paints again.
	const left = shown.indexOf("none");
	expect(left).toBeGreaterThanOrEqual(0);
	expect(shown.slice(left)).not.toContain(sessions[0].title);
});
