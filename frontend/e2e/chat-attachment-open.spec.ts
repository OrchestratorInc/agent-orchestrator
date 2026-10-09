import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

const sessionId = "attachment-open";
const path = ".ao/attachments/attachment-open.png";
const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=";

test("a sent image attachment opens at full size when clicked @T0", async ({ page }) => {
	await installFakeAgent(page, { workers: [{ id: sessionId, title: "Attached image", mode: "chat" }] });
	const now = "2026-09-06T10:00:00Z";
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const url = new URL(route.request().url());
		if (url.pathname.endsWith("/conversation") && route.request().method() === "GET") {
			await route.fulfill({
				json: {
					conversationId: "conversation-attachment",
					sessionId,
					harness: "codex",
					mode: "chat",
					controller: "idle",
					capabilities: ["images"],
					latestSequence: 1,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [{ id: "turn-1", state: "completed", requestedAt: now, startedAt: now, completedAt: now }],
					messages: [
						{
							id: "message-1",
							turnId: "turn-1",
							sequence: 1,
							revision: 0,
							role: "user",
							origin: "human",
							text: `Look at this\n\nAttached files (read these files in the workspace):\n- ${path}`,
							streaming: false,
							createdAt: now,
							content: [],
						},
					],
					activities: [],
					settings: {},
				},
			});
			return;
		}
		if (url.pathname.endsWith("/preview/files/" + path)) {
			await route.fulfill({ contentType: "image/png", body: Buffer.from(png, "base64") });
			return;
		}
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);

	await page.getByRole("button", { name: "Open image: Image 1" }).click();
	const dialog = page.getByRole("dialog");
	await expect(dialog).toBeVisible();
	const full = dialog.getByRole("img", { name: "Image 1" });
	await expect(full).toBeVisible();
	await expect.poll(() => full.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
	await page.screenshot({ path: test.info().outputPath("dialog.png") });
	await page.keyboard.press("Escape");
	await expect(dialog).toBeHidden();
});

test("a pasted image sits inline in the prose, in the composer and in history @T0", async ({ page }) => {
	const id = "inline-image";
	const staged = ".ao/attachments/attachment-inline.png";
	await installFakeAgent(page, { workers: [{ id, title: "Inline image", mode: "chat" }] });
	const now = "2026-09-06T10:00:00Z";
	const sent: string[] = [];
	await page.route(`**/api/v1/sessions/${id}/**`, async (route) => {
		const url = new URL(route.request().url());
		const method = route.request().method();
		if (url.pathname.endsWith("/conversation") && method === "GET") {
			await route.fulfill({
				json: {
					conversationId: "conversation-inline",
					sessionId: id,
					harness: "codex",
					mode: "chat",
					controller: "idle",
					capabilities: ["images"],
					latestSequence: 1,
					oldestSequence: 1,
					hasMoreBefore: false,
					turns: [{ id: "turn-1", state: "completed", requestedAt: now, startedAt: now, completedAt: now }],
					messages: [
						{
							id: "message-1",
							turnId: "turn-1",
							sequence: 1,
							revision: 0,
							role: "user",
							origin: "human",
							text: `The header in ${staged} is too tall\n\nAttached files (read these files in the workspace):\n- ${staged}`,
							streaming: false,
							createdAt: now,
							content: [],
						},
					],
					activities: [],
					settings: {},
				},
			});
			return;
		}
		if (url.pathname.endsWith("/attachments") && method === "POST") {
			await route.fulfill({ json: { paths: [staged] } });
			return;
		}
		if (url.pathname.endsWith("/conversation/messages") && method === "POST") {
			sent.push(route.request().postDataJSON().text);
			await route.fulfill({ json: { turnId: "turn-2", state: "running", duplicate: false } });
			return;
		}
		if (url.pathname.endsWith("/preview/files/" + staged)) {
			await route.fulfill({ contentType: "image/png", body: Buffer.from(png, "base64") });
			return;
		}
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	await page.goto(`/#/projects/fake-proj/sessions/${id}`);

	// History: the path in the prose is a chip that opens the image.
	const bubble = page.locator(".cursor-chat-human-message");
	await expect(bubble.locator("p")).toHaveText("The header in Image 1 is too tall");
	await bubble.locator("p").getByRole("button", { name: "Open image: Image 1" }).click();
	await expect(page.getByRole("dialog")).toBeVisible();
	await page.keyboard.press("Escape");

	// Composer: paste at the caret, keep typing, send.
	const field = page.getByRole("combobox", { name: "Message the agent" });
	await field.click();
	await page.keyboard.type("Make this one");
	await page.evaluate((data) => {
		const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
		const transfer = new DataTransfer();
		transfer.items.add(new File([bytes], "image.png", { type: "image/png" }));
		document.activeElement?.dispatchEvent(new ClipboardEvent("paste", { clipboardData: transfer, bubbles: true, cancelable: true }));
	}, png);
	await expect(field.locator('[data-composer-token="image"]')).toHaveText("Image 1");
	await page.keyboard.type("shorter");
	await expect(field).toHaveText("Make this one Image 1 shorter");
	await page.screenshot({ path: test.info().outputPath("composer.png") });
	await page.keyboard.press("Enter");
	await expect.poll(() => sent).toEqual([
		`Make this one ${staged} shorter\n\nAttached files (read these files in the workspace):\n- ${staged}`,
	]);
});
