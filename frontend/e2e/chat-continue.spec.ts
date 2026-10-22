import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Stop and Continue on one button, like pause and play: stop the agent mid-turn,
// then either continue it with the same button or type a new question, which
// turns the button back into Send.

const sessionId = "chat-continue";
const now = "2026-10-05T12:00:00Z";
const CONTINUE_PROMPT = "Continue from where you stopped.";

type TurnState = "running" | "interrupted" | "continued";

function snapshot(state: TurnState) {
	return {
		conversationId: "conversation-chat-continue",
		continueTurnId: state === "interrupted" ? "turn-long" : "",
		sessionId,
		harness: "codex",
		mode: "chat",
		controller: state === "interrupted" ? "ready" : "busy",
		latestSequence: 2,
		oldestSequence: 1,
		hasMoreBefore: false,
		turns: [
			{
				id: "turn-long",
				state: state === "continued" ? "interrupted" : state,
				providerTurnId: "provider-long",
				requestedAt: now,
				startedAt: now,
				...(state === "running" ? {} : { completedAt: now }),
			},
			...(state === "continued"
				? [{ id: "turn-continue", state: "running", providerTurnId: "provider-continue", requestedAt: now, startedAt: now }]
				: []),
		],
		messages: [
			{ kind: "message", id: "user-long", turnId: "turn-long", sequence: 1, revision: 0, role: "user", origin: "human", text: "Write the migration and its tests", streaming: false, createdAt: now },
			{ kind: "message", id: "agent-long", turnId: "turn-long", sequence: 2, revision: 0, role: "assistant", origin: "agent", text: "Starting with the migration: adding the paused_at column", streaming: state === "running", createdAt: now },
			...(state === "continued"
				? [{ kind: "message", id: "user-continue", turnId: "turn-continue", sequence: 3, revision: 0, role: "user", origin: "human", text: CONTINUE_PROMPT, continuation: true, streaming: false, createdAt: now }]
				: []),
		],
		activities: [],
		settings: {},
	};
}

/** A Chat session whose one turn is running until the user stops it. */
async function installStoppableConversation(page: Page) {
	let state: TurnState = "running";
	const sent: string[] = [];
	const continuations: boolean[] = [];
	await installFakeAgent(page, { workers: [{ id: sessionId, title: "Continue evidence", mode: "chat" }] });
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const request = route.request();
		const path = new URL(request.url()).pathname;
		if (path.endsWith("/conversation") && request.method() === "GET") return route.fulfill({ json: snapshot(state) });
		if (path.endsWith("/conversation/interrupt") && request.method() === "POST") {
			state = "interrupted";
			return route.fulfill({ json: { ok: true } });
		}
		if (path.endsWith("/conversation/messages") && request.method() === "POST") {
			const body = request.postDataJSON() as { text: string; continuation?: boolean };
			sent.push(body.text);
			continuations.push(Boolean(body.continuation));
			if (body.continuation) state = "continued";
			return route.fulfill({ json: { turnId: "turn-continue", state: "running" } });
		}
		if (path.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
		if (path.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
		if (path.endsWith("/workspace/files")) return route.fulfill({ json: { files: [], truncated: false } });
		if (path.endsWith("/interface-transition")) return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible();
	return { sent, continuations };
}

async function capture(page: Page, name: string) {
	const directory = process.env.AO_CONTINUE_EVIDENCE_DIR;
	if (directory) await page.screenshot({ path: `${directory}/${name}.png`, fullPage: true });
}

test("the one button stops a turn, then continues it, like pause and play @T0", async ({ page }) => {
	const { sent, continuations } = await installStoppableConversation(page);
	const stop = page.getByRole("button", { name: "Stop turn" });
	await expect(stop).toBeVisible();
	await capture(page, "continue-1-running");

	await stop.click();
	await expect(page.getByText("The agent was interrupted by you")).toBeVisible();
	// The same button, now Continue.
	const resume = page.getByRole("button", { name: "Continue turn" });
	await expect(resume).toBeEnabled();
	await expect(stop).toHaveCount(0);
	await capture(page, "continue-2-stopped");

	await resume.click();
	await expect.poll(() => sent).toEqual([CONTINUE_PROMPT]);
	expect(continuations).toEqual([true]);
	// The continue shows as a quiet marker, not as a message the user wrote.
	await expect(page.getByText("Continued", { exact: true })).toBeVisible();
	await expect(page.getByText(CONTINUE_PROMPT)).toHaveCount(0);
	await capture(page, "continue-3-continued");
});

test("after stopping, typing turns the button into Send for a new question @T0", async ({ page }) => {
	const { sent, continuations } = await installStoppableConversation(page);
	await page.getByRole("button", { name: "Stop turn" }).click();
	await expect(page.getByRole("button", { name: "Continue turn" })).toBeVisible();

	const composer = page.getByRole("combobox", { name: "Message the agent" });
	await composer.click();
	await page.keyboard.type("Skip the tests for now, just the migration");
	await expect(page.getByRole("button", { name: "Continue turn" })).toHaveCount(0);
	await expect(page.getByRole("button", { name: "Send message" })).toBeEnabled();
	await capture(page, "continue-4-typed");

	await page.getByRole("button", { name: "Send message" }).click();
	await expect.poll(() => sent).toEqual(["Skip the tests for now, just the migration"]);
	expect(continuations).toEqual([false]);
});
