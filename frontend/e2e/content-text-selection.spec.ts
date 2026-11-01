import { expect, test, type Locator, type Page } from "@playwright/test";
import { openInspector } from "./support/open-inspector";

// #4268: `body { user-select: none }` keeps app chrome inert; read-only
// content must opt back in so a drag selects it and Cmd/Ctrl+C copies it.

test.use({ permissions: ["clipboard-read", "clipboard-write"] });

async function dragAcross(page: Page, target: Locator) {
	await page.evaluate(() => {
		window.getSelection()?.removeAllRanges();
		return navigator.clipboard.writeText("<untouched>");
	});
	await target.hover(); // waits out the inspector's slide-in so the box is final
	const box = (await target.boundingBox())!;
	// Corner to corner, so text that wraps onto several lines is covered whole.
	await page.mouse.move(box.x + 1, box.y + 2);
	await page.mouse.down();
	await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 5 });
	await page.mouse.move(box.x + box.width - 1, box.y + box.height - 2, { steps: 5 });
	await page.mouse.up();
}

async function expectDragCopies(page: Page, target: Locator, text: string) {
	await dragAcross(page, target);
	expect(await page.evaluate(() => window.getSelection()?.toString().trim())).toBe(text);
	await page.keyboard.press("ControlOrMeta+c");
	expect((await page.evaluate(() => navigator.clipboard.readText())).trim()).toBe(text);
}

test("inspector details can be drag-selected and copied @T0", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-ci-failed");
	const inspector = await openInspector(page);
	const text = "The review requirement is satisfied, but failing checks still block this PR.";
	await expectDragCopies(page, inspector.getByText(text, { exact: true }), text);
});

test("Markdown file content and error copy can be drag-selected and copied @T0", async ({ page }) => {
	await page.goto("/e2e/text-selection/harness.html");
	await expectDragCopies(page, page.getByText("Run ao session ls to list sessions."), "Run ao session ls to list sessions.");
	await expectDragCopies(page, page.getByRole("alert"), "Push rejected: branch demo/new-task-flake is protected.");
});

test("dragging across a board card selects nothing and still opens the session @T0", async ({ page }) => {
	await page.goto("/#/projects/ao-demo");
	const card = page.locator('[data-testid="board-session-card"][data-session-id="demo-working"]');
	const title = card.getByText("Build screenshot-ready dashboard data");
	await expect(title).toBeVisible();
	expect(await title.evaluate((el) => getComputedStyle(el).userSelect)).toBe("none");
	await dragAcross(page, title);
	expect(await page.evaluate(() => window.getSelection()?.toString())).toBe("");
	await expect(page).toHaveURL(/\/sessions\/demo-working$/);
});
