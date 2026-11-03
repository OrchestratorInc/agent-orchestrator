import { expect, test, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

// Focus hand-off between a closing Radix menu and the surface its item opened.
// A menu traps focus for the commit in which it closes and restores focus to
// its trigger a tick after it unmounts. Both of those used to land on top of
// the surface a menu item opened, leaving the New task composer with no caret
// until the user clicked it (#focus regression, reported 2026-09-03).

const projectId = "task-focus";
const sessionA = "focus-session-a";
const sessionB = "focus-session-b";

function conversation(sessionId: string) {
	const completedAt = "2026-08-26T06:00:00Z";
	return {
		conversationId: `conversation-${sessionId}`,
		sessionId,
		harness: "codex",
		mode: "chat",
		controller: "ready",
		latestSequence: 1,
		oldestSequence: 1,
		hasMoreBefore: false,
		turns: [
			{ id: `${sessionId}-turn-1`, state: "completed", requestedAt: completedAt, startedAt: completedAt, completedAt },
		],
		messages: [
			{
				kind: "message",
				id: `${sessionId}-message-1`,
				turnId: `${sessionId}-turn-1`,
				sequence: 1,
				revision: 0,
				role: "user",
				origin: "human",
				text: `Existing conversation in ${sessionId}`,
				streaming: false,
				createdAt: completedAt,
			},
		],
		activities: [],
		settings: {},
	};
}

async function setup(page: Page, { animated = false } = {}) {
	// The menu's exit animation decides whether Radix's deferred focus restore
	// lands before or after the dialog mounts, so both paths are worth covering.
	await page.emulateMedia({ reducedMotion: animated ? "no-preference" : "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: projectId,
		workers: [
			{ id: sessionA, provider: "codex", title: "Session A", mode: "chat" },
			{ id: sessionB, provider: "codex", title: "Session B", mode: "chat" },
		],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") {
			await route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
			return;
		}
		if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({
				json: {
					status: "ok",
					project: { id: projectId, agent: "codex", config: { worker: { agent: "codex" } } },
				},
			});
			return;
		}
		for (const id of [sessionA, sessionB]) {
			if (pathname === `/api/v1/sessions/${id}/conversation`) {
				await route.fulfill({ json: conversation(id) });
				return;
			}
			if (pathname === `/api/v1/sessions/${id}/conversation/models`) {
				await route.fulfill({ json: { models: [], selected: {} } });
				return;
			}
			if (pathname === `/api/v1/sessions/${id}/conversation/skills`) {
				await route.fulfill({ json: { skills: [] } });
				return;
			}
			if (pathname === `/api/v1/sessions/${id}/workspace/files`) {
				await route.fulfill({ json: { files: [], truncated: false } });
				return;
			}
			if (pathname === `/api/v1/sessions/${id}/interface-transition`) {
				await route.fulfill({ json: { supported: true, targetMode: "tui" } });
				return;
			}
		}
		await route.fulfill({ json: { status: "ok" } });
	});
}


function activeElementInfo(page: Page) {
	return page.evaluate(() => {
		const active = document.activeElement as HTMLElement | null;
		return {
			label: active?.getAttribute("aria-label") ?? active?.tagName ?? "none",
			inDialog: Boolean(active?.closest("[role='dialog']")),
			inTerminal: Boolean(active?.closest(".xterm")),
		};
	});
}

function openProjectMenu(page: Page) {
	return page
		.getByRole("button", { name: new RegExp(`Project actions for ${projectId}`) })
		.first()
		.click({ force: true });
}

// New task opens the new chat page for the project; its composer takes typing.
async function expectPromptTakesTyping(page: Page) {
	await expect(page).toHaveURL(new RegExp(`#/new\\?project=${projectId}`));
	const prompt = page.getByRole("combobox", { name: "Message the agent" });
	await expect(prompt).toBeVisible();
	await expect(page.getByTestId("chat-conversation-panel").getByRole("button", { name: /^Project: / })).toHaveText(projectId);
	await expect.poll(async () => (await activeElementInfo(page)).label).toBe("Message the agent");
	await page.keyboard.type("caret is here");
	await expect(prompt).toHaveText("caret is here");
}

test("renderer: the board New task button focuses the composer prompt @T0", async ({ page }) => {
	await setup(page);
	await page.goto(`/#/projects/${projectId}`);
	await page.getByRole("button", { name: "New task" }).first().click();
	await expectPromptTakesTyping(page);
});

