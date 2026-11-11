import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

test("@P0 navigation gestures are on by default, can be turned off, and survive a reload", async ({ page }) => {
	await installFakeBridge(page);
	await page.goto("/#/settings");
	const toggle = page.getByRole("switch", { name: "Navigation gestures" });
	await expect(toggle).toBeChecked();

	const general = page.getByRole("button", { name: "General", exact: true });
	const updates = page.getByRole("button", { name: "Updates", exact: true });
	const settings = page.getByTestId("settings-page");
	await updates.click();
	await settings.hover();
	await page.mouse.wheel(-160, 0);
	await expect(general).toHaveAttribute("aria-current", "page");

	await toggle.click();
	await expect(toggle).not.toBeChecked();
	await page.reload();
	await page.goto("/#/settings");
	await expect(toggle).not.toBeChecked();
	await updates.click();
	await settings.hover();
	await page.mouse.wheel(-160, 0);
	await page.waitForTimeout(200);
	await expect(updates).toHaveAttribute("aria-current", "page");
});
