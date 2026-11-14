import { expect, test } from "@playwright/test";

test("renderer: terminal attaches on session detail and renders a stream @T0 @TRM", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");
	await expect(page.getByTestId("session-detail")).toBeVisible();

	const terminal = page.getByTestId("session-terminal");
	await expect(terminal).toBeVisible();
	await expect(terminal).toContainText("PASS 18 tests passed");
});
