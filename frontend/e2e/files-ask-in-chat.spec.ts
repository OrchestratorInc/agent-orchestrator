import path from "node:path";
import { _electron as electron, expect, test as base, type Locator, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent, installFakeBridge } from "./support/fake-bridge";
import { openInspector } from "./support/open-inspector";

// AO_E2E_ELECTRON=1 runs these specs inside AO's own Electron (its Chromium can
// differ from Playwright's: Electron 33 reports a shadow-root selection as
// collapsed while it holds text, which only showed up there).
const test = process.env.AO_E2E_ELECTRON
	? base.extend({
		page: async ({}, use) => {
			const app = await electron.launch({
				executablePath: path.join(process.cwd(), "node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"),
				args: [path.join(process.cwd(), "e2e/support/electron-shell.cjs")],
			});
			await use(await app.firstWindow());
			await app.close();
		},
	})
	: base;
const origin = `http://127.0.0.1:${process.env.AO_E2E_PORT ?? 5173}`;

const projectId = "ask-in-chat";
const chatSession = "ask-in-chat-worker";
const filePath = "src/retry.ts";
const before = Array.from({ length: 150 }, (_, index) => (index % 3 === 0 ? `const value${index + 1} = ${index + 1};` : `  step(${index + 1});`)).join("\n") + "\n";
const after = before.replace("  step(5);", "  step(5, { retry: true });").replace("  step(6);", "  step(6);\n  logStep(6);");
const patch = [
	`diff --git a/${filePath} b/${filePath}`,
	"index 1111111..2222222 100644",
	`--- a/${filePath}`,
	`+++ b/${filePath}`,
	"@@ -2,7 +2,8 @@",
	"   step(2);",
	"   step(3);",
	" const value4 = 4;",
	"-  step(5);",
	"+  step(5, { retry: true });",
	"   step(6);",
	"+  logStep(6);",
	" const value7 = 7;",
	"   step(8);",
	"",
].join("\n");

// No daemon serves the live event streams here, so each would fail and
// reconnect every few seconds, refetching and redrawing the review mid-drag.
// Answer them once and ask the browser not to reconnect for a while.
function quietEventStream(route: Parameters<Parameters<Page["route"]>[1]>[0]) {
	return route.fulfill({ status: 200, headers: { "content-type": "text/event-stream" }, body: "retry: 600000\n: connected\n\n" });
}

/** Serves one changed file for `id`, and records what the session's chat sends. */
async function stubWorkspace(page: Page, id: string): Promise<string[]> {
	const sentToChat: string[] = [];
	const file = { path: filePath, status: "modified", additions: 2, deletions: 1, size: after.length, binary: false, editable: true, fileFingerprint: "fp-1" };
	const detail = {
		sessionId: id, path: filePath, status: "modified", additions: 2, deletions: 1, size: after.length, binary: false, deleted: false,
		content: after, contentTruncated: false, diff: patch, diffTruncated: false, editable: true, fileFingerprint: "fp-1", workspaceVersion: "workspace-1",
	};
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${id}/**`, async (route) => {
		if (route.request().headers().accept?.includes("text/event-stream")) return quietEventStream(route);
		const url = new URL(route.request().url());
		const endpoint = url.pathname.replace(`/api/v1/sessions/${id}`, "");
		if (endpoint === "/workspace/manifest") {
			return route.fulfill({ json: { sessionId: id, workspaceVersion: "workspace-1", files: [file], sections: { committed: [], staged: [], unstaged: [file], untracked: [] }, commits: [], summary: { additions: 2, deletions: 1, files: 1 }, truncated: false } });
		}
		if (endpoint === "/workspace/diffs") {
			return route.fulfill({ json: { sessionId: id, workspaceVersion: "workspace-1", groups: [{ repository: "", patch, truncated: false, includedPaths: [filePath], deferred: [] }] } });
		}
		if (endpoint === "/workspace/file") return route.fulfill({ json: detail });
		if (endpoint === "/workspace/history") return route.fulfill({ json: { sessionId: id, commits: [] } });
		if (endpoint === "/workspace/file/revision") {
			const side = url.searchParams.get("side") === "before" ? "before" : "after";
			const content = side === "before" ? before : after;
			return route.fulfill({ json: { sessionId: id, path: filePath, side, binary: false, content, exists: true, size: content.length, truncated: false, workspaceVersion: "workspace-1", revision: side } });
		}
		if (endpoint === "/conversation/messages" && route.request().method() === "POST") {
			sentToChat.push((route.request().postDataJSON() as { text: string }).text);
			return route.fulfill({ json: { status: "ok" } });
		}
		return route.fallback();
	});
	return sentToChat;
}

/** A Chat-mode worker with one changed file, opened on its Files tab. */
async function openChatSession(page: Page) {
	await installFakeAgent(page, { projectId, projectName: projectId, workers: [{ id: chatSession, provider: "codex", title: "Ask in chat", mode: "chat" }] });
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		if (route.request().headers().accept?.includes("text/event-stream")) return quietEventStream(route);
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") return route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
		if (pathname === `/api/v1/projects/${projectId}`) return route.fulfill({ json: { status: "ok", project: { id: projectId, agent: "codex", config: { worker: { agent: "codex" } } } } });
		if (pathname === `/api/v1/sessions/${chatSession}/conversation`) {
			return route.fulfill({ json: { conversationId: "conversation-ask-in-chat", sessionId: chatSession, harness: "codex", mode: "chat", controller: "ready", latestSequence: 0, oldestSequence: 0, hasMoreBefore: false, turns: [], messages: [], activities: [], settings: {} } });
		}
		if (pathname === `/api/v1/sessions/${chatSession}/conversation/models`) return route.fulfill({ json: { models: [], selected: {} } });
		if (pathname === `/api/v1/sessions/${chatSession}/conversation/skills`) return route.fulfill({ json: { skills: [] } });
		if (pathname === `/api/v1/sessions/${chatSession}/interface-transition`) return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		return route.fulfill({ json: { status: "ok" } });
	});
	const sentToChat = await stubWorkspace(page, chatSession);
	const inspector = await openFilesTab(page, chatSession, projectId);
	const composer = page.getByRole("combobox", { name: "Message the agent" });
	await expect(composer).toBeVisible();
	return { inspector, composer, sentToChat };
}

