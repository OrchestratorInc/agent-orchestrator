import { expect, test, type Page, type Route } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { openInspector } from "./support/open-inspector";
import { installFakeAgent } from "./support/fake-bridge";

const projectId = "chat-provider-switch";
const sessionId = "chat-provider-switch-worker";
const now = "2026-10-19T12:00:00Z";

interface SwitchRequest {
	targetHarness: string;
	message?: { text: string; clientMessageId: string };
}

/** One switch the daemon made, with the message sent in it. */
interface Switch {
	from: string;
	to: string;
	text: string;
	clientMessageId: string;
}

/** The chat as the daemon reports it: on harness, after the switches made so far. */
function snapshot(harness: string, switches: Switch[]) {
	// Each switch is its marker, then the message's completed turn.
	return {
		conversationId: "conversation-provider-switch",
		sessionId,
		harness,
		mode: "chat",
		controller: "ready",
		latestSequence: switches.length * 2,
		oldestSequence: 1,
		hasMoreBefore: false,
		turns: switches.map((_entry, index) => ({
			id: `turn-${index}`, state: "completed", requestedAt: now, startedAt: now, completedAt: now,
		})),
		messages: switches.map((entry, index) => ({
			kind: "message", id: `message-${index}`, turnId: `turn-${index}`, sequence: index * 2 + 2, revision: 0,
			role: "user", origin: "human", text: entry.text, clientMessageId: entry.clientMessageId, streaming: false, createdAt: now,
		})),
		activities: switches.map((entry, index) => ({
			kind: "activity", id: `branch-${index}:provider-switch`, sequence: index * 2 + 1, revision: 0,
			activityKind: "system", status: "completed", summary: `Switched provider from ${entry.from} to ${entry.to}`,
			createdAt: now, detail: { event: "provider_switch", sourceHarness: entry.from, targetHarness: entry.to },
		})),
		settings: {},
	};
}

/**
 * A chat on Codex whose daemon switches agents as asked, or refuses with
 * refusal. Returns the switch requests the app sent.
 */
