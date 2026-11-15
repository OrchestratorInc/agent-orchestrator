import { expect, test } from "@playwright/test";
import { installFakeAgent, installFakeBridge } from "./support/fake-bridge";

test("renderer: packaged bundle launches and paints @T0 @INS", async ({ page }) => {
	await installFakeBridge(page, { version: "9.9.9-test" });
	await page.goto("/");
	await expect(page.getByText("Jump back right in")).toBeVisible();
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.getByTestId("app-version")).toContainText(/v\d+\.\d+\.\d+/);
});

test("renderer: update settings surface renders (feed/checksum checks are pod) @T0 @INS", async ({ page }) => {
	await installFakeBridge(page, { version: "9.9.9-test" });
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.locator('[data-testid="settings-section"][data-section="updates"]')).toBeVisible();
	await expect(page.getByTestId("app-version")).toContainText("v9.9.9-test");
});

test("renderer: first-run home renders with the app launched @T0 @INS", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Jump back right in")).toBeVisible();
	await expect(page.getByTestId("daemon-status")).toBeAttached();
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();
});

test("renderer: reflects a ready daemon (data dir + config initialized) @T0 @INS", async ({ page }) => {
	await installFakeBridge(page, { daemonState: "ready", daemonPort: 8080 });
	await page.goto("/");
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
});

test("renderer: app version string renders as expected @T0 @INS", async ({ page }) => {
	await installFakeBridge(page, { version: "9.9.9-test" });
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.getByTestId("app-version")).toContainText("v9.9.9-test");
});

test("renderer: reflects the daemon reaching ready on app start @T0 @DMN", async ({ page }) => {
	await installFakeBridge(page, { daemonState: "ready", daemonPort: 8080 });
	await page.goto("/");
	const status = page.getByTestId("daemon-status");
	await expect(status).toHaveAttribute("data-state", "ready");
	await expect(status).toContainText("ready");
});

test("renderer: daemon health reflected with a hydrated board @T0 @DMN", async ({ page }) => {
	await installFakeAgent(page, {
		daemonPort: 8080,
		workers: [{ id: "dmn002", title: "Active worker", status: "working" }],
	});
	await page.goto("/#/projects/fake-proj");
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
	await expect(page.getByTestId("board-session-card").first()).toBeVisible();
});

test("renderer: daemon stop surfaced cleanly with no renderer crash @T0 @DMN", async ({ page }) => {
	await installFakeBridge(page, { daemonState: "stopped" });
	await page.goto("/#/projects/fake-proj");
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "stopped");
	await expect(page.getByTestId("board")).toBeVisible();
});

test("renderer: board state rehydrates after a renderer relaunch @T0 @DMN", async ({ page }) => {
	await installFakeAgent(page, {
		daemonPort: 8080,
		workers: [{ id: "dmn009", title: "Persisted worker", status: "working" }],
	});
	await page.goto("/#/projects/fake-proj");
	const firstCard = page.getByTestId("board-session-card").first();
	await expect(firstCard).toBeVisible();
	const before = await firstCard.textContent();

	await page.reload();
	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
	await expect(page.getByTestId("board-session-card").first()).toBeVisible();
	expect(await page.getByTestId("board-session-card").first().textContent()).toBe(before);
});

test("renderer: board renders all status columns @T0 @BRD", async ({ page }) => {
	await page.goto("/#/projects/ao-demo");
	const columns = page.getByTestId("board-column");
	await expect(columns).toHaveCount(4);
	await expect(columns.nth(0)).toContainText("Building");
	await expect(columns.nth(1)).toContainText("Validating");
	await expect(columns.nth(2)).toContainText("In review");
	await expect(columns.nth(3)).toContainText("Ready");
});

test("renderer: route nav home to board to session detail and back @T0 @BRD", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Jump back right in")).toBeVisible();

	await page.locator('[data-sidebar="menu-button"]').filter({ hasText: "ao-demo" }).first().click();
	await expect(page).toHaveURL(/projects\/ao-demo/);
	await expect(page.getByTestId("board")).toBeVisible();

	await page.getByTestId("board-session-card").first().click();
	await expect(page).toHaveURL(/sessions\//);
	await expect(page.getByTestId("session-detail")).toBeVisible();

	await page.goBack();
	await expect(page).toHaveURL(/projects\/ao-demo$/);
	await expect(page.getByTestId("board")).toBeVisible();
});

test("renderer: global settings page renders all sections @T0 @SET", async ({ page }) => {
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Updates" }).click();
	await expect(page.locator('[data-testid="settings-section"][data-section="updates"]')).toBeVisible();
	await page.getByRole("button", { name: "Help" }).click();
	await expect(page.getByRole("button", { name: "Report a problem" })).toBeVisible();
});
