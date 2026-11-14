import { expect, test } from "@playwright/test";

test("topbar button collapses and reopens the inspector rail", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");

	const inspector = page.locator("#inspector");
	await expect(page.getByRole("button", { name: "Open inspector panel" })).toBeVisible();
	await expect(inspector).toBeHidden();

	await page.getByRole("button", { name: "Open inspector panel" }).click();
	await expect(inspector).toBeVisible();

	await page.getByRole("button", { name: "Close inspector panel" }).click();
	await expect(inspector).toBeHidden();
});
