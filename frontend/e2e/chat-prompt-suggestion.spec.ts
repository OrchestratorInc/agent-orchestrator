import { expect, test, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const projectId = "chat-prompt-suggestion";
const sessionId = "chat-prompt-suggestion-worker";
const turnId = "completed-turn";
const prompt = "Fix the reconnect test to handle either frame order, then rerun the package race tests.";

async function openChat(page: Page, suggestionReady = Promise.resolve(), suggestedPrompt = prompt) {
	const sent: string[] = [];
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		workers: [{ id: sessionId, provider: "codex", title: "Review reconnect test", mode: "chat" }],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") {
			await route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
		} else if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({ json: { status: "ok", project: { id: projectId, agent: "codex", config: { worker: { agent: "codex" } } } } });
		} else if (pathname === `/api/v1/sessions/${sessionId}/conversation`) {
			const completedAt = "2026-10-12T06:00:00Z";
			await route.fulfill({
				json: {
					conversationId: "conversation-prompt-suggestion",
					sessionId,
					harness: "codex",
					mode: "chat",
					controller: "ready",
					latestSequence: 2,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [{ id: turnId, state: "completed", requestedAt: completedAt, startedAt: completedAt, completedAt }],
					messages: [
						{ id: "user-message", turnId, sequence: 1, revision: 0, role: "user", origin: "human", text: "Review the reconnect test.", streaming: false, createdAt: completedAt },
						{ id: "assistant-message", turnId, sequence: 2, revision: 0, role: "assistant", origin: "provider", text: "The first frame can be an acknowledgment or a command. Handle either order, then rerun the race tests.", streaming: false, createdAt: completedAt },
					],
					activities: [],
					settings: {},
				},
			});
		} else if (pathname.endsWith("/conversation/prompt-suggestion")) {
			expect(route.request().postDataJSON()).toEqual({ turnId });
			await suggestionReady;
			await route.fulfill({ json: { turnId, prompt: suggestedPrompt } });
		} else if (pathname.endsWith("/conversation/messages") && route.request().method() === "POST") {
			sent.push(route.request().postDataJSON().text);
			await route.fulfill({ json: { turnId: "next-turn", state: "running", duplicate: false } });
		} else if (pathname.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
		} else if (pathname.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
		} else if (pathname.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
		} else if (pathname.endsWith("/interface-transition")) {
			await route.fulfill({ json: { supported: true, targetMode: "tui" } });
		} else {
			await route.fulfill({ json: { status: "ok" } });
		}
	});
	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	const composer = page.getByRole("combobox", { name: "Message the agent" });
	await expect(composer).toBeVisible({ timeout: 15_000 });
	return { composer, sent, suggestion: page.getByTestId("prompt-suggestion") };
}

test("Tab inserts the suggested next message for editing, and Enter sends the edited draft @T0", async ({ page }) => {
	const { composer, sent, suggestion } = await openChat(page);
	await expect(suggestion).toHaveText(prompt);
	await expect(composer).toHaveText("");
	const ghostColor = await suggestion.evaluate((element) => getComputedStyle(element).color);
	const draftColor = await composer.evaluate((element) => getComputedStyle(element).color);
	expect(ghostColor).not.toBe(draftColor);
	await page.screenshot({ path: "/tmp/ao-chat-prompt-suggestion.png" });
	await composer.press("Enter");
	await expect(composer).toHaveText("");
	await expect(suggestion).toHaveText(prompt);
	expect(sent).toEqual([]);
	await composer.press("Tab");
	await expect(composer).toHaveText(prompt);
	await expect(composer).toBeFocused();
	await expect(suggestion).toBeHidden();
	expect(sent).toEqual([]);
	await page.keyboard.type(" Include the fix in this PR.");
	await composer.press("Enter");
	await expect.poll(() => sent).toEqual([`${prompt} Include the fix in this PR.`]);
});

