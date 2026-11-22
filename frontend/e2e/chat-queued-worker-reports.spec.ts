import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Renderer e2e with mocked replacement and report-preserving daemons. Real
// service/provider-boundary coverage lives in TestQueuedEditPreservesWorkerReports.
for (const daemon of ["old", "updated"] as const) test(`queued edits retain hidden reports on the ${daemon} daemon through reload, save and steer @T0`, async ({ page }, testInfo) => {
	test.setTimeout(120_000);
	const sessionId = "queued-worker-reports";
	const now = "2026-11-17T10:00:00Z";
	const human = "Review café changes 👋";
	const suffix = "\n\n<ao-worker-reports>\n[checkpoint] [Worker one](ao://sessions/fake-proj/worker-1)\nInternal checkpoint: suite passed\n\n[done] [Worker two](ao://sessions/fake-proj/worker-2)\npr_created: https://example.com/pr/1044\n</ao-worker-reports>";
	let text = human + suffix;
	let revision = 0;
	let steered = false;
	const edits: { text: string; expectedRevision: number }[] = [];
	await installFakeAgent(page, { workers: [{ id: sessionId, title: "Queued message report boundary", mode: "chat" }] });
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const path = new URL(route.request().url()).pathname;
		if (path.endsWith("/conversation")) {
			await route.fulfill({ json: {
				conversationId: "conversation-reports", sessionId, harness: "codex", mode: "chat",
				controller: "busy", capabilities: ["steer"], latestSequence: 2, oldestSequence: 1,
				hasMoreBefore: false, settings: {}, activities: [],
				turns: [
					{ id: "running", state: "running", providerTurnId: "provider-1", requestedAt: now, startedAt: now },
					...(steered ? [] : [{ id: "queued", state: "queued", requestedAt: now }]),
				],
				messages: [{ id: "queued-message", turnId: steered ? "running" : "queued", sequence: 2, revision,
					role: "user", origin: "human", text, streaming: false, createdAt: now }],
			} });
			return;
		}
		if (path.endsWith("/queue/edit")) {
			const edit = route.request().postDataJSON();
			edits.push(edit);
			expect(edit.text).toBe("Review café changes carefully 👋" + suffix);
			expect(edit.expectedRevision).toBe(revision);
			// The old daemon stores the exact request. The updated daemon
			// strips a matching suffix and appends its own authoritative copy.
			text = daemon === "old" ? edit.text : edit.text.slice(0, -suffix.length) + suffix;
			revision++;
			await route.fulfill({ status: 204 });
			return;
		}
		if (path.endsWith("/turns/queued/steer")) {
			steered = true;
			await route.fulfill({ status: 204 });
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
	const field = page.getByRole("combobox", { name: "Message the agent" });
	await expect(field).toBeVisible({ timeout: 60_000 });
	await field.fill("Keep my independent draft");
	await page.getByRole("button", { name: "Edit queued message" }).click();
	await expect(field).toHaveText(human);
	await expect(page.locator(".queue-dock-row-text")).toHaveAttribute("title", human);
	await expect(page.getByTestId("queued-message-dock")).not.toContainText("Internal checkpoint");
	await field.fill("Review café changes carefully 👋");
	await page.reload();
	await expect(field).toHaveText("Review café changes carefully 👋");
	await page.getByRole("button", { name: "Send message", exact: true }).click();
	await expect.poll(() => edits.length).toBe(1);
	await expect(field).toHaveText("Keep my independent draft");
	await page.getByRole("button", { name: "Edit queued message" }).click();
	await expect(field).toHaveText("Review café changes carefully 👋");
	const screenshot = testInfo.outputPath("queued-edit-reopened.png");
	await page.screenshot({ path: screenshot });
	await testInfo.attach("queued-edit-reopened", { path: screenshot, contentType: "image/png" });
	await page.getByRole("button", { name: "Cancel edit" }).click();
	await page.getByRole("button", { name: "Steer this queued message into the running turn" }).click();
	await expect.poll(() => steered).toBe(true);
	await page.reload();
	await expect(page.getByRole("button", { name: "Edit queued message" })).toHaveCount(0);
	await expect(field).toHaveText("Keep my independent draft");
	expect(text).toBe("Review café changes carefully 👋" + suffix);
	expect(edits).toHaveLength(1);
});