async function openFilesTab(page: Page, id: string, project: string) {
	await page.goto(`${origin}/#/projects/${project}/sessions/${id}`);
	const inspector = await openInspector(page);
	await inspector.getByRole("tab", { name: /Files?$/ }).click();
	await expect(page.getByRole("button", { name: `Collapse ${filePath}` })).toBeVisible();
	await expect(row(page, REVIEW, 4)).toBeVisible();
	return inspector;
}

const REVIEW = "#inspector";
const CENTER = '[data-testid="session-file-workspace"]';

// Rows live in Pierre's open shadow root; Playwright's CSS engine pierces it.
function row(page: Page, scope: string, line: number, type = "context") {
	const kind = type === "context" ? '[data-line-type^="context"]' : `[data-line-type="${type}"]`;
	return page.locator(`${scope} diffs-container [data-line="${line}"]${kind}`).first();
}

const askButton = (page: Page) => page.locator("[data-selection-ask]");

/** Highlights code with the mouse, from the start of one row to the end of another. */
async function selectText(page: Page, from: Locator, to: Locator, { release = true } = {}) {
	await expect(from).toBeVisible();
	await expect(to).toBeVisible();
	// Pierre redraws rows once syntax highlighting loads (dropping a selection in
	// progress) and can load context above them (moving them); start once both
	// rows have stayed the same nodes in the same place for a second, which a
	// loaded machine running several workers needs.
	await expect.poll(async () => {
		const now = JSON.stringify([await from.boundingBox(), await to.boundingBox()]);
		return from.evaluate((element, position) => {
			const seen = window as unknown as { __aoRow?: Element; __aoPosition?: string };
			const same = seen.__aoRow === element && seen.__aoPosition === position;
			seen.__aoRow = element;
			seen.__aoPosition = position;
			return same;
		}, now);
	}, { intervals: [1000, 1000, 1000, 1000, 1000], timeout: 15_000 }).toBe(true);
	const a = (await from.boundingBox())!;
	const b = (await to.boundingBox())!;
	await page.mouse.move(a.x + 2, a.y + a.height / 2);
	await page.mouse.down();
	await page.mouse.move(b.x + Math.min(b.width - 2, 160), b.y + b.height / 2, { steps: 12 });
	if (release) await page.mouse.up();
}

