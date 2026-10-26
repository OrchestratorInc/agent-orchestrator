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
