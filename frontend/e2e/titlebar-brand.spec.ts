import { expect, test, type Locator, type Page } from "@playwright/test";

test.use({
	userAgent:
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
});

const brand = (page: Page) => page.getByText("Agent Orchestrator", { exact: true });

function overlaps(a: { x: number; y: number; width: number; height: number }, b: typeof a) {
	return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

async function isTruncated(span: Locator) {
	return span.evaluate((el) => el.scrollWidth > el.clientWidth + 1);
}

async function expectBrandClearsCluster(page: Page) {
	const cluster = page.locator(".titlebar-nav");
	await expect(cluster).toBeVisible();
	const span = brand(page);
	await expect(span).toBeVisible();

	const clusterBox = await cluster.boundingBox();
	const brandBox = await span.boundingBox();
	expect(clusterBox).not.toBeNull();
	expect(brandBox).not.toBeNull();

	expect(overlaps(brandBox!, clusterBox!)).toBe(false);
	expect(await isTruncated(span)).toBe(false);
}

test("home board route: brand clears the macOS titlebar cluster and stays readable", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();
	await expectBrandClearsCluster(page);
});

test("project board route: brand clears the macOS titlebar cluster and stays readable", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();

	await page.locator('[data-sidebar="menu-button"]').filter({ hasText: "api-gateway" }).first().click();
	await expect(page.locator('[aria-current="page"]')).toBeVisible();

	await expectBrandClearsCluster(page);
});

test("brand stays put and readable when navigating board → session", async ({ page }) => {
	await page.goto("/");
	await expect(page.getByText("Projects", { exact: true })).toBeVisible();

	const boardBrandBox = await brand(page).boundingBox();
	expect(boardBrandBox).not.toBeNull();

	await page.getByRole("button", { name: "Open Split terminal mux responsibilities" }).click();
	await expect(page.getByRole("button", { name: "Board", exact: true })).toBeVisible();

	const sessionBrandBox = await brand(page).boundingBox();
	expect(sessionBrandBox).not.toBeNull();
	expect(Math.abs(sessionBrandBox!.x - boardBrandBox!.x)).toBeLessThanOrEqual(1);
	expect(Math.abs(sessionBrandBox!.y - boardBrandBox!.y)).toBeLessThanOrEqual(1);
	await expectBrandClearsCluster(page);
});
