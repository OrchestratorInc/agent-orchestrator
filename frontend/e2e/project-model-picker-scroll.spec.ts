import { expect, test } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const projectId = "project-model-picker-scroll";

// Chromium's headless default hides scrollbars even when the desktop CSS renders them.
// Exercise the native thumb with real pointer input, without changing the installed browser.
const localChromium = process.env.AO_E2E_CHROMIUM_EXECUTABLE;
test.use({
	viewport: { width: 980, height: 435 },
	launchOptions: {
		...(localChromium ? { executablePath: localChromium, chromiumSandbox: true } : {}),
		ignoreDefaultArgs: ["--hide-scrollbars"],
	},
});

test("renderer: project model results scroll by wheel and scrollbar with search fixed @T0", async ({ page }) => {
	const argon = Array.from({ length: 30 }, (_, index) => ({
		id: `test-router/example-argon-${String(index + 1).padStart(2, "0")}`,
		label: "Example Argon",
		provider: "test-router",
	}));
	await installFakeAgent(page, { projectId, projectName: projectId });
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname.startsWith("/api/v1/agents/readiness")) {
			await route.fulfill({ json: { agents: [agentReadiness("omp", "OMP")] } });
		} else if (pathname === "/api/v1/agents/installers") {
			await route.fulfill({ json: { agents: [] } });
		} else if (pathname === "/api/v1/agents/install-jobs") {
			await route.fulfill({ json: { jobs: [] } });
		} else if (pathname === "/api/v1/agents/auth-plans") {
			await route.fulfill({ json: { plans: [] } });
		} else if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({ json: { status: "ok", project: { id: projectId, agent: "omp", config: { worker: { agent: "omp" } } } } });
		} else if (pathname === "/api/v1/agents/omp/models") {
			await route.fulfill({ json: { agent: "omp", selectionMode: "catalog", models: argon, refreshRecommended: false } });
		} else {
			await route.fulfill({ json: { status: "ok" } });
		}
	});

	await page.goto(`/#/projects/${projectId}/settings`);
	await page.getByRole("button", { name: "Agents", exact: true }).click();
	const trigger = page.getByRole("button", { name: "Worker model", exact: true });
	await trigger.click();
	const menu = page.getByRole("menu");
	const search = menu.getByRole("searchbox", { name: "Search worker model" });
	await search.fill("argon");
	const scroller = menu.locator(".model-picker-scrollbar");
	const first = scroller.getByRole("menuitem").first();
	const last = scroller.getByRole("menuitem").last();
	await expect(first).toHaveText(/argon-01/);
	await expect(last).toHaveText(/argon-30/);
	await expect(last).not.toBeInViewport();
	const bounds = (await scroller.boundingBox())!;
	expect(bounds.y + bounds.height).toBeLessThanOrEqual(page.viewportSize()!.height);
	const searchY = (await search.boundingBox())!.y;
	const scrollTop = () => scroller.evaluate((el) => el.scrollTop);
	const maximum = await scroller.evaluate((el) => el.scrollHeight - el.clientHeight);
	expect(maximum).toBeGreaterThan(0);
	await page.mouse.move(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
	await page.mouse.wheel(0, 440);
	await expect.poll(scrollTop).toBeGreaterThan(0);
	await page.mouse.wheel(0, -5_000);
	await expect.poll(scrollTop).toBe(0);

	// Drag from within the thumb, not just click the track or auto-scroll with a locator.
	const gutter = await scroller.evaluate((el) => el.getBoundingClientRect().width - el.clientWidth);
	expect(gutter).toBeGreaterThanOrEqual(8);
	const thumbX = bounds.x + bounds.width - gutter / 2;
	await page.mouse.move(thumbX, bounds.y + 10);
	await page.mouse.down();
	await page.mouse.move(thumbX, bounds.y + bounds.height - 6, { steps: 12 });
	await page.mouse.up();
	await expect.poll(scrollTop).toBeGreaterThanOrEqual(maximum - 2);
	await expect(last).toBeInViewport({ ratio: 0.95 });
	await expect.poll(async () => (await search.boundingBox())!.y).toBeCloseTo(searchY, 0);
	await expect(menu).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(menu).toHaveCount(0);
	await trigger.click();
	await expect(search).toHaveValue("");
	await search.fill("argon-30");
	await search.press("ArrowDown");
	await expect(menu.getByRole("menuitem", { name: "Example Argon" })).toBeFocused();
	await page.keyboard.press("Enter");
	await expect(menu).toHaveCount(0);
	await trigger.click();
	await expect(menu).toBeVisible();
	await page.mouse.click(4, 4);
	await expect(menu).toHaveCount(0);
	await expect(trigger).toBeVisible();
});
