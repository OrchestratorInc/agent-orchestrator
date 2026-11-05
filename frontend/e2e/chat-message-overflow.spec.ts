import { expect, test, type Locator, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

const sessionId = "chat-message-overflow";
const now = "2026-08-18T08:05:58Z";
const longMessage =
	'LABEL OCR OUTPUT:\n{"time":"2026-08-18T13:35:58.376284251+05:30","level":"INFO","msg":"request","method":"GET","path":"/v1/foods/barcode/890619087014","request_id":"a6ff589fb4fc214a","duration":154957090}';

async function expectBubbleToStayInsideViewport(bubble: Locator) {
	await expect(bubble).toBeVisible();
	const layout = await bubble.evaluate((node) => {
		const bubbleRect = node.getBoundingClientRect();
		const viewport = node.closest(".chat-scroll-viewport");
		if (!viewport) throw new Error("chat scroll viewport not found");
		const viewportRect = viewport.getBoundingClientRect();
		return {
			bubbleRight: bubbleRect.right,
			viewportRight: viewportRect.right,
			bubbleScrollWidth: node.scrollWidth,
			bubbleClientWidth: node.clientWidth,
		};
	});

	expect(layout.bubbleRight).toBeLessThanOrEqual(layout.viewportRight);
	expect(layout.bubbleScrollWidth).toBeLessThanOrEqual(layout.bubbleClientWidth);
}

async function openConversation(
	page: Page,
	{ turnState, messages, activities }: { turnState: string; messages: unknown[]; activities: unknown[] },
) {
	await installFakeAgent(page, {
		workers: [{ id: sessionId, title: "Debug chat input", mode: "chat" }],
	});

	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation")) {
			await route.fulfill({
				json: {
					conversationId: "conversation-chat-message-overflow",
					sessionId,
					harness: "codex",
					mode: "chat",
					controller: "ready",
					latestSequence: messages.length + activities.length,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [
						{
							id: "turn-1",
							state: turnState,
							requestedAt: now,
							startedAt: now,
							...(turnState === "completed" ? { completedAt: now } : {}),
						},
					],
					messages,
					activities,
					settings: {},
				},
			});
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
		await route.fulfill({
			status: 404,
			json: { error: { code: "NOT_FOUND", message: "not found" } },
		});
	});

	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
}

test("long unbroken user messages stay inside their chat bubble @T0", async ({ page }) => {
	await openConversation(page, {
		turnState: "completed",
		messages: [
			{
				kind: "message",
				id: "user-1",
				turnId: "turn-1",
				sequence: 1,
				revision: 0,
				role: "user",
				origin: "human",
				text: longMessage,
				streaming: false,
				createdAt: now,
			},
		],
		activities: [
			{
				kind: "activity",
				id: "steer-1",
				turnId: "turn-1",
				sequence: 2,
				revision: 0,
				activityKind: "system",
				status: "completed",
				summary: longMessage,
				detail: { event: "steer", text: longMessage, origin: "human" },
				createdAt: now,
			},
		],
	});
	const humanBubble = page
		.locator(".cursor-chat-human-message")
		.filter({ hasText: "LABEL OCR OUTPUT" });
	const steerBubble = page
		.getByText("Steered", { exact: true })
		.locator("..")
		.locator(":scope > div")
		.first();

	await expectBubbleToStayInsideViewport(humanBubble);
	await expectBubbleToStayInsideViewport(steerBubble);
});

// Claude's Read reaches AO over ACP as an MCP-style row whose only name is its
// title, so the label carries the whole file path.
test("a tool row named by a long file path truncates inside the chat column @T0", async ({ page }) => {
	const readPath =
		"/private/tmp/claude-501/-Users-dhruvsharma--ao-data-worktrees-agent-orchestrator-1-agent-orchestrator-1-349/d5f66703-1942-488f-aab0-aa2bf491a787/tasks/bkzss8xyu.output";
	await openConversation(page, {
		turnState: "running",
		messages: [],
		activities: [
			{
				kind: "activity",
				id: "read-1",
				turnId: "turn-1",
				sequence: 1,
				revision: 0,
				activityKind: "mcp_tool",
				status: "completed",
				summary: `Read ${readPath}`,
				detail: {
					protocol: "acp",
					toolKind: "read",
					locations: [{ path: readPath }],
					content: [{ type: "content", content: { type: "text", text: "1\tleft:" } }],
				},
				createdAt: now,
			},
		],
	});

	const label = page.locator(".activity-row-toggle .activity-row-label").filter({ hasText: "Read /private/tmp" });
	await expect(label).toBeVisible();
	const layout = await label.evaluate((node) => {
		const row = node.closest(".activity-row-toggle");
		const viewport = node.closest(".chat-scroll-viewport");
		if (!row || !viewport) throw new Error("activity row or chat scroll viewport not found");
		return {
			labelRight: node.getBoundingClientRect().right,
			rowRight: row.getBoundingClientRect().right,
			viewportRight: viewport.getBoundingClientRect().right,
		};
	});

	expect(layout.labelRight).toBeLessThanOrEqual(layout.rowRight);
	expect(layout.rowRight).toBeLessThanOrEqual(layout.viewportRight);
	// The full path stays one hover away.
	await expect(label).toHaveAttribute("title", `Read ${readPath}`);
});
