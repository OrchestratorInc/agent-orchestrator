import { expect, test } from "@playwright/test";

test("opens, selects, and closes standalone shell terminals from the tab strip", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");
	await expect(page.getByRole("button", { name: "New terminal" })).toBeVisible();

	const closeButtons = page.getByRole("button", { name: /^Close terminal / });
	const initialCount = await closeButtons.count();

	await page.getByRole("button", { name: "New terminal" }).click();
	await expect(page.getByRole("menu")).toHaveCount(0);
	await expect(closeButtons).toHaveCount(initialCount + 1);

	const sessionTab = page
		.getByTestId("terminal")
		.getByRole("tab", { name: /^Build screenshot-ready dashboard data/ });
	await sessionTab.click();
	await expect(sessionTab).toHaveAttribute("aria-selected", "true");

	await page.locator("[data-terminal-tab-frame]").filter({ has: closeButtons.last() }).hover();
	await closeButtons.last().click();
	await expect(closeButtons).toHaveCount(initialCount);
});
