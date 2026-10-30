import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Evidence rig for the "Agent tool" null-body regression (and its fix).
//
// A hermes delegate call reports no title, no rawInput and no content. Daemons
// before the fix stored those absent fields as JSON null, and the mcp_tool row
// expanded onto a literal "null". This spec seeds one conversation with both
// shapes — the historical null-only call and a call whose arguments the daemon
// kept — and captures what the timeline shows when the reader opens them.
//
// Run with AO_TOOL_EVIDENCE_DIR set to a directory to capture screenshots, and
// `video: "on"` (below) leaves a screencast of the expansion in test-results.

test.use({ video: "on" });

const sessionId = "chat-tool-null";
const now = "2026-10-23T12:00:00Z";

function snapshot() {
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
			{
				id: "turn-1",
				state: "completed",
				providerTurnId: "provider-1",
				requestedAt: now,
				startedAt: now,
				completedAt: now,
			},
		],
		messages: [
			{
				kind: "message",
				id: "user-1",
				turnId: "turn-1",
				sequence: 1,
				revision: 0,
				role: "user",
				origin: "human",
				text: "Spawn both workers in parallel",
				streaming: false,
				createdAt: now,
			},
		],
		activities: [
			{
				kind: "activity",
				id: "agent-null",
				turnId: "turn-1",
				sequence: 2,
				revision: 1,
				activityKind: "mcp_tool",
				status: "completed",
				summary: "Agent tool",
				// The historical stored shape: a call with no payload kept as JSON null.
				detail: { arguments: null, result: null, content: null },
				createdAt: now,
			},
			{
				kind: "activity",
				id: "agent-real",
				turnId: "turn-1",
				sequence: 3,
				revision: 1,
				activityKind: "mcp_tool",
				status: "completed",
				summary: "Agent tool",
				detail: { arguments: { task: "fix the flaky login test" } },
				createdAt: now,
			},
		],
		settings: {},
	};
}

async function installConversation(page: Page) {
	await installFakeAgent(page, {
		workers: [{ id: sessionId, title: "Tool call evidence", mode: "chat" }],
	});
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation") && route.request().method() === "GET") {
			await route.fulfill({ json: snapshot() });
			return;
		}
		if (path.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
			return;
		}
		if (path.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
			return;
		}
		if (path.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
			return;
		}
		if (path.endsWith("/interface-transition")) {
			await route.fulfill({ json: { supported: true, targetMode: "tui" } });
			return;
		}
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible();
	// A completed turn collapses its work behind the duration summary; open the
	// turn, then the run ("Used 2 tool calls"), so the rows are on screen.
	await page.getByRole("button", { name: /^Worked for/ }).click();
	await page.getByRole("button", { name: /Ran 2 tool calls/ }).click();
}

async function capture(page: Page, name: string) {
	const directory = process.env.AO_TOOL_EVIDENCE_DIR;
	if (directory) await page.screenshot({ path: `${directory}/${name}.png`, fullPage: true });
}

test("a tool call whose payload the daemon stored as null shows no null body @T0", async ({ page }) => {
	await installConversation(page);

	const rows = page.getByRole("button", { name: /Agent tool/ });
	await expect(rows).toHaveCount(2);

	// First row: the null-payload call. The fix leaves it with no body at all —
	// the row is not openable and never prints "null". (On the pre-fix build
	// this row is enabled, opens, and shows a literal "null"; the assertion
	// below is what goes red there.)
	const nullRow = rows.first();
	await expect(nullRow).toBeDisabled();
	await capture(page, "tool-call-null-row");
	await expect(page.getByText("null", { exact: true })).toHaveCount(0);
});

test("a tool call with stored arguments opens onto them @T0", async ({ page }) => {
	await installConversation(page);

	const rows = page.getByRole("button", { name: /Agent tool/ });
	// Second row: the call whose arguments survived.
	const argsRow = rows.nth(1);
	await argsRow.click();
	await expect(page.getByText("Arguments")).toBeVisible();
	await expect(page.getByText(/fix the flaky login test/)).toBeVisible();
	await capture(page, "tool-call-arguments-opened");
});