for (const animated of [false, true]) {
	const motion = animated ? "animated" : "reduced motion";

	test(`renderer: New task from the sidebar project menu focuses the composer prompt (${motion}) @T0`, async ({
		page,
	}) => {
		await setup(page, { animated });
		await page.goto(`/#/projects/${projectId}/sessions/${sessionA}`);
		await expect(page.getByRole("combobox", { name: "Message the agent" })).toBeVisible();
		await openProjectMenu(page);
		await page.getByRole("menuitem", { name: /New task/ }).click();
		await expectPromptTakesTyping(page);
	});
}

test("renderer: New task from the sidebar project context menu focuses the composer prompt @T0", async ({ page }) => {
	await setup(page);
	await page.goto(`/#/projects/${projectId}/sessions/${sessionA}`);
	await expect(page.getByRole("combobox", { name: "Message the agent" })).toBeVisible();
	await page
		.getByRole("button", { name: new RegExp(`Project actions for ${projectId}`) })
		.first()
		.click({ button: "right", force: true });
	await page.getByRole("menuitem", { name: /New task/ }).click();
	await expectPromptTakesTyping(page);
});

test("renderer: New task from the command palette focuses the composer prompt @T0", async ({ page }) => {
	await setup(page);
	await page.goto(`/#/projects/${projectId}/sessions/${sessionA}`);
	await expect(page.getByRole("combobox", { name: "Message the agent" })).toBeVisible();
	await page.keyboard.press("ControlOrMeta+k");
	await page.getByRole("option", { name: /New task/ }).first().click();
	await expectPromptTakesTyping(page);
});

test("renderer: switching project with a draft never flashes the sample requests @T0", async ({ page }) => {
	await setup(page, { animated: true });
	await page.goto(`/#/new?project=${projectId}`);
	const prompt = page.getByRole("combobox", { name: "Message the agent" });
	await expect(page.getByRole("list", { name: "Sample requests" })).toBeVisible();
	await prompt.click();
	await page.keyboard.type("keep this");
	await expect(page.getByRole("list", { name: "Sample requests" })).toHaveCount(0);
	// Any frame that draws them counts, so watch the DOM rather than poll.
	await page.evaluate(() => {
		const seen = { flashed: false };
		(window as unknown as { __samples: typeof seen }).__samples = seen;
		new MutationObserver(() => {
			if (document.querySelector('ul[aria-label="Sample requests"]')) seen.flashed = true;
		}).observe(document.body, { childList: true, subtree: true });
	});

	const projectPicker = page.getByTestId("chat-conversation-panel").getByRole("button", { name: /^Project: / });
	await projectPicker.click();
	await page.getByRole("menuitem", { name: "Scratchpad" }).click();
	await expect(projectPicker).toHaveText("Scratchpad");
	await expect(prompt).toHaveText("keep this");
	expect(await page.evaluate(() => (window as unknown as { __samples: { flashed: boolean } }).__samples.flashed)).toBe(false);
});

test("renderer: opening another session focuses its chat composer @T0", async ({ page }) => {
	await setup(page);
	await page.goto(`/#/projects/${projectId}/sessions/${sessionA}`);
	const composer = page.getByRole("combobox", { name: "Message the agent" });
	await expect(composer).toBeVisible();
	await expect.poll(async () => (await activeElementInfo(page)).label).toBe("Message the agent");

	await page.getByRole("button", { name: /Open Session B/ }).first().click();
	await expect(composer).toBeVisible();
	await expect.poll(async () => (await activeElementInfo(page)).label).toBe("Message the agent");
	await page.keyboard.type("typed after switching");
	await expect(composer).toHaveText("typed after switching");
});

test("renderer: hovering a Chat session fetches its conversation once, and opening it reuses that @T0", async ({ page }) => {
	await setup(page);
	let requests = 0;
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${sessionB}/conversation*`, async (route) => {
		requests++;
		await route.fulfill({ json: conversation(sessionB) });
	});
	await page.goto(`/#/projects/${projectId}/sessions/${sessionA}`);
	await expect(page.getByText(`Existing conversation in ${sessionA}`)).toBeVisible();

	const row = page.getByRole("button", { name: /Open Session B/ }).first();
	await row.hover();
	await expect.poll(() => requests).toBe(1);
	const viewRegistered = page.waitForResponse((response) =>
		response.request().method() === "POST" && response.url().endsWith(`/sessions/${sessionB}/chat-view`));
	await row.click();
	await expect(page.getByText(`Existing conversation in ${sessionB}`)).toBeVisible();
	// A ready chat changes only through events, so neither mounting it nor
	// registering its Chat view downloads the prefetched history again.
	await viewRegistered;
	await page.waitForTimeout(500);
	expect(requests).toBe(1);
});
