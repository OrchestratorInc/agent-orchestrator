import { expect, test, type Page } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";

const projectId = "model-catalog-proj";

// OpenCode 2 lists every OpenRouter model twice: once under openrouter/ (the
// user's own OpenRouter connection) and once under console-openrouter/ (the
// OpenCode console gateway). The two lists carry the same model name, so a
// picker that shows names alone shows each model twice with nothing to tell
// the rows apart. The catalog is also larger than the picker's row cap, which
// is what pushed the rest of it out of reach.
const providers = ["openrouter", "console-openrouter"];
const models = Array.from({ length: 60 }, (_, index) =>
	providers.map((provider) => ({
		id: `${provider}/vendor/model-${index}`,
		label: `vendor/model-${index}`,
		provider,
	})),
).flat();

function installCatalog(page: Page): void {
	void page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
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
		if (
			pathname === "/api/v1/agents/opencode/models" ||
			// Opening a harness in Settings refreshes its catalog and publishes the
			// refreshed copy, so both endpoints have to answer with the catalog.
			pathname === "/api/v1/agents/opencode/models/refresh"
		) {
			await route.fulfill({
				json: {
					agent: "opencode",
					selectionMode: "catalog",
					models,
					allowCustom: false,
					customModelEntry: "none",
					refreshRecommended: false,
				},
			});
			return;
		}
		await route.fulfill({ json: { status: "ok" } });
	});
}

async function openModelMenu(page: Page) {
	await installFakeAgent(page, { projectId, projectName: projectId });
	installCatalog(page);
	await page.goto(`/#/projects/${projectId}`);
	await page.getByRole("button", { name: "New task" }).first().click();
	const dialog = page.getByRole("dialog");
	await expect(dialog).toBeVisible();
	await dialog.getByRole("button", { name: "Model" }).click();
	// The menu renders in a portal, so it is not inside the dialog subtree.
	const menu = page.getByRole("menu").last();
	await expect(menu.getByRole("menuitem").first()).toBeVisible();
	return menu;
}

test("renderer: the New Task model picker names the provider each model comes from @T0", async ({ page }) => {
	const menu = await openModelMenu(page);

	const rows = menu.getByRole("menuitem");

	// The two providers list the same model name, so the rows are only
	// distinguishable by the provider they name. OpenRouter's console provider
	// ends in the same word, so the plain one is the row without it.
	await expect(rows.nth(0)).toContainText("vendor/model-0");
	await expect(rows.nth(0)).toContainText("openrouter");
	await expect(rows.nth(0)).not.toContainText("console-openrouter");
	await expect(rows.nth(1)).toContainText("vendor/model-0");
	await expect(rows.nth(1)).toContainText("console-openrouter");
});

test("renderer: the New Task model picker says it left models out and reaches them by search @T0", async ({ page }) => {
	const menu = await openModelMenu(page);

	// The picker lists a bounded number of rows. It has to say so, or the
	// models it left out look like models the harness does not offer.
	await expect(menu.getByText(/Showing \d+ of 120 matching models/)).toBeVisible();
	await expect(menu.getByText(/type to narrow/)).toBeVisible();

	// Everything the catalog offers stays reachable: a model past the row cap
	// is found by search and can be picked.
	const search = menu.getByRole("searchbox", { name: "Search model" });
	await search.fill("vendor/model-59");
	const matches = menu.getByRole("menuitem").filter({ hasText: "vendor/model-59" });
	await expect(matches).toHaveCount(2);
	await expect(matches.first()).toContainText("openrouter");
	await expect(matches.last()).toContainText("console-openrouter");

	await matches.first().click();
	await expect(page.getByRole("button", { name: "Model" })).toContainText("vendor/model-59");
});

test("renderer: the Settings model list and the New Task picker offer the same harness models @T0", async ({ page }) => {
	await installFakeAgent(page, { projectId, projectName: projectId });
	installCatalog(page);

	// Settings reads the harness catalog by provider.
	await page.goto("/#/settings");
	await page.getByRole("button", { name: "Harness", exact: true }).click();
	await page.getByRole("button", { name: "Open OpenCode details" }).click();
	await page.getByRole("tab", { name: "Models" }).click();
	const providerMenu = page.getByRole("button", { name: "Provider" });
	await expect(providerMenu).toBeVisible();
	const settingsModels = page.getByRole("list", { name: "Available models" });
	const firstSettingsModel = settingsModels.getByRole("listitem").first();
	await expect(firstSettingsModel).toBeVisible();
	// Settings counts the harness catalog, and the picker counts the same one.
	// The two surfaces must agree on how many models the harness offers.
	await expect(page.getByText(/120 of 120 shown in model pickers/)).toBeVisible();
	const settingsLabel = (await firstSettingsModel.innerText()).trim();

	// The picker reads the same catalog. A model Settings offers must be
	// pickable, or the model exists only in Settings.
	await page.goto(`/#/projects/${projectId}`);
	// The hash change alone leaves the settings view on screen; a reload lands
	// on the project board.
	await page.reload();
	await page.getByRole("button", { name: "New task" }).first().click();
	const dialog = page.getByRole("dialog");
	await expect(dialog).toBeVisible();
	await dialog.getByRole("button", { name: "Model" }).click();
	const menu = page.getByRole("menu").last();
	await expect(menu.getByText(/Showing \d+ of 120 matching models/)).toBeVisible();

	const search = menu.getByRole("searchbox", { name: "Search model" });
	await search.fill(settingsLabel);
	const matches = menu.getByRole("menuitem").filter({ hasText: settingsLabel });
	await expect(matches).toHaveCount(2);
	await expect(matches.first()).toContainText("openrouter");
	await expect(matches.last()).toContainText("console-openrouter");
});