/** The button sits inside the window and on or just under the given row. */
async function expectButtonBy(page: Page, target: Locator) {
	const button = (await askButton(page).boundingBox())!;
	const line = (await target.boundingBox())!;
	const width = await page.evaluate(() => window.innerWidth);
	expect(button.x).toBeGreaterThanOrEqual(0);
	expect(button.x + button.width).toBeLessThanOrEqual(width);
	expect(button.y).toBeGreaterThanOrEqual(line.y - button.height);
	expect(button.y).toBeLessThanOrEqual(line.y + line.height + 16);
}

test.beforeEach(async ({ page }) => {
	page.on("pageerror", (error) => { throw error; });
});

test.describe("Ask in chat from highlighted code", () => {
	test("review list: the button follows a drag, asks about lines 2–4, and the chip sends their code", async ({ page }) => {
		const { composer, sentToChat } = await openChatSession(page);
		// The review header is slotted from the selected surface's light DOM,
		// so the file lookup must succeed on that same host (review regression).
		await expect.poll(() => row(page, REVIEW, 2).evaluate((element) => {
			const host = (element.getRootNode() as ShadowRoot).host;
			return host.querySelector("[data-review-item-id]")?.getAttribute("data-review-item-id");
		})).toContain(filePath);
		await selectText(page, row(page, REVIEW, 2), row(page, REVIEW, 4), { release: false });
		// Shown while the selection is still being dragged, letting the drag through.
		await expect(askButton(page)).toBeVisible();
		await expect(askButton(page)).toHaveCSS("pointer-events", "none");
		await page.mouse.up();
		await expect(askButton(page)).not.toHaveCSS("pointer-events", "none");
		await expectButtonBy(page, row(page, REVIEW, 4));

		await askButton(page).getByRole("button", { name: "Ask in chat" }).click();
		await expect(composer).toContainText("retry.ts#L2-L4");
		await expect(composer).toBeFocused();
		await expect(askButton(page)).toBeHidden();
		await page.keyboard.type("why three steps?");
		await page.keyboard.press("Enter");
		await expect.poll(() => sentToChat.length).toBe(1);
		expect(sentToChat[0]).toContain("why three steps?");
		expect(sentToChat[0]).toContain("[src/retry.ts#L2-L4]");
		expect(sentToChat[0]).toContain("```diff\n   step(2);\n   step(3);\n const value4 = 4;\n```");
	});

	test("Cmd/Ctrl+L asks about the highlighted lines", async ({ page }) => {
		const { composer } = await openChatSession(page);
		await selectText(page, row(page, REVIEW, 8), row(page, REVIEW, 9));
		await expect(askButton(page)).toBeVisible();
		await page.keyboard.press("ControlOrMeta+L");
		await expect(composer).toContainText("retry.ts#L8-L9");
	});

	test("a highlight across removed and added lines quotes both, with markers", async ({ page }) => {
		const { composer, sentToChat } = await openChatSession(page);
		await selectText(page, row(page, REVIEW, 5, "change-deletion"), row(page, REVIEW, 7, "change-addition"));
		await askButton(page).getByRole("button", { name: "Ask in chat" }).click();
		await expect(composer).toContainText("retry.ts#L5-L7");
		await page.keyboard.press("Enter");
		await expect.poll(() => sentToChat.length).toBe(1);
		expect(sentToChat[0]).toContain("```diff\n-  step(5);\n+  step(5, { retry: true });\n   step(6);\n+  logStep(6);\n```");
	});

	test("in a split diff, old-side lines keep their old numbers", async ({ page }) => {
		const { inspector, composer } = await openChatSession(page);
		await inspector.getByRole("button", { name: "Split diff view" }).click();
		const oldRow = (line: number) => page.locator(`${REVIEW} diffs-container [data-deletions] [data-line="${line}"]`).first();
		await selectText(page, oldRow(6), oldRow(7));
		await askButton(page).getByRole("button", { name: "Ask in chat" }).click();
		await expect(composer).toContainText("retry.ts#L6-L7 (old)");
	});

	test("the full file in the centre: asking brings Chat forward with the file's lines", async ({ page }) => {
		const { inspector, composer, sentToChat } = await openChatSession(page);
		await inspector.getByRole("button", { name: "Open full file" }).click();
		await selectText(page, row(page, CENTER, 3), row(page, CENTER, 6));
		await expectButtonBy(page, row(page, CENTER, 6));
		await askButton(page).getByRole("button", { name: "Ask in chat" }).click();
		await expect(composer).toBeVisible();
		await expect(composer).toContainText("retry.ts#L3-L6");
		await page.keyboard.press("Enter");
		await expect.poll(() => sentToChat.length).toBe(1);
		expect(sentToChat[0]).toContain("```ts\n  step(3);\nconst value4 = 4;\n  step(5, { retry: true });\n  step(6);\n```");
	});

	test("the diff in the centre: asking about a range across a change", async ({ page }) => {
		const { inspector, composer } = await openChatSession(page);
		await inspector.getByRole("button", { name: "Open diff in center" }).click();
		await selectText(page, row(page, CENTER, 4), row(page, CENTER, 7, "change-addition"));
		await askButton(page).getByRole("button", { name: "Ask in chat" }).click();
		await expect(composer).toContainText("retry.ts#L4-L7");
	});

	test("the button hides on Escape, when scrolled away (returning when back), and when the rows redraw", async ({ page }) => {
		const { inspector } = await openChatSession(page);
		await selectText(page, row(page, REVIEW, 2), row(page, REVIEW, 4));
		await expect(askButton(page)).toBeVisible();
		await page.keyboard.press("Escape");
		await expect(askButton(page)).toBeHidden();

		await inspector.getByRole("button", { name: "Open full file" }).click();
		await selectText(page, row(page, CENTER, 3), row(page, CENTER, 6));
		await expect(askButton(page)).toBeVisible();
		const scroll = (where: "past" | "top") => row(page, CENTER, 6).evaluate((element, to) => {
			let node: Element | null = (element.getRootNode() as ShadowRoot).host;
			while (node && !/(auto|scroll)/.test(getComputedStyle(node).overflowY)) node = node.parentElement;
			if (!node) return;
			if (to === "top") node.scrollTo(0, 0);
			else node.scrollBy(0, element.getBoundingClientRect().bottom - node.getBoundingClientRect().top + 4);
		}, where);
		await scroll("past");
		await expect(askButton(page)).toBeHidden();
		await scroll("top");
		await expect(askButton(page)).toBeVisible();

		// Replace every row with a fresh copy, as Pierre does when it redraws.
		await page.locator(`${CENTER} diffs-container`).first().evaluate((host) => {
			for (const column of host.shadowRoot!.querySelectorAll("[data-content]")) {
				column.replaceChildren(...[...column.children].map((child) => child.cloneNode(true)));
			}
		});
		await expect(askButton(page)).toBeHidden();
	});

	test("a plain click in the code shows no button", async ({ page }) => {
		await openChatSession(page);
		await row(page, REVIEW, 3).click({ position: { x: 40, y: 8 } });
		await page.waitForTimeout(400);
		await expect(askButton(page)).toBeHidden();
	});

	test("the gutter + still opens the single-line feedback composer, and sits clear of the code and the line number", async ({ page }) => {
		await openChatSession(page);
		await row(page, REVIEW, 4).hover();
		const plus = page.locator(REVIEW).getByRole("button", { name: "Add feedback" }).last();
		await expect(plus).toBeVisible();
		const box = (await plus.boundingBox())!;
		const code = (await row(page, REVIEW, 4).boundingBox())!;
		const digits = await page.locator(`${REVIEW} diffs-container`).first().evaluate((host) => [...host.shadowRoot!.querySelectorAll("[data-line-number-content]")]
			.map((element) => element.getBoundingClientRect()).filter((rect) => rect.width > 0).map(({ left, right, top, bottom }) => ({ left, right, top, bottom })));
		// A drag from the start of a line selects its text instead of pressing the +.
		expect(box.x + box.width).toBeLessThanOrEqual(code.x);
		expect(digits.some((digit) => digit.top < box.y + box.height && box.y < digit.bottom && box.x + box.width > digit.left && digit.right > box.x)).toBe(false);
		await plus.click();
		await expect(page.getByPlaceholder("Describe what the agent should change...")).toBeVisible();
	});
});

test("a TUI session has no Chat composer, so highlighting offers no Ask in chat", async ({ page }) => {
	page.on("pageerror", (error) => { throw error; });
	await installFakeBridge(page);
	await page.route("http://127.0.0.1:8080/api/v1/**", (route) => (route.request().headers().accept?.includes("text/event-stream") ? quietEventStream(route) : route.fallback()));
	await stubWorkspace(page, "demo-working");
	await openFilesTab(page, "demo-working", "ao-demo");
	await selectText(page, row(page, REVIEW, 2), row(page, REVIEW, 4));
	await page.waitForTimeout(400);
	await expect(askButton(page)).toHaveCount(0);
});
