import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

const sessionId = "chat-question-history";
const now = "2026-10-19T12:00:00Z";
const prompt = "Which worktrees should I delete? Keep fix/settings until its local notes have been saved.";
const feedback = "Please tell me the worktree names before deleting anything.";

test("question replies remain readable after another question and a reload @T0", async ({ page }) => {
	await installFakeAgent(page, {
		workers: [{ id: sessionId, title: "Worktree cleanup", mode: "chat" }],
	});
	let answered = false;
	let resolution: unknown;
	let settled = false;
	const input = {
		kind: "activity", id: "question-1", turnId: "turn-1", sequence: 2, revision: 1,
		activityKind: "user_input", status: "pending", summary: prompt, requestId: "request-1", createdAt: now,
		detail: {
			inputMode: "form", message: prompt,
			schema: { type: "object", properties: {
				question_0: { type: "string", title: "Delete", oneOf: [{ const: "safe", title: "fix/login", description: "Merged with no local changes" }] },
				question_0_custom: { type: "string", title: "Other" },
			} },
		},
	};
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/inputs/request-1/resolve")) {
			resolution = route.request().postDataJSON();
			answered = true;
			await route.fulfill({ json: {} });
		} else if (path.endsWith("/conversation")) {
			await route.fulfill({ json: {
				conversationId: "conversation-question-history", sessionId, harness: "claude-code", mode: "chat",
				controller: settled ? "ready" : "busy", settings: {}, oldestSequence: 1, latestSequence: answered ? 3 : 2, hasMoreBefore: false,
				turns: [{ id: "turn-1", state: settled ? "completed" : "running", requestedAt: now, startedAt: now, ...(settled ? { completedAt: now } : {}) }],
				messages: [{ kind: "message", id: "user-1", turnId: "turn-1", sequence: 1, revision: 0, role: "user", origin: "human", text: "Help me clean up merged worktrees.", streaming: false, createdAt: now }],
				activities: [
					answered ? { ...input, status: "resolved", detail: { ...input.detail, action: "accept", content: { question_0: "safe", question_0_custom: feedback } } } : input,
					...(answered && !settled ? [{ ...input, id: "question-2", requestId: "request-2", sequence: 3, detail: { ...input.detail, message: "Should I keep fix/settings?" } }] : []),
				],
			} });
		} else if (path.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
		} else if (path.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
		} else if (path.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
		} else if (path.endsWith("/interface-transition")) {
			await route.fulfill({ json: { supported: true, targetMode: "tui" } });
		} else {
			await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
		}
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible({ timeout: 15_000 });
	const dock = page.getByTestId("elicitation-composer-dock");
	await expect(dock.getByText(prompt)).toBeVisible();
	await dock.getByRole("radio", { name: /fix\/login/ }).check();
	await dock.getByRole("textbox", { name: "Other" }).fill(feedback);
	await dock.getByRole("button", { name: "Continue" }).click();
	await expect.poll(() => resolution).toEqual({ action: "accept", content: { question_0: "safe", question_0_custom: feedback } });
	const history = page.getByRole("region", { name: "Agent question history" });
	await expect(history.getByText(prompt)).toBeVisible();
	await expect(history.getByText(feedback)).toBeVisible();
	await expect(dock.getByText("Should I keep fix/settings?")).toBeVisible();
	await expect(dock.getByRole("textbox", { name: "Other" })).toHaveValue("");
	await expect(dock.getByRole("radio", { name: /fix\/login/ })).not.toBeChecked();
	await page.reload();
	await expect(history.getByText(feedback)).toBeVisible();
	settled = true;
	await page.reload();
	await expect(dock).toHaveCount(0);
	await expect(history.getByText(prompt)).toBeVisible();
	await expect(history.getByText("fix/login — Merged with no local changes")).toBeVisible();
	await expect(history.getByText(feedback)).toBeVisible();
	await expect(history.getByRole("button")).toHaveCount(0);
	const evidenceDirectory = process.env.AO_QUESTION_HISTORY_EVIDENCE_DIR;
	if (evidenceDirectory) await page.screenshot({ path: `${evidenceDirectory}/question-history.png`, fullPage: true });
});
