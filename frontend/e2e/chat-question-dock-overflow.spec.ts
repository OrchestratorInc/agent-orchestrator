import { expect, test, type Locator, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

const sessionId = "chat-question-dock-overflow";
const now = "2026-10-25T12:00:00Z";
const longQuestion =
	"Before I delete anything, confirm which worktrees are safe to remove and which branches still carry local notes or uncommitted work that must be kept. ".repeat(20).trim();
const longURL = `https://console.anthropic.com/oauth/authorize?scope=org%3Acreate_api_key&state=${"x".repeat(3000)}`;

async function openDock(page: Page, detail: Record<string, unknown>): Promise<Locator> {
	// The size the overflow was reported at (#6975): a bounded dock must still fit its actions.
	await page.setViewportSize({ width: 960, height: 640 });
	await installFakeAgent(page, {
		workers: [{ id: sessionId, title: "Question overflow", mode: "chat" }],
	});
	const input = {
		kind: "activity", id: "question-1", turnId: "turn-1", sequence: 2, revision: 1,
		activityKind: "user_input", status: "pending", summary: "Question", requestId: "request-1", createdAt: now, detail,
	};
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation")) {
			await route.fulfill({ json: {
				conversationId: "conversation-question-dock-overflow", sessionId, harness: "claude-code", mode: "chat",
				controller: "busy", settings: {}, oldestSequence: 1, latestSequence: 2, hasMoreBefore: false,
				turns: [{ id: "turn-1", state: "running", requestedAt: now, startedAt: now }],
				messages: [{ kind: "message", id: "user-1", turnId: "turn-1", sequence: 1, revision: 0, role: "user", origin: "human", text: "Clean up merged worktrees.", streaming: false, createdAt: now }],
				activities: [input],
			} });
		} else if (path.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
		} else if (path.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
		} else if (path.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
		} else {
			await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
		}
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	const dock = page.getByTestId("elicitation-dock");
	await expect(dock).toBeVisible({ timeout: 15_000 });
	return dock;
}

/** The control is inside the dock and the window, and nothing paints over its centre. */
async function expectReachable(dock: Locator, control: Locator) {
	await expect(control).toBeVisible();
	const layout = await control.evaluate((node, dockNode) => {
		const rect = node.getBoundingClientRect();
		const dockRect = (dockNode as Element).getBoundingClientRect();
		const top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
		return {
			insideDock: rect.top >= dockRect.top && rect.bottom <= dockRect.bottom + 0.5,
			insideWindow: rect.bottom <= window.innerHeight,
			onTop: Boolean(top && (node === top || node.contains(top))),
		};
	}, await dock.elementHandle());
	expect(layout).toEqual({ insideDock: true, insideWindow: true, onTop: true });
}

/** The focused field's row paints its whole focus ring, without the scroll body clipping an edge. */
async function expectRingUnclipped(body: Locator, field: Locator) {
	const bodyHandle = await body.elementHandle();
	// Polled, because moving focus may still be scrolling the row in.
	await expect.poll(() => field.evaluate((node, bodyNode) => {
		// The row is the nearest ancestor drawing a box-shadow ring; its spread is how far the ring reaches.
		let row: Element | null = node;
		let spread = 0;
		for (; row && row !== bodyNode; row = row.parentElement) {
			const spreads = [...getComputedStyle(row).boxShadow.matchAll(/(?:-?[\d.]+px\s+){3}(-?[\d.]+)px/g)].map((match) => Number(match[1]));
			spread = Math.max(0, ...spreads);
			if (spread > 0) break;
		}
		const rect = row!.getBoundingClientRect();
		const bodyRect = (bodyNode as Element).getBoundingClientRect();
		return {
			ring: spread > 0,
			top: rect.top - spread >= bodyRect.top - 0.5,
			bottom: rect.bottom + spread <= bodyRect.bottom + 0.5,
		};
	}, bodyHandle)).toEqual({ ring: true, top: true, bottom: true });
}

test("a long question scrolls on its own and leaves the answers and actions reachable @T0", async ({ page }) => {
	const dock = await openDock(page, {
		inputMode: "form",
		message: longQuestion,
		schema: { type: "object", required: ["choice"], properties: {
			choice: { type: "string", title: "Choice", oneOf: [
				{ const: "login", title: "Delete fix/login" },
				{ const: "search", title: "Delete fix/search" },
				{ const: "keep", title: "Keep everything" },
			] },
		} },
	});

	// The choice's radio covers its whole row, so that is what a click lands on.
	await expectReachable(dock, dock.getByRole("radio", { name: /Delete fix\/login/ }));
	await expectReachable(dock, dock.getByRole("button", { name: "Continue" }));
	const question = dock.getByTestId("elicitation-question");
	await expect(question).toHaveText(longQuestion);
	expect(await question.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
});

test("every focused answer row keeps its whole focus ring inside the scrolling answers @T0", async ({ page }) => {
	// Enough choices that the answers scroll, so moving focus scrolls rows in to the body's edges.
	const choices = Array.from({ length: 12 }, (_, index) => ({
		const: `direction-${index + 1}`,
		title: `Direction ${index + 1}`,
		description: "Keeps each icon where a user drops it.",
	}));
	const dock = await openDock(page, {
		inputMode: "form",
		message: "Which direction should ship?",
		schema: { type: "object", required: ["question_0"], properties: {
			question_0: { type: "string", title: "Direction", oneOf: choices },
			question_0_custom: {
				type: "string", title: "Other",
				_meta: { _askUserQuestionCustomAnswer: { questionId: "question_0", isCustomAnswer: true } },
			},
		} },
	});
	const body = dock.getByTestId("elicitation-scroll-body");
	expect(await body.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);

	// Tab in from the header so the choices show their keyboard ring. The first sits on the
	// body's top edge; each arrow step after that scrolls the next one in at the bottom edge.
	await dock.getByRole("button", { name: "Cancel" }).focus();
	await page.keyboard.press("Tab");
	for (let index = 0; index < choices.length; index++) {
		if (index > 0) await page.keyboard.press("ArrowDown");
		const choice = dock.getByRole("radio").nth(index);
		await expect(choice).toBeFocused();
		await expectRingUnclipped(body, choice);
	}
	expect(await body.evaluate((node) => node.scrollTop > 0)).toBe(true);

	// The browser scrolls to the Other field, which sits inside the row that draws the ring.
	await page.keyboard.press("Tab");
	const other = dock.getByRole("textbox", { name: "Other" });
	await expect(other).toBeFocused();
	await expectRingUnclipped(body, other);

	// Back into the picked choice and up the list: each step scrolls a row in at the top edge.
	await page.keyboard.press("Shift+Tab");
	for (let index = choices.length - 1; index >= 0; index--) {
		if (index < choices.length - 1) await page.keyboard.press("ArrowUp");
		const choice = dock.getByRole("radio").nth(index);
		await expect(choice).toBeFocused();
		await expectRingUnclipped(body, choice);
	}
});

test("a long URL scrolls between a pinned header and consent actions @T0", async ({ page }) => {
	const dock = await openDock(page, { inputMode: "url", url: longURL, message: "Sign in to continue" });

	await expectReachable(dock, dock.getByText("Sign in to continue"));
	for (const name of ["Cancel", "Decline", "Open console.anthropic.com"]) {
		await expectReachable(dock, dock.getByRole("button", { name }));
	}
	const body = dock.getByTestId("elicitation-scroll-body");
	await expect(body).toContainText("console.anthropic.com");
	expect(await body.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
});
