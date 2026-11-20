import { expect, test, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";
import { installFakeTerminalMux } from "./support/fake-terminal-mux";

// Issue #8420: on Windows/Linux a focused terminal owned Ctrl+F, and the renderer
// dropped the chord for Find in chat without looking at the binding, so even a
// user-rebound chord never opened the bar. Ctrl+Shift+F opens chat search
// while plain Ctrl+F (Cmd+F on macOS) keeps opening terminal search.

const projectId = "find-from-terminal";
const sessionId = "find-from-terminal-chat";
const shellHandle = "find-from-terminal-shell";
const at = "2026-08-26T06:00:00Z";

async function openChatWithFocusedTerminal(page: Page) {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await page.setViewportSize({ width: 1280, height: 800 });
	await page.addInitScript(() => {
		// Split panes are the layout where the transcript and a terminal are both on
		// screen, which is the case issue #8420 describes.
		localStorage.setItem("ao.developerMode", "true");
		localStorage.setItem("ao.splitPanes", "true");
	});
	await installFakeAgent(page, {
		projectId,
		projectName: projectId,
		workers: [{ id: sessionId, provider: "codex", title: "Find from terminal", mode: "chat" }],
	});
	await installFakeTerminalMux(page, { [shellHandle]: "shell ready\n" });
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const request = route.request();
		const pathname = new URL(request.url()).pathname;
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
		if (pathname === `/api/v1/sessions/${sessionId}/conversation` && request.method() === "GET") {
			await route.fulfill({
				json: {
					conversationId: "conversation-find-from-terminal",
					sessionId,
					harness: "codex",
					mode: "chat",
					controller: "ready",
					latestSequence: 1,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [{ id: "turn-0", state: "completed", requestedAt: at, startedAt: at, completedAt: at }],
					messages: [
						{
							kind: "message",
							id: "message-0",
							turnId: "turn-0",
							sequence: 1,
							revision: 0,
							role: "user",
							origin: "human",
							text: "The dashboard is ready for review.",
							streaming: false,
							createdAt: at,
						},
						{
							kind: "message",
							id: "message-1",
							turnId: "turn-0",
							sequence: 2,
							revision: 0,
							role: "assistant",
							origin: "agent",
							text: "Dashboard data is ready for review.",
							streaming: false,
							createdAt: at,
						},
					],
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
		if (pathname === "/api/v1/shell-terminals") {
			if (request.method() === "POST") {
				await route.fulfill({
					json: {
						shellTerminal: {
							handleId: shellHandle,
							projectId,
							sessionId,
							workingDir: "/tmp/ws",
							title: "shell",
							createdAt: at,
						},
					},
				});
				return;
			}
			await route.fulfill({ json: { shellTerminals: [] } });
			return;
		}
		await route.fulfill({ json: { status: "ok" } });
	});

	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	await expect(page.getByRole("combobox", { name: "Message the agent" })).toBeVisible({ timeout: 15_000 });
}

const findBar = (page: Page) => page.locator('input[aria-label="Find in chat"]');

async function focusSplitShell(page: Page) {
	await openChatWithFocusedTerminal(page);
	await page.getByRole("button", { name: "New terminal" }).click();
	await expect(page.getByTestId("chat-shell-terminal")).toBeVisible({ timeout: 15_000 });

	// Back to the transcript, then move the terminal beside it: the layout where
	// find in chat is worth reaching from a focused terminal.
	await page.getByRole("tab", { name: /Find from terminal/ }).click();
	await expect(page.getByRole("combobox", { name: "Message the agent" })).toBeVisible();
	await page.getByRole("tab", { name: "Terminal 1" }).click({ button: "right" });
	await page.getByRole("menuitem", { name: "Split right" }).click();
	await expect(page.locator(".xterm:visible")).toBeVisible({ timeout: 15_000 });
	await expect(page.getByRole("combobox", { name: "Message the agent" })).toBeVisible();

	const shell = page.locator(".xterm:visible").last();
	await shell.click();
	await expect.poll(() => page.evaluate(() => Boolean(document.activeElement?.closest(".xterm")))).toBe(true);

	return shell;
}

test("focused terminal keeps its platform find key; Ctrl+Shift+F opens chat Find on Windows/Linux @T0 @TRM", async ({ page }, testInfo) => {
	const shell = await focusSplitShell(page);
	// macOS uses Cmd+F for terminal search; it has no Ctrl+Shift+F default.
	await page.keyboard.press(process.platform === "darwin" ? "Meta+f" : "Control+f");
	await expect(page.getByRole("searchbox", { name: "Search terminal" })).toBeVisible();
	await expect(findBar(page)).toHaveCount(0);
	await testInfo.attach("terminal-search", { body: await page.screenshot(), contentType: "image/png" });
	await page.keyboard.press("Escape");
	if (process.platform === "darwin") return;

	await shell.click();
	await expect.poll(() => page.evaluate(() => Boolean(document.activeElement?.closest(".xterm")))).toBe(true);

	// The terminal-safe chord opens the chat's find bar with the terminal focused.
	await page.keyboard.press("Control+Shift+F");
	await expect(findBar(page)).toBeVisible();
	await expect(findBar(page)).toBeFocused();
	await testInfo.attach("chat-find-default", { body: await page.screenshot(), contentType: "image/png" });
	await page.keyboard.press("Escape");
	await expect(findBar(page)).toHaveCount(0);
	await expect.poll(() => page.evaluate(() => Boolean(document.activeElement?.closest(".xterm")))).toBe(true);
});

test("custom Ctrl+J opens chat Find before xterm can receive the key @T0 @TRM", async ({ page }, testInfo) => {
	const shell = await focusSplitShell(page);
	await page.evaluate(async () => {
		const modulePath = "/src/renderer/stores/keybindings-store.ts";
		const { useKeybindingsStore } = await import(/* @vite-ignore */ modulePath);
		await useKeybindingsStore.getState().setBindings("find-in-chat", [
			{ key: "j", ctrl: true, meta: false, shift: false, alt: false },
		]);
	});
	const input = shell.getByRole("textbox", { name: "Terminal input" });
	await input.evaluate((node) => {
		node.setAttribute("data-test-received-keys", "");
		node.addEventListener("keydown", (event) => {
			const key = (event as KeyboardEvent).key.toLowerCase();
			if (key === "a" || key === "j") {
				node.setAttribute("data-test-received-keys", (node.getAttribute("data-test-received-keys") ?? "") + key);
			}
		}, { capture: true });
	});
	// This listener is on xterm's actual keyboard target. Ordinary keys reach
	// the target, but the Find key is captured before xterm can emit a line feed.
	await page.keyboard.press("a");
	await expect(input).toHaveAttribute("data-test-received-keys", "a");
	await page.keyboard.press("Control+j");
	await expect(findBar(page)).toBeFocused();
	await expect(input).toHaveAttribute("data-test-received-keys", "a");
	await testInfo.attach("chat-find-custom-ctrl-j", { body: await page.screenshot(), contentType: "image/png" });
	await page.keyboard.press("Escape");
	await expect(findBar(page)).toHaveCount(0);
	await expect(input).toBeFocused();
	await page.keyboard.press("a");
	await expect(input).toHaveAttribute("data-test-received-keys", "aa");
});