test("typing dismisses the whole-message suggestion without accepting it @T0", async ({ page }) => {
	const { composer, sent, suggestion } = await openChat(page);
	await expect(suggestion).toHaveText(prompt);
	await composer.pressSequentially("My own next message");
	await expect(composer).toHaveText("My own next message");
	await expect(suggestion).toBeHidden();
	// Use editor keyboard commands so Lexical owns both the selection and deletion.
	// fill("") selects DOM text, then sends Delete, which can race reconciliation.
	await composer.press("ControlOrMeta+a");
	await composer.press("Backspace");
	await expect(suggestion).toBeHidden();
	await expect(composer).toHaveText("");
	expect(sent).toEqual([]);
});

test("Escape dismisses the suggestion and restores normal Tab focus navigation @T0", async ({ page }) => {
	const { composer, sent, suggestion } = await openChat(page);
	await expect(suggestion).toHaveText(prompt);
	await composer.press("Escape");
	await expect(suggestion).toBeHidden();
	await composer.press("Tab");
	await expect(composer).not.toBeFocused();
	await expect(composer).toHaveText("");
	expect(sent).toEqual([]);
});

test("a delayed suggestion does not overwrite a draft already being typed @T0", async ({ page }) => {
	let releaseSuggestion = () => {};
	const suggestionReady = new Promise<void>((resolve) => { releaseSuggestion = resolve; });
	const suggestionRequest = page.waitForRequest((request) => request.url().endsWith("/conversation/prompt-suggestion"));
	const { composer, sent, suggestion } = await openChat(page, suggestionReady);
	await suggestionRequest;
	await composer.fill("Keep my draft");
	const response = page.waitForResponse((response) => response.url().endsWith("/conversation/prompt-suggestion"));
	releaseSuggestion();
	await response;
	await expect(composer).toHaveText("Keep my draft");
	await expect(suggestion).toBeHidden();
	await composer.press("Tab");
	await expect(composer).toHaveText("Keep my draft");
	expect(sent).toEqual([]);
});

test("a long suggestion wraps within the empty composer at a narrow window width @T0", async ({ page }) => {
	await page.setViewportSize({ width: 480, height: 900 });
	const longPrompt = "Fix the reconnect test to accept either acknowledgment or command as its first frame, add coverage for both frame orders, and rerun the browser runtime package race tests before updating the pull request with the result.";
	const { composer, suggestion } = await openChat(page, Promise.resolve(), longPrompt);
	await expect(suggestion).toHaveText(longPrompt);
	await expect(composer).toHaveText("");
	await page.screenshot({ path: "/tmp/ao-chat-prompt-suggestion-narrow.png" });
	const suggestedBounds = await suggestion.boundingBox();
	const editableBounds = await composer.boundingBox();
	expect(suggestedBounds).not.toBeNull();
	expect(editableBounds).not.toBeNull();
	expect(suggestedBounds!.y + suggestedBounds!.height).toBeLessThanOrEqual(editableBounds!.y + editableBounds!.height + 1);
});

test("a full-length suggestion can be scrolled and accepted in a narrow composer @T0", async ({ page }) => {
	await page.setViewportSize({ width: 480, height: 900 });
	const longPrompt = "检查修复重连测试".repeat(30);
	const { composer, suggestion, sent } = await openChat(page, Promise.resolve(), longPrompt);
	await expect(suggestion).toHaveText(longPrompt);
	const scrollState = () => suggestion.evaluate((element) => {
		for (let parent = element.parentElement; parent; parent = parent.parentElement) {
			if (/(auto|scroll)/.test(getComputedStyle(parent).overflowY) && parent.scrollHeight > parent.clientHeight) {
				return { height: parent.clientHeight, scrollHeight: parent.scrollHeight, scrollTop: parent.scrollTop };
			}
		}
		return null;
	});
	const initialScroll = await scrollState();
	expect(initialScroll).not.toBeNull();
	expect(initialScroll!.height).toBeLessThanOrEqual(160);
	await composer.hover();
	await page.mouse.wheel(0, 160);
	await expect.poll(async () => (await scrollState())?.scrollTop ?? 0).toBeGreaterThan(0);
	await page.screenshot({ path: "/tmp/ao-chat-prompt-suggestion-scroll.png" });
	await composer.press("Tab");
	await expect(composer).toHaveText(longPrompt);
	expect(sent).toEqual([]);
});
