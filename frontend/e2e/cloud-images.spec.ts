import { expect, test } from "@playwright/test";
test("Cloud image composers preserve IDs and TUI input does not submit @T0", async ({ page }) => {
	await page.goto("/e2e/performance/cloud-images.html");
	await page.getByRole("button", { name: "Load example into composers" }).click();
	await expect(page.getByRole("img", { name: "layout.png" })).toHaveCount(2);
	const terminal = page.getByRole("textbox", { name: "Terminal pending input" });
	await expect(terminal).toHaveValue(/^'\.ao\/attachments\/image-[0-9a-f-]+\.png' $/);
	expect(await terminal.inputValue()).not.toMatch(/[\r\n]/);
	if (process.env.AO_CLOUD_IMAGE_SCREENSHOT)
		await page.screenshot({ path: process.env.AO_CLOUD_IMAGE_SCREENSHOT, fullPage: true });
	await page.getByRole("button", { name: "Start task" }).click();
	await expect(page.getByRole("status").filter({ hasText: "Task references" })).toHaveText(
		"Task references 1 verified image ID.",
	);
	await page.getByRole("button", { name: "Send message" }).click();
	await expect(page.getByRole("status").filter({ hasText: "Message references" })).toHaveText(
		"Message references 1 image ID.",
	);
});
