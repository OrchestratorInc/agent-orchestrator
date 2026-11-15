import { expect, test } from "@playwright/test";

test("renderer: added project appears in the sidebar and board @T0 @PRJ", async ({ page }) => {
	await page.goto("/#/");
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();

	const projectRow = page.locator('[data-sidebar="menu-button"]').filter({ hasText: "ao-demo" }).first();
	await expect(projectRow).toBeVisible();

	await projectRow.click();
	await expect(page).toHaveURL(/projects\/ao-demo/);
	await expect(page.getByTestId("board")).toBeVisible();
	await expect(page.getByTestId("board-session-card").first()).toBeVisible();
});
