import { expect, test } from "@playwright/test";

test("renders the orchestrator-first workbench shell", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByRole("button", { name: "Orchestrator", exact: true })).toBeVisible();
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();
	await expect(page.getByRole("button", { name: "fix-webgl-fallback", exact: true })).toBeVisible();
	await expect(page.getByText("Workers", { exact: true })).toBeVisible();
});

test("deep-links into a worker session", async ({ page }) => {
	await page.goto("/#/workspaces/api-gateway/sessions/refactor-mux");
	await expect(page.getByText("Changed")).toBeVisible();
	await expect(page.getByRole("button", { name: /Commit & Push/ })).toBeVisible();
});

test("drilling into a worker opens its Git review rail", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("button", { name: "refactor-mux", exact: true }).click();
	await expect(page.getByRole("button", { name: /Commit & Push/ })).toBeVisible();
	await expect(page.getByText("internal/mux/terminal_mux.go")).toBeVisible();
});
