import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

const columnCard = (column: string, id: string) =>
	`[data-testid="board-column"][data-column="${column}"] [data-session-id="${id}"]`;

test("renderer: card moves columns when its status changes @T0 @BRD", async ({ page }) => {
	await installFakeAgent(page, { workers: [{ id: "mover", title: "Wandering worker", status: "working" }] });
	await page.goto("/#/projects/fake-proj");
	await expect(page.getByTestId("board")).toBeVisible();
	await expect(page.locator(columnCard("building", "mover"))).toBeVisible();
	await expect(page.locator(columnCard("needs_review", "mover"))).toHaveCount(0);

	await page.evaluate(() => window.__aoFakeAgent!.setStatus("mover", "needs_input", "waiting_input"));

	await expect(page.locator(columnCard("needs_review", "mover"))).toBeVisible();
	await expect(page.locator(columnCard("building", "mover"))).toHaveCount(0);
	await expect(page.locator(columnCard("needs_review", "mover"))).toContainText("Input needed");
});

test("renderer: SSE pushes card updates without a manual refresh @T0 @BRD", async ({ page }) => {
	await installFakeAgent(page, { workers: [{ id: "live", title: "Live worker", status: "working" }] });
	await page.goto("/#/projects/fake-proj");
	await expect(page.locator(columnCard("building", "live"))).toContainText("Working");

	await page.evaluate(() => window.__aoFakeAgent!.setStatus("live", "mergeable", "idle"));

	await expect(page.locator(columnCard("ready", "live"))).toBeVisible();
	await expect(page.locator(columnCard("ready", "live"))).toContainText("Ready");
});

test("renderer: narrow card status truncates without overlapping metadata @BRD", async ({ page }) => {
	await installFakeAgent(page, {
		workers: [{ id: "review", title: "Review worker", status: "review_pending", activity: "idle" }],
	});
	await page.route("**/api/v1/usage/sessions**", (route) =>
		route.fulfill({
			contentType: "application/json",
			body: JSON.stringify({
				sessions: [{ sessionId: "review", totalTokens: 24_600_000, incomplete: false }],
			}),
		}),
	);
	await page.goto("/#/projects/fake-proj");

	const card = page.locator(columnCard("validating", "review"));
	const status = card.getByText("Review pending", { exact: true });
	const usage = card.getByText("24.6M tok", { exact: true });
	await expect(usage).toBeVisible();

	await card.evaluate((element) => {
		(element as HTMLElement).style.width = "12rem";
	});

	const statusMetrics = await status.evaluate((element) => ({
		clientWidth: element.clientWidth,
		scrollWidth: element.scrollWidth,
		overflow: getComputedStyle(element).overflow,
		textOverflow: getComputedStyle(element).textOverflow,
		whiteSpace: getComputedStyle(element).whiteSpace,
	}));
	const statusBox = await status.boundingBox();
	const usageBox = await usage.boundingBox();

	expect(statusMetrics.scrollWidth).toBeGreaterThan(statusMetrics.clientWidth);
	expect(statusMetrics).toMatchObject({ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" });
	expect(statusBox).not.toBeNull();
	expect(usageBox).not.toBeNull();
	expect(Math.abs(usageBox!.y - statusBox!.y)).toBeLessThan(2);
	expect(statusBox!.x + statusBox!.width).toBeLessThanOrEqual(usageBox!.x);
});
