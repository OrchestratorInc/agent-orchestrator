import { expect, test, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

// The new chat page hands its brief to the session's chat when the first send
// creates it. The first session ever opened used to draw a blank pane for a
// moment while the session view's lazy chunk resolved, even when preloaded.

const projectId = "handoff";
const at = new Date().toISOString();

async function setup(page: Page, { existingSession = false } = {}) {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: projectId,
		workers: existingSession ? [{ id: "handoff-w1", provider: "codex", title: "Hand this over", mode: "chat" }] : [],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === "/api/v1/settings") return route.fulfill({ json: { defaultSessionMode: "chat", chatHarnesses: ["codex"] } });
		if (pathname.startsWith("/api/v1/agents/readiness")) return route.fulfill({ json: { agents: [agentReadiness("codex", "Codex")] } });
		if (pathname === `/api/v1/projects/${projectId}`) {
			return route.fulfill({ json: { status: "ok", project: { id: projectId, agent: "codex", config: { worker: { agent: "codex" } } } } });
		}
		if (pathname === "/api/v1/orchestrators/delegate") {
			await page.evaluate(() => (window as unknown as { __aoFakeAgent: { createWorker: (worker: unknown) => void } }).__aoFakeAgent
				.createWorker({ id: "handoff-w1", provider: "codex", title: "Hand this over", mode: "chat" }));
			return route.fulfill({ json: { workerId: "handoff-w1" } });
		}
		if (pathname === "/api/v1/sessions/handoff-w1/conversation") {
			return route.fulfill({ json: {
				conversationId: "c-handoff", sessionId: "handoff-w1", harness: "codex", mode: "chat", controller: "ready",
				latestSequence: 1, oldestSequence: 1, hasMoreBefore: false,
				turns: [{ id: "t1", state: "running", requestedAt: at, startedAt: at }],
				messages: [{ kind: "message", id: "m1", turnId: "t1", sequence: 1, revision: 0, role: "user", origin: "human", text: "Hand this over", streaming: false, createdAt: at }],
				activities: [], settings: {},
			} });
		}
		return route.fulfill({ json: { status: "ok" } });
	});
}

test("renderer: the first send from the new chat page never blanks the composer @T0", async ({ page }) => {
	await setup(page);
	await page.goto(`/#/new?project=${projectId}`);
	const prompt = page.getByRole("combobox", { name: "Message the agent" });
	await expect(prompt).toBeVisible();
	// The page preloads the session view as it opens; give it the moment typing would.
	await page.waitForTimeout(1500);
	// Every frame counts, so a requestAnimationFrame loop watches for a missing composer.
	await page.evaluate(() => {
		const seen = { blank: false };
		(window as unknown as { __handoff: typeof seen }).__handoff = seen;
		const tick = () => {
			if (!document.querySelector('[role="combobox"][aria-label="Message the agent"]')) seen.blank = true;
			requestAnimationFrame(tick);
		};
		tick();
	});
	await prompt.click();
	await page.keyboard.type("Hand this over");
	await page.keyboard.press("Enter");
	await expect(page).toHaveURL(/sessions\/handoff-w1/);
	await expect(prompt).toBeVisible();
	await page.waitForTimeout(500);
	expect(await page.evaluate(() => (window as unknown as { __handoff: { blank: boolean } }).__handoff.blank)).toBe(false);
});

// The composer's shortcuts work on the new chat page as on the session's chat.
test("renderer: Cmd/Ctrl+Shift+M opens the model menu on the new chat page and in the chat @T0", async ({ page }) => {
	await setup(page, { existingSession: true });
	for (const where of ["new", "session"] as const) {
		if (where === "new") await page.goto(`/#/new?project=${projectId}`);
		else await page.goto(`/#/projects/${projectId}/sessions/handoff-w1`);
		const prompt = page.getByRole("combobox", { name: "Message the agent" });
		await expect(prompt).toBeVisible();
		await expect(page.locator("[data-shortcut-target='model-picker']")).toBeEnabled();
		await prompt.click();
		await page.keyboard.press("ControlOrMeta+Shift+KeyM");
		await expect(page.getByRole("menu")).toBeVisible();
		// Closing hands the caret back to the composer.
		await page.keyboard.press("Escape");
		await expect(page.getByRole("menu")).toBeHidden();
		await expect(prompt).toBeFocused();
	}
});
