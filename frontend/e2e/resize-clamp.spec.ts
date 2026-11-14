import { expect, test, type Page } from "@playwright/test";
import { openInspector } from "./support/open-inspector";

test("@P0 sidebar content width stays fixed when opening a project and returning home", async ({ page }) => {
	await page.goto("/");
	const project = page.locator('[data-sidebar="menu-button"]').filter({ hasText: "ao-demo" }).first();
	await expect(project).toBeVisible();
	const content = page.locator('[data-slot="sidebar-inner"]');
	const width = await content.evaluate((el) => el.getBoundingClientRect().width);

	await project.click();
	await expect(page).toHaveURL(/projects\/ao-demo/);
	expect(await content.evaluate((el) => el.getBoundingClientRect().width)).toBe(width);

	await page.goBack();
	await expect(page).not.toHaveURL(/projects\//);
	expect(await content.evaluate((el) => el.getBoundingClientRect().width)).toBe(width);
});

async function dragPointer(page: Page, from: { x: number; y: number }, to: { x: number; y: number }) {
	await page.mouse.move(from.x, from.y);
	await page.mouse.down();
	await page.mouse.move(to.x, to.y, { steps: 8 });
	await page.mouse.up();
}

test("sidebar drag stops at its minimum width instead of collapsing", async ({ page }) => {
	await page.goto("/");

	const sidebar = page.locator('[data-slot="sidebar"]');
	await expect(sidebar).toHaveAttribute("data-state", "expanded");

	await page.waitForFunction(() =>
		Array.from(document.querySelectorAll("[data-sidebar-brand]")).some((el) => el.getBoundingClientRect().width > 0),
	);
	const handle = page.getByTestId("resize-handle");
	const box = await handle.boundingBox();
	if (!box) throw new Error("sidebar resize handle not visible");
	const readWidth = () =>
		page.evaluate(() =>
			document.querySelector<HTMLElement>('[data-slot="sidebar-gap"]')?.style.getPropertyValue("--ao-sidebar-w"),
		);

	const y = box.y + box.height / 2;
	await dragPointer(page, { x: box.x + box.width / 2, y }, { x: 0, y });
	await expect(sidebar).toHaveAttribute("data-state", "expanded");
	const floor = await readWidth();
	expect(Number.parseFloat(floor ?? "")).toBeGreaterThanOrEqual(140);
	const box2 = await handle.boundingBox();
	if (!box2) throw new Error("sidebar resize handle not visible after drag");
	await dragPointer(page, { x: box2.x + box2.width / 2, y }, { x: 0, y });
	expect(await readWidth()).toBe(floor);

	await page.keyboard.press("ControlOrMeta+b");
	await expect(sidebar).toHaveAttribute("data-state", "collapsed");
	const stored = await page.evaluate(() => window.localStorage.getItem("ao-sidebar-w"));
	expect([null, String(Number.parseFloat(floor ?? ""))]).toContain(stored);
});

test("inspector drag stops at minSize instead of collapsing; buttons still toggle", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");

	const inspector = await openInspector(page);

	const handle = page.getByTestId("inspector-resize-handle");
	const handleBox = await handle.boundingBox();
	const groupBox = await page.locator("#session-workspace").boundingBox();
	if (!handleBox || !groupBox) throw new Error("session split not visible");
	const y = handleBox.y + handleBox.height / 2;

	await dragPointer(
		page,
		{ x: handleBox.x + handleBox.width / 2, y },
		{ x: groupBox.x + groupBox.width - 2, y },
	);

	await expect(inspector).toBeVisible();
	const inspectorBox = await inspector.boundingBox();
	if (!inspectorBox) throw new Error("inspector hidden after drag");
	expect(inspectorBox.width).toBeGreaterThanOrEqual(340);

	await page.getByRole("button", { name: "Close inspector panel" }).click();
	await expect(inspector).toBeHidden();

	await page.getByRole("button", { name: "Open inspector panel" }).click();
	await expect(inspector).toBeVisible();

	const reopenedHandleBox = await handle.boundingBox();
	if (!reopenedHandleBox) throw new Error("handle not visible after reopen");
	await dragPointer(
		page,
		{ x: reopenedHandleBox.x + reopenedHandleBox.width / 2, y },
		{ x: groupBox.x + groupBox.width - 2, y },
	);
	await expect(inspector).toBeVisible();
});
