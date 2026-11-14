import { expect, type Page } from "@playwright/test";

export async function openInspector(page: Page) {
	await page.getByRole("button", { name: "Open inspector panel" }).click();
	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	return inspector;
}
