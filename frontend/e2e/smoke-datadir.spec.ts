import { expect, test } from "@playwright/test";
import { installFakeAgent, installFakeBridge } from "./support/fake-bridge";

test("renderer: reflects daemon data-dir readiness @P0 @DATADIR", async ({ page }) => {
	await installFakeAgent(page, {
		daemonPort: 8080,
		workers: [{ id: "datadir", title: "Persisted worker", status: "working" }],
	});
	await page.goto("/#/projects/fake-proj");

	await expect(page.getByTestId("daemon-status")).toHaveAttribute("data-state", "ready");
	await expect(page.getByTestId("board")).toBeVisible();
	await expect(page.getByTestId("board-session-card").first()).toBeVisible();
});

test("renderer: surfaces a not-ready data dir without crashing @P0 @DATADIR", async ({ page }) => {
	await installFakeBridge(page, { daemonState: "starting" });
	await page.goto("/#/projects/ao-demo");
	await expect(page.getByTestId("daemon-status")).not.toHaveAttribute("data-state", "ready");
	await expect(page.getByTestId("board")).toBeVisible();
});
