import { expect, test } from "@playwright/test";

for (const mode of ["light", "dark"]) {
	test(`@P0 raised controls preserve caller text colors in ${mode} mode`, async ({ page }) => {
		await page.goto("/");
		await expect(page.locator("#root")).not.toBeEmpty();
		const colors = await page.evaluate((theme) => {
			document.documentElement.dataset.theme = theme;
			const fixture = document.createElement("div");
			fixture.style.cssText = "position: fixed; inset: 100px auto auto 300px; z-index: 9999; display: grid; width: 400px; --button-color: #abcdef; --primary-foreground: #ebdbb2; --secondary-foreground: #3c3836";
			document.body.append(fixture);
			return [
				["text-primary-foreground", "text-primary-foreground"],
				["text-secondary-foreground", "text-secondary-foreground"],
				["text-error", "text-error"],
				["text-secondary-foreground data-[placeholder]:text-muted-foreground", "text-muted-foreground"],
			].map(([classes, reference]) => {
				const button = document.createElement("button");
				button.className = `button-raised ${classes}`;
				button.dataset.placeholder = "";
				button.textContent = classes;
				const expected = document.createElement("span");
				expected.className = reference;
				fixture.append(button, expected);
				return { classes, actual: getComputedStyle(button).color, expected: getComputedStyle(expected).color };
			});
		}, mode);
		for (const { classes, actual, expected } of colors) {
			expect(actual, classes).toBe(expected);
			const button = page.getByRole("button", { name: classes, exact: true });
			await button.hover();
			await expect(button).toHaveCSS("color", expected);
			await page.mouse.down();
			try {
				await expect(button).toHaveCSS("color", expected);
			} finally {
				await page.mouse.up();
			}
		}
	});
}
