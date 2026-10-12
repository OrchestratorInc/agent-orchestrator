import { expect, test, type Locator, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const projectId = "chat-composer-history";
const sessionId = "chat-composer-history-worker";
const sent = ["Second ask\nwith a second line", "Latest ask"];

async function openChat(page: Page): Promise<Locator> {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: projectId,
		workers: [{ id: sessionId, provider: "codex", title: "Composer history", mode: "chat" }],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") {
			await route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
			return;
		}
		if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({
				json: { status: "ok", project: { id: projectId, agent: "codex", config: { worker: { agent: "codex" } } } },
			});
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/conversation`) {
			const at = "2026-08-26T06:00:00Z";
			await route.fulfill({
				json: {
					conversationId: "conversation-chat-composer-history",
					sessionId,
					harness: "codex",
					mode: "chat",
					controller: "ready",
					latestSequence: sent.length,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: sent.map((_, index) => ({
						id: `turn-${index}`,
						state: "completed",
						requestedAt: at,
						startedAt: at,
						completedAt: at,
					})),
					messages: sent.map((text, index) => ({
						kind: "message",
						id: `message-${index}`,
						turnId: `turn-${index}`,
						sequence: index + 1,
						revision: 0,
						role: "user",
						origin: "human",
						text,
						streaming: false,
						createdAt: at,
					})),
					activities: [],
					settings: {},
				},
			});
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/conversation/models`) {
			await route.fulfill({ json: { models: [], selected: {} } });
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/conversation/skills`) {
			await route.fulfill({ json: { skills: [] } });
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/workspace/files`) {
			await route.fulfill({ json: { files: [], truncated: false } });
			return;
		}
		await route.fulfill({ json: { status: "ok" } });
	});

	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	const composer = page.getByRole("combobox", { name: "Message the agent" });
	await expect(composer).toBeVisible({ timeout: 15_000 });
	await expect(page.getByText("Latest ask").first()).toBeVisible();
	await composer.click();
	return composer;
}

/** The composer's text with its paragraphs joined by newlines, as it is sent. */
const composerText = (composer: Locator) =>
	composer.evaluate((element) =>
		Array.from(element.querySelectorAll("p"), (paragraph) => paragraph.textContent).join("\n"));

/** The paragraph holding the browser's caret. */
const caretLine = (composer: Locator) =>
	composer.evaluate((element) => {
		const node = window.getSelection()?.anchorNode;
		const paragraph = node instanceof Element ? node.closest("p") : node?.parentElement?.closest("p");
		return Array.from(element.querySelectorAll("p")).indexOf(paragraph as HTMLParagraphElement);
	});

// Lexical adopts a native caret move on a later selectionchange, so a key pressed
// right after one must still see where the browser put the caret.
test("arrow recall follows native caret moves made just before it @T0", async ({ page }) => {
	const composer = await openChat(page);
	await page.keyboard.type("draft");
	for (let step = 0; step < "draft".length; step += 1) await composer.press("ArrowLeft");
	await composer.press("ArrowUp");
	await expect.poll(() => composerText(composer)).toBe("Latest ask");
	await composer.press("ArrowUp");
	await expect.poll(() => composerText(composer)).toBe(sent[0]);

	// Off the start of a recalled multiline message, Down is caret movement again.
	await composer.press("ArrowRight");
	await composer.press("ArrowDown");
	await expect.poll(() => caretLine(composer)).toBe(1);
	expect(await composerText(composer)).toBe(sent[0]);

	for (let step = 0; step < sent[0]!.length; step += 1) await composer.press("ArrowLeft");
	await composer.press("ArrowDown");
	await expect.poll(() => composerText(composer)).toBe("Latest ask");
	await composer.press("ArrowDown");
	await expect.poll(() => composerText(composer)).toBe("draft");
});
