import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

for (const variant of ["legacy", "advisory", "unavailable", "handler404"]) {
	test(`Cloud catalog ${variant} keeps safe choices @T0`, async ({ page }, testInfo) => {
		await installFakeBridge(page);
		let desktopCatalogReads = 0;
		await page.route("**/api/v1/**", async (route) => {
			if (new URL(route.request().url()).pathname.includes("/models")) desktopCatalogReads++;
			await route.fulfill({ json: { agent: "codex", selectionMode: "catalog", models: [{ id: "desktop-only", label: "Desktop-only suggestion", isDefault: true }], allowCustom: true } });
		});
		await page.goto(`/e2e/cloud-model-catalog/harness.html?variant=${variant}`);
		await expect(page.getByRole("note")).toContainText("Renderer fixture only");
		const model = page.getByRole("button", { name: "Model", exact: true });
		await expect(model).toHaveText(variant === "unavailable" ? "configured-model" : "Project or agent default");
		await expect(page.getByRole("status")).toContainText(variant === "advisory" ? "not a verified catalog" : variant === "legacy" ? "compatibility suggestions" : "No live worker catalog");
		await page.getByRole("textbox", { name: "Task" }).fill("Check the safe selection");
		await page.getByRole("button", { name: "Start task", exact: true }).click();
		const creates = () => page.evaluate(() => (window as unknown as { catalogCreates: Record<string, unknown>[] }).catalogCreates);
		await expect.poll(creates).toHaveLength(1);
		const payload = (await creates())[0];
		if (variant === "unavailable") expect(payload).toMatchObject({ model: "configured-model", reasoningEffort: "high" });
		else { expect(payload).not.toHaveProperty("model"); expect(payload).not.toHaveProperty("reasoningEffort"); }
		await model.click();
		if (variant === "legacy") await expect(page.getByRole("menuitem", { name: "Desktop-only suggestion" })).toBeVisible();
		else await expect(page.getByRole("menuitem", { name: "Desktop-only suggestion" })).toHaveCount(0);
		if (variant === "legacy") expect(desktopCatalogReads).toBeGreaterThan(0);
		else expect(desktopCatalogReads).toBe(0);
		if (variant === "advisory") {
			await page.getByRole("menuitem", { name: "Borrowed worker model" }).click();
			await page.getByRole("button", { name: "Effort", exact: true }).click();
			await page.getByRole("menuitemradio", { name: "High", exact: true }).click();
		} else {
			await page.getByRole("searchbox").fill("explicit-model");
			await page.getByRole("menuitem", { name: /explicit-model/ }).click();
		}
		await page.getByRole("textbox", { name: "Task" }).fill("Use the explicit selection");
		await page.getByRole("button", { name: "Start task", exact: true }).click();
		await expect.poll(creates).toHaveLength(2);
		expect((await creates())[1]).toMatchObject({ model: variant === "advisory" ? "borrowed-model" : "explicit-model" });
		if (variant === "unavailable" || variant === "advisory") expect((await creates())[1]).toHaveProperty("reasoningEffort", "high");
		await page.screenshot({ path: testInfo.outputPath(`cloud-catalog-${variant}.png`), fullPage: true });
	});
}
