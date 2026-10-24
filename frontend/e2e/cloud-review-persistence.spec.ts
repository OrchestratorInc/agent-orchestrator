import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
	await page.goto("/e2e/cloud-review/harness.html");
});

test("@P0 unclicked Cloud Staged default and review progress survive polling and remounts", async ({ page }) => {
	await expect(page.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
	await page.getByRole("checkbox", { name: "Mark app.ts as viewed", exact: true }).click();
	await page.getByRole("button", { name: "Collapse staged.ts", exact: true }).click();
	await page.getByRole("button", { name: "Agent adds unstaged file" }).click();
	await expect(page.getByRole("button", { name: /^Unstaged/ })).toBeVisible({ timeout: 10_000 });
	for (const label of ["Maximize files", "Minimize files"]) {
		await expect(page.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
		await expect(page.getByText("1 of 3 viewed")).toBeVisible();
		await expect(page.getByRole("button", { name: "Expand staged.ts", exact: true })).toBeVisible();
		await page.getByRole("button", { name: label }).click();
	}
	await expect(page.getByRole("button", { name: /^Staged/ })).toHaveAttribute("aria-pressed", "true");
	await expect(page.getByText("1 of 3 viewed")).toBeVisible();
});

test("@P0 Cloud deferred diffs, commit browser, All files and filter survive maximize", async ({ page }) => {
	await page.getByRole("button", { name: "Expand package-lock.json", exact: true }).click();
	await page.getByRole("button", { name: "Load diff", exact: true }).click();
	await page.getByRole("button", { name: "Maximize files" }).click();
	await expect(page.getByRole("button", { name: "Collapse package-lock.json", exact: true })).toBeVisible();
	await expect(page.getByRole("button", { name: "Load diff", exact: true })).toBeHidden();
	await page.getByRole("button", { name: /^Commits/ }).click();
	await page.getByRole("textbox", { name: "Filter files" }).fill("Previous");
	await page.getByRole("button", { name: "Minimize files" }).click();
	await expect(page.getByRole("button", { name: /^Commits/ })).toHaveAttribute("aria-expanded", "true");
	await expect(page.getByRole("button", { name: /Previous work/ })).toBeVisible();
	await page.getByRole("textbox", { name: "Filter files" }).fill("");
	await page.getByRole("tab", { name: "Files", exact: true }).click();
	await page.getByRole("treeitem", { name: /staged.ts/ }).click();
	await page.getByRole("textbox", { name: "Filter files" }).fill("staged");
	await page.getByRole("button", { name: "Maximize files" }).click();
	await expect(page.getByRole("tab", { name: "Files", exact: true })).toHaveAttribute("aria-selected", "true");
	await expect(page.getByRole("textbox", { name: "Filter files" })).toHaveValue("staged");
	await page.getByRole("textbox", { name: "Filter files" }).fill("");
	await expect(page.getByRole("treeitem", { name: /staged.ts/ })).toHaveAttribute("aria-selected", "true");
});

test("@P0 Cloud falls back to main when the active sibling is no longer checked out", async ({ page }) => {
	await page.getByRole("tab", { name: "tools", exact: true }).click();
	await expect(page.getByRole("button", { name: /^Untracked/ })).toHaveAttribute("aria-pressed", "true");
	await page.getByRole("button", { name: "Remove sibling checkout" }).click();
	await expect(page.getByRole("tab", { name: "tools", exact: true })).toBeDisabled({ timeout: 10_000 });
	await expect(page.getByRole("tab", { name: "main", exact: true })).toHaveAttribute("aria-selected", "true");
	await page.getByRole("button", { name: "Maximize files" }).click();
	await expect(page.getByRole("tab", { name: "main", exact: true })).toHaveAttribute("aria-selected", "true");
});
