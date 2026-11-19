import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

for (const mode of ["light", "dark"]) {
	test(`@P0 topbar controls preserve affordances in ${mode} mode`, async ({ page }) => {
		await page.setViewportSize({ width: 1800, height: 900 });
		await installFakeAgent(page, { projectId: "ao-demo", workers: [{ id: "demo-working", title: "Topbar review", status: "working" }] });
		await page.addInitScript((theme) => {
			Object.defineProperty(navigator, "platform", { get: () => "MacIntel", configurable: true });
			Object.defineProperty(navigator, "userAgentData", { get: () => ({ platform: "macOS" }), configurable: true });
			localStorage.setItem("ao.theme", theme);
		}, mode);
		await page.route("**/api/v1/projects/ao-demo/cues", (route) => route.fulfill({ json: { cues: [] } }));
		await page.goto("/#/projects/ao-demo/sessions/demo-working");
		await expect(page.locator(".workspace-topbar-actions").first()).toBeVisible({ timeout: 20_000 });
		await page.evaluate((theme) => { document.documentElement.dataset.theme = theme; }, mode);
		const actions = page.locator(".center-panel-shell--floating-topbar .workspace-topbar-actions");
		const button = actions.locator("button.group\\/button:not(:disabled)").first();
		await expect(button).toBeVisible();
		if (mode === "light") {
			const foreground = await button.evaluate((element) => {
				const reference = document.createElement("span");
				reference.style.color = "var(--color-foreground)";
				element.append(reference);
				const color = getComputedStyle(reference).color;
				reference.remove();
				return color;
			});
			await expect(button).toHaveCSS("color", foreground);
		}
		await expect(button).toHaveCSS("transition-property", /gap/);
		await expect(button).toHaveCSS("transition-property", /padding-inline/);
		const run = actions.getByRole("button", { name: "Run a cue", exact: true });
		await run.hover();
		await expect(page.getByRole("menu")).toBeVisible();
		const hoverFill = await run.evaluate((element) => getComputedStyle(element, "::before").backgroundColor);
		const expectedHover = await run.evaluate((element) => {
			const reference = document.createElement("span");
			reference.style.backgroundColor = "var(--color-interactive-hover)";
			element.append(reference);
			const color = getComputedStyle(reference).backgroundColor;
			reference.remove();
			return color;
		});
		expect(hoverFill).toBe(expectedHover);
		await page.getByRole("menu").hover();
		expect(await run.evaluate((element) => getComputedStyle(element, "::before").opacity)).toBe("0");
		await page.keyboard.press("Escape");
		const editor = actions.getByRole("button", { name: "Open in Cursor", exact: true });
		const editorGroup = editor.locator("../..");
		await editor.click();
		await page.mouse.move(0, 0);
		await expect.poll(() => editorGroup.evaluate((element) => getComputedStyle(element, "::before").opacity)).toBe("0");
		await page.keyboard.press("Tab");
		await expect(actions.getByRole("button", { name: "Open workspace options" })).toBeFocused();
		await expect.poll(() => editorGroup.evaluate((element) => getComputedStyle(element, "::before").opacity)).toBe("1");
		await button.evaluate((element) => {
			const clone = element.cloneNode(true) as HTMLButtonElement;
			clone.disabled = true;
			clone.dataset.testid = "disabled-topbar-fixture";
			element.parentElement!.append(clone);
		});
		const disabled = page.getByTestId("disabled-topbar-fixture");
		await expect(disabled).toHaveCSS("cursor", "not-allowed");
		await expect(disabled).toHaveCSS("pointer-events", "auto");
		await expect(disabled).toHaveCSS("opacity", "0.35");
		await disabled.hover();
		expect(await disabled.evaluate((element) => getComputedStyle(element, "::before").display)).toBe("none");
	});
}

for (const mode of ["light", "dark"]) {
	for (const palette of ["vercel", "gruvbox", "orchestrate"]) {
		test(`@P0 filled topbar actions retain ${palette} text colors in ${mode} mode`, async ({ page }) => {
			await page.setViewportSize({ width: 1800, height: 900 });
			await installFakeAgent(page, { projectId: "topbar-palette" });
			await page.addInitScript(({ mode, palette }) => {
				Object.defineProperty(navigator, "platform", { get: () => "MacIntel", configurable: true });
				Object.defineProperty(navigator, "userAgentData", { get: () => ({ platform: "macOS" }), configurable: true });
				localStorage.setItem("ao.theme", mode);
				localStorage.setItem("ao.theme-style", palette);
			}, { mode, palette });
			await page.goto("/#/projects/topbar-palette/sessions/topbar-palette-orchestrator");
			const action = page.locator(".center-panel-shell--floating-topbar .workspace-topbar-actions").getByRole("button", { name: "New task", exact: true });
			await expect(action).toBeEnabled({ timeout: 20_000 });
			await expect(page.locator("html")).toHaveAttribute("data-theme", mode);
			if (palette !== "orchestrate") await expect(page.locator("html")).toHaveAttribute("data-style-theme", palette);
			const expected = await action.evaluate((element) => {
				const reference = document.createElement("span");
				reference.style.color = "var(--primary-foreground)";
				element.append(reference);
				const color = getComputedStyle(reference).color;
				reference.remove();
				return color;
			});
			await expect(action).toHaveCSS("color", expected);
			await action.hover();
			await expect(action).toHaveCSS("color", expected);
			await page.mouse.down();
			try {
				await expect(action).toHaveCSS("color", expected);
			} finally {
				await page.mouse.move(0, 0);
				await page.mouse.up();
			}
		});
	}
}
