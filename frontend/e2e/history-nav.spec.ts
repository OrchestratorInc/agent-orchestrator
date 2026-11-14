import { expect, test } from "@playwright/test";

test("titlebar back/forward arrows traverse history", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();

	await page.getByRole("button", { name: "Open refactor-mux" }).click();
	await expect(page).toHaveURL(/sessions\/refactor-mux/);

	const back = page.getByRole("button", { name: "Go back" });
	const forward = page.getByRole("button", { name: "Go forward" });

	await expect(forward).toBeDisabled();
	await expect(back).toBeEnabled();

	await back.click();
	await expect(page).not.toHaveURL(/sessions\/refactor-mux/);

	await expect(forward).toBeEnabled();
	await forward.click();
	await expect(page).toHaveURL(/sessions\/refactor-mux/);
});
