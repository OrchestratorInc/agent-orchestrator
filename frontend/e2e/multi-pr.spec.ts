import { expect, test } from "@playwright/test";
import { openInspector } from "./support/open-inspector";

test("the inspector rail stacks every PR a session owns, actionable-first", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("button", { name: "Open auth stack" }).click();
	await expect(page).toHaveURL(/sessions\/stacked-auth/);

	const inspector = await openInspector(page);

	await expect(inspector.getByText("Pull requests (3)")).toBeVisible();

	const prSection = inspector.locator("section.inspector-section", { hasText: "Pull requests (3)" });
	const cards = prSection.locator("text=/^PR #\\d+$/");
	await expect(cards).toHaveText(["PR #41", "PR #42", "PR #40"]);
});