async function installSwitchingChat(
	page: Page,
	refusal?: { status: number; code: string; message: string },
	model?: { id: string; displayName: string },
) {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: projectId,
		workers: [{ id: sessionId, provider: "codex", title: "Provider switch worker", mode: "chat", status: "idle", activity: "idle" }],
	});
	let harness = "codex";
	const switches: Switch[] = [];
	const requests: SwitchRequest[] = [];
	const models = (agentId: string) => ({
		agentId, allowCustom: true, fetchedAt: now, models: [], selectionMode: "catalog", source: "test", stale: false,
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route: Route) => {
		const pathname = new URL(route.request().url()).pathname;
		const method = route.request().method();
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") {
			await route.fulfill({ json: { agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")] } });
			return;
		}
		if (pathname === "/api/v1/agents/chat/unavailable") {
			await route.fulfill({ json: { agents: [] } });
			return;
		}
		if (pathname === "/api/v1/agents/chat/permission-modes") {
			const modes = ["default", "accept-edits", "auto", "bypass-permissions"];
			await route.fulfill({ json: { agents: [{ agent: "claude-code", modes }, { agent: "codex", modes }] } });
			return;
		}
		const agentModels = /^\/api\/v1\/agents\/([^/]+)\/models$/.exec(pathname);
		if (agentModels) {
			await route.fulfill({ json: models(agentModels[1]!) });
			return;
		}
		if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({ json: { status: "ok", project: { id: projectId, agent: "codex", config: { worker: { agent: "codex" } } } } });
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/conversation` && method === "GET") {
			await route.fulfill({ json: {
				...snapshot(harness, switches),
				settings: model ? { model: model.id, reasoningEffort: "high" } : {},
				usage: model ? {
					contextUsed: 20000, contextWindow: 200000,
					inputTokens: 20000, outputTokens: 0, cachedTokens: 0, totalTokens: 20000,
				} : undefined,
			} });
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/provider-switch` && method === "POST") {
			const request = route.request().postDataJSON() as SwitchRequest;
			requests.push(request);
			if (refusal) {
				await route.fulfill({ status: refusal.status, json: { error: "conflict", code: refusal.code, message: refusal.message } });
				return;
			}
			switches.push({
				from: harness, to: request.targetHarness,
				text: request.message?.text ?? "", clientMessageId: request.message?.clientMessageId ?? "",
			});
			harness = request.targetHarness;
			await page.evaluate(([id, provider]) => window.__aoFakeAgent!.setProvider(id!, provider!), [sessionId, harness]);
			await route.fulfill({
				headers: { "X-AO-Provider-Switched": "1" },
				json: { status: "ok", message: { turnId: `turn-${switches.length - 1}`, state: "completed", duplicate: false } },
			});
			return;
		}
		if (pathname === `/api/v1/sessions/${sessionId}/conversation/models`) {
			await route.fulfill({ json: {
				models: model ? [{ ...model, efforts: ["high"], defaultEffort: "high" }] : [],
				selected: model ? { model: model.id, reasoningEffort: "high" } : {},
			} });
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
		if (pathname === `/api/v1/sessions/${sessionId}/interface-transition`) {
			await route.fulfill({ json: { supported: true, targetMode: "tui" } });
			return;
		}
		await route.fulfill({ json: { status: "ok" } });
	});
	await page.goto(`/#/projects/${projectId}/sessions/${sessionId}`);
	await expect(page.getByRole("region", { name: "Chat" })).toBeVisible();
	return requests;
}

const picker = (page: Page) => page.getByRole("button", { name: "Agent, model and reasoning effort for the next message" });

/** Picks agent for the next message from the composer's picker. */
async function pickAgent(page: Page, agent: string) {
	await picker(page).click();
	// By keyboard: the submenu slides in, so a pointer can land on it mid-move.
	await page.getByRole("menuitem", { name: /^Agent/ }).focus();
	await page.keyboard.press("ArrowRight");
	const choice = page.getByRole("menuitemradio", { name: agent });
	await choice.focus();
	await page.keyboard.press("Enter");
	await page.keyboard.press("Escape");
	await page.keyboard.press("Escape");
}

async function send(page: Page, text: string) {
	const input = page.getByRole("combobox", { name: "Message the agent" });
	await input.click();
	await input.fill(text);
	await input.press("Enter");
}

test("a picked agent takes over with the next message, and the chat can switch back @T0", async ({ page }) => {
	const requests = await installSwitchingChat(page);

	await pickAgent(page, "Claude Code");
	await send(page, "Carry on with the login page");
	await expect.poll(() => requests.length).toBe(1);
	expect(requests[0]).toMatchObject({ targetHarness: "claude-code", message: { text: "Carry on with the login page" } });
	await expect(page.getByRole("separator", { name: "Switched from Codex to Claude Code" })).toBeVisible();
	await expect(picker(page)).toContainText("Claude Code");

	await pickAgent(page, "Codex");
	await send(page, "Back to Codex");
	await expect.poll(() => requests.length).toBe(2);
	expect(requests[1]).toMatchObject({ targetHarness: "codex", message: { text: "Back to Codex" } });
	await expect(page.getByRole("separator", { name: "Switched from Claude Code to Codex" })).toBeVisible();
});

test("a refused switch keeps the draft and the previous agent @T0", async ({ page }) => {
	const requests = await installSwitchingChat(page, {
		status: 409, code: "PROVIDER_SWITCH_busy", message: "the provider is still working",
	});

	await pickAgent(page, "Claude Code");
	await send(page, "Carry on with the login page");
	await expect.poll(() => requests.length).toBe(1);
	const notice = page.getByRole("alert").filter({ hasText: "Couldn't switch agent" });
	await expect(notice).toContainText("the provider is still working");
	// Nothing was sent: the message stays to edit, and the chat stays on Codex.
	await expect(page.getByRole("combobox", { name: "Message the agent" })).toHaveText("Carry on with the login page");
	await expect(picker(page)).toContainText("Codex");
	await notice.getByRole("button", { name: "Dismiss" }).click();
	await expect(notice).toHaveCount(0);
});

test("composer pickers stay inside the composer beside an open browser @P0", async ({ page }) => {
	await installSwitchingChat(page, undefined, { id: "gpt-5.4-long", displayName: "GPT 5.4 with a longer model name" });
	await openInspector(page);
	await page.locator("#inspector").getByRole("tab", { name: "Browser" }).click();
	const model = picker(page);
	await expect(model).toContainText("GPT 5.4 with a longer model name");
	const permissions = page.getByRole("button", { name: "Approval policy for the next turn" });
	for (const width of [1280, 1100, 960]) {
		await page.setViewportSize({ width, height: 720 });
		await expect.poll(async () => {
			const box = await model.locator("xpath=ancestor::form").boundingBox();
			const modelBox = await model.boundingBox();
			const permissionBox = await permissions.boundingBox();
			if (!box || !modelBox || !permissionBox) return false;
			return modelBox.y >= box.y && modelBox.y + modelBox.height <= box.y + box.height
				&& modelBox.x >= box.x && modelBox.x + modelBox.width <= permissionBox.x
				&& Math.abs(modelBox.y - permissionBox.y) <= 1;
		}).toBe(true);
	}
	await permissions.click();
	await expect(page.getByRole("menu")).toHaveAttribute("data-browser-native-overlay", "true");
	await page.keyboard.press("Escape");
	await model.click();
	await page.getByRole("menuitem", { name: /^Agent/ }).focus();
	await page.keyboard.press("ArrowRight");
	const agent = page.getByRole("menuitemradio", { name: "Claude Code" });
	await expect(agent).toBeVisible();
	await expect(agent.locator('xpath=ancestor::*[@role="menu"]')).toHaveAttribute("data-browser-native-overlay", "true");
});
