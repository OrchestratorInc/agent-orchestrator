import { expect, test, type Locator } from "@playwright/test";

// Measure the rendered paint, not class names: transparent currentColor leaves
// SVGs present and "visible" to Playwright while their pixels disappear.
async function contrast(target: Locator, selection = false) {
	return target.evaluate((element, selection) => {
		const canvas = document.createElement("canvas");
		canvas.width = canvas.height = 1;
		const ctx = canvas.getContext("2d")!;
		function rgba(color: string) {
			ctx.clearRect(0, 0, 1, 1);
			ctx.fillStyle = color;
			ctx.fillRect(0, 0, 1, 1);
			return [...ctx.getImageData(0, 0, 1, 1).data].map((v) => v / 255);
		}
		function luminance(color: number[]) {
			const [r, g, b] = color.map((v) => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
			return r! * 0.2126 + g! * 0.7152 + b! * 0.0722;
		}
		const style = getComputedStyle(element, selection ? "::selection" : null);
		const svg = element.querySelector("svg");
		const ink = rgba(selection ? style.color : getComputedStyle(svg!).stroke);
		const background = rgba(style.backgroundColor);
		const a = luminance(ink), b = luminance(background);
		return { alpha: ink[3], ratio: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) };
	}, selection);
}

for (const theme of ["dark", "light"]) {
	for (const transparency of [false, true]) {
		test(`@P0 chat icon and selection contrast: ${theme}, transparency ${transparency}`, async ({ page }, testInfo) => {
			await page.setViewportSize({ width: 1100, height: 850 });
			await page.goto(`/e2e/chat-contrast/harness.html?theme=${theme}&transparency=${transparency}`);
			const cases = [
				["Send and selected text", "Send message", "lucide-arrow-up"],
				["Stop", "Stop turn", "lucide-square"],
				["Continue", "Continue turn", "lucide-play"],
				["Side-chat send", "Send message", "lucide-arrow-up"],
				["Side-chat stop", "Stop turn", "lucide-square"],
				["Message-edit send", "Send edited message", "lucide-arrow-up"],
			];
			for (const [section, label, icon] of cases) {
				const button = page.getByRole("region", { name: section, exact: true }).getByRole("button", { name: label, exact: true });
				await expect(button).toBeEnabled();
				await expect(button.locator(`svg.${icon}`)).toBeVisible();
				for (const hovered of [false, true]) {
					if (hovered) await button.hover();
					await expect.poll(async () => (await contrast(button)).alpha).toBe(1);
					await expect.poll(async () => (await contrast(button)).ratio, {
						message: `${section}: ${hovered ? "hover" : "resting"} icon contrast`,
					}).toBeGreaterThanOrEqual(4.5);
				}
			}
			const editor = page.getByRole("region", { name: "Send and selected text" }).getByRole("combobox");
			await editor.selectText();
			await expect.poll(() => page.evaluate(() => window.getSelection()?.toString())).toBe("Selected text stays readable");
			// Lexical renders nested spans; check the actual selected text node too.
			for (const target of [editor, editor.locator('[data-lexical-text="true"]')]) {
				const paint = await contrast(target, true);
				expect(paint.alpha).toBe(1);
				expect(paint.ratio).toBeGreaterThanOrEqual(4.5);
			}
			await page.mouse.move(0, 0);
			const proof = testInfo.outputPath(`${theme}-transparency-${transparency ? "on" : "off"}.png`);
			await page.screenshot({ path: proof, fullPage: true });
			await testInfo.attach("verified-controls", { path: proof, contentType: "image/png" });
		});
	}
}
