import { expect, test } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const projectId = "model-scroll-proj";
const models = Array.from({ length: 30 }, (_, index) => ({
	id: `opencode-go/model-${index + 1}`,
	label: `opencode-go/model-${index + 1}`,
	provider: "opencode-go",
	isDefault: index === 0,
}));

test("renderer: hidden model-menu scrollbar keeps wheel scrolling functional @T0", async ({ page }) => {
	await installFakeAgent(page, { projectId, projectName: projectId });
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const request = route.request();
		const pathname = new URL(request.url()).pathname;
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") {
			await route.fulfill({
				json: {
					agents: [agentReadiness("opencode", "OpenCode")],
				},
			});
			return;
		}
		if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({
				json: {
					status: "ok",
					project: {
						id: projectId,
						agent: "opencode",
						config: { worker: { agent: "opencode" } },
					},
				},
			});
			return;
		}
		if (pathname === "/api/v1/agents/opencode/models") {
			await route.fulfill({
				json: {
					agent: "opencode",
					selectionMode: "catalog",
					models,
					allowCustom: true,
					refreshRecommended: false,
				},
			});
			return;
		}
		await route.fulfill({ json: { status: "ok" } });
	});

	await page.goto(`/#/projects/${projectId}`);
	await page.getByRole("button", { name: "New task" }).first().click();
	await expect(page).toHaveURL(/#\/new/);
	// The new chat page uses the session's own picker: agent, model and effort in one menu.
	await page.getByRole("button", { name: "Agent, model and reasoning effort for the next message" }).click();
	const modelTrigger = page.getByRole("menuitem", { name: /^Model/ });
	await modelTrigger.click();
	const submenuId = await modelTrigger.getAttribute("aria-controls");
	if (!submenuId) throw new Error("Model submenu has no aria-controls");
	const submenu = page.locator(`[id="${submenuId}"]`);

	// The picker and model submenu both scroll; use the submenu controlled by the Model row.
	const scroller = submenu.locator(".model-menu-scroll");
	await expect(scroller).toBeVisible();
	const geometry = await scroller.evaluate((element) => ({
		clientHeight: element.clientHeight,
		scrollHeight: element.scrollHeight,
	}));
	expect(geometry.scrollHeight).toBeGreaterThan(geometry.clientHeight);
	await scroller.hover();
	await page.mouse.wheel(0, 400);

	await expect.poll(() => scroller.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
	await scroller.getByRole("menuitemradio").last().click();
	await expect(modelTrigger).toHaveAttribute("data-state", "closed");
	const trigger = page.getByRole("button", { name: "Agent, model and reasoning effort for the next message" });
	await expect(trigger).toContainText("model-30");
	await modelTrigger.click();
	const search = submenu.getByRole("searchbox", { name: "Search models" });
	// Move off the trigger so its pending hover open cannot outlive keyboard selection.
	await search.hover();
	await search.fill("model-29");
	await expect(page.getByRole("menuitemradio", { name: /model-29/ })).toHaveAttribute("data-search-active", "true");
	await expect(search).toBeFocused();
	await search.press("Enter");
	await expect(trigger).toContainText("model-29");
	await expect(modelTrigger).toHaveAttribute("data-state", "closed");
});
