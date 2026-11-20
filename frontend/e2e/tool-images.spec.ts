import { expect, test } from "@playwright/test";

test("@P0 tool images decode, zoom, and keep binary data out of expanded text", async ({ page }) => {
	await page.goto("/e2e/tool-images/harness.html");
	for (let index = 1; index <= 7; index++) {
		await page.getByRole("button", { name: `Read fixture ${index}`, exact: true }).click();
	}
	await expect(page.locator("main img")).toHaveCount(4);
	for (const image of await page.locator("main img").all()) {
		await image.scrollIntoViewIfNeeded();
		await expect.poll(() => image.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0)).toBe(true);
		await expect(image).toHaveClass(/max-h-80/);
	}
	await expect(page.locator("main")).not.toContainText("iVBOR");
	await expect(page.getByText("Tool image (image/png, 13 base64 characters)", { exact: true })).toBeVisible();
	await expect(page.locator('[data-sample="4"] pre')).toHaveText("Text-only result");
	await expect(page.locator("[data-tool-diff-row]")).toHaveCount(2);
	await expect(page.locator('[data-sample="6"] pre')).toContainText('"answer": 42');
	await expect(page.locator('[data-sample="6"] pre')).toContainText("resource body");
	await expect(page.locator('[data-sample="6"] pre')).toContainText("CSV source text");
	await page.getByRole("button", { name: /Open image/ }).first().click();
	await expect(page.getByRole("dialog")).toBeVisible();
	await expect(page.getByRole("dialog").getByRole("img")).toBeVisible();
});
