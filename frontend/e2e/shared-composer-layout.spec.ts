import { expect, test } from "@playwright/test";

for (const width of [960, 390]) {
 test(`expanded queue stays above the shared draft at ${width}px @T0`, async ({ page }) => {
	await page.setViewportSize({ width, height: 760 });
	await page.emulateMedia({ reducedMotion: "reduce" });
	await page.goto("/e2e/shared-composer/harness.html");
	const queue = page.getByTestId("queued-message-dock");
	await expect(queue).toHaveAttribute("data-expanded", "true");
	const notice = page.locator("[data-shared-composer-notice]");
	const form = page.locator("form.cursor-chat-composer");
	const draft = page.getByRole("region", { name: "Alice Example's draft" });
	await expect(draft).toContainText("Keep this live draft fully visible");
	const [q, n, d, f] = await Promise.all([queue.boundingBox(), notice.boundingBox(), draft.boundingBox(), form.boundingBox()]);
	expect(q && n && d && f).toBeTruthy();
	expect(q!.y + q!.height).toBeLessThanOrEqual(n!.y + 1);
	expect(n!.y + n!.height).toBeLessThanOrEqual(f!.y + 1);
	expect(d!.y).toBeGreaterThanOrEqual(n!.y);
	expect(d!.y + d!.height).toBeLessThanOrEqual(n!.y + n!.height + 1);
	await expect(page.getByLabel("Message the agent")).toHaveAttribute("contenteditable", "false");
	await expect(page.getByLabel("Message the agent")).toHaveAttribute("aria-placeholder", "");
	expect(await form.evaluate(el => getComputedStyle(el).borderTopLeftRadius)).toBe("0px");
	await page.getByRole("button", { name: "Request turn" }).click();
	await expect(notice.getByRole("status")).toContainText("You're number 1 in line");
	await page.getByRole("button", { name: "Leave line" }).click();
	await expect(page.getByRole("button", { name: "Request turn" })).toBeVisible();
	await page.getByRole("button", { name: "Show only me" }).click();
	await expect(notice).toHaveCount(0);
	await expect(page.getByLabel("Message the agent")).toHaveAttribute("contenteditable", "true");
	expect(await form.evaluate(el => Number.parseFloat(getComputedStyle(el).borderTopLeftRadius))).toBeGreaterThan(0);
 });
}
