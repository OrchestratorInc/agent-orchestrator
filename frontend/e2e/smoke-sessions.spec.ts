import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

const card = (id: string) => `[data-testid="board-session-card"][data-session-id="${id}"]`;
const columnCard = (column: string, id: string) =>
	`[data-testid="board-column"][data-column="${column}"] [data-session-id="${id}"]`;

test("renderer: new session card appears in the spawning/working state @T0 @SES", async ({ page }) => {
	await installFakeAgent(page);
	await page.goto("/#/projects/fake-proj");
	await expect(page.getByTestId("board")).toBeVisible();
	await expect(page.locator(card("fake-spawn"))).toHaveCount(0);

	await page.evaluate(() =>
		window.__aoFakeAgent!.createWorker({ id: "fake-spawn", title: "Spawning worker", activity: "exited" }),
	);

	await expect(page.locator(columnCard("building", "fake-spawn"))).toBeVisible();
	await expect(page.locator(card("fake-spawn"))).toContainText("Working");
});
