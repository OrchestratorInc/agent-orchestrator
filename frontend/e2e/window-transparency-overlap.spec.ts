import { expect, test, type Locator } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Platform detection can prefer Chromium's client hints over navigator.platform.
// Use a macOS user agent so transparency is enabled on Linux CI too.
test.use({ userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36" });

// Check actual paint with production components/CSS. A transparent sticky
// header or archive can still pass visibility and geometry assertions.
async function expectSurfaceTransparency(target: Locator, transparent: boolean) {
	const alpha = await target.evaluate((element) => {
		const context = document.createElement("canvas").getContext("2d")!;
		context.fillStyle = getComputedStyle(element).backgroundColor;
		context.fillRect(0, 0, 1, 1);
		return context.getImageData(0, 0, 1, 1).data[3];
	});
	expect(alpha).toBe(transparent ? 0 : 255);
}

async function expectOccludes(surface: Locator, underneath: Locator, occludes = true) {
	const bounds = await surface.boundingBox();
	expect(bounds).not.toBeNull();
	const cards = await underneath.evaluateAll((elements) => elements.map((element) => {
		const rect = element.getBoundingClientRect();
		return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right };
	}));
	expect(cards.some((rect) => rect.top < bounds!.y + bounds!.height && rect.bottom > bounds!.y
		&& rect.left < bounds!.x + bounds!.width && rect.right > bounds!.x)).toBe(true);
	const before = await surface.screenshot({ animations: "disabled" });
	try {
		await underneath.evaluateAll((elements) => {
			for (const element of elements) (element as HTMLElement).style.visibility = "hidden";
		});
		const after = await surface.screenshot({ animations: "disabled" });
		const changedPixels = await surface.evaluate(async (_, images) => {
			const decode = async (png: string) => {
				const image = new Image();
				image.src = `data:image/png;base64,${png}`;
				await image.decode();
				const canvas = document.createElement("canvas");
				canvas.width = image.width; canvas.height = image.height;
				const context = canvas.getContext("2d")!;
				context.drawImage(image, 0, 0);
				return context.getImageData(0, 0, canvas.width, canvas.height);
			};
			const [before, after] = await Promise.all([decode(images.before), decode(images.after)]);
			if (before.width !== after.width || before.height !== after.height) throw new Error("surface dimensions changed");
			let changed = 0;
			for (let i = 0; i < before.data.length; i += 4) {
				// Repainting the hidden layer can change icon/edge antialiasing by
				// up to 5/255 per channel (under 2%). Every pixel must stay within that tolerance.
				if ([0, 1, 2, 3].some((channel) => Math.abs(before.data[i + channel] - after.data[i + channel]) > 5)) changed++;
			}
			return changed;
		}, { before: before.toString("base64"), after: after.toString("base64") });
		if (occludes && changedPixels) {
			await test.info().attach("occlusion-before", { body: before, contentType: "image/png" });
			await test.info().attach("occlusion-after", { body: after, contentType: "image/png" });
		}
		if (occludes) expect(changedPixels, "content underneath must not affect the surface's pixels").toBe(0);
		else expect(changedPixels, "the comparison must detect content when clipping is removed").toBeGreaterThan(0);
	} finally {
		await underneath.evaluateAll((elements) => {
			for (const element of elements) (element as HTMLElement).style.removeProperty("visibility");
		});
	}
}

for (const theme of ["dark", "light"]) {
	for (const transparency of [false, true]) {
		test(`@P0 sticky project and archive occlude sessions: ${theme}, transparency ${transparency}`, async ({ page }) => {
			await page.setViewportSize({ width: 1400, height: 650 });
			await page.emulateMedia({ reducedMotion: "reduce" });
			await installFakeAgent(page, {
				platform: "MacIntel",
				expandProject: true,
				workers: [
					...Array.from({ length: 12 }, (_, i) => ({ id: `active-${i}`, title: `Active session ${i}`, status: "working" })),
					...Array.from({ length: 8 }, (_, i) => ({ id: `archived-${i}`, title: `Archived session ${i}`, status: "terminated" })),
				],
			});
			await page.addInitScript(({ theme, transparency }) => {
				localStorage.setItem("ao.theme", theme);
				localStorage.setItem("ao.windowTranslucency", String(transparency));
			}, { theme, transparency });
			await page.goto("/#/projects/fake-proj");
			await expect(page.getByTestId("board")).toBeVisible();
			await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
			if (transparency) await expect(page.locator("html")).toHaveAttribute("data-window-translucent", "true");

			const toggle = page.getByRole("button", { name: "Archive, 8 sessions", exact: true });
			const archive = toggle.locator("../..");
			// Light keeps the center panel solid in a translucent window (only the
			// sidebar chrome is frosted), so the board's archive is opaque there.
			const centerClear = transparency && theme === "dark";
			await expectSurfaceTransparency(archive, centerClear);
			await toggle.click();
			await expect(toggle).toHaveAttribute("aria-expanded", "true");
			await expect(page.getByRole("list", { name: "Archived sessions" })).toBeVisible();
			await expectSurfaceTransparency(archive, centerClear);
			await expectOccludes(archive, page.locator('[data-testid="board-column"] [data-session-id]'));
			if (centerClear) {
				// A negative control keeps the pixel tolerance honest: removing the
				// production clip must expose the active cards through the clear archive.
				const lanes = page.locator('[style*="clip-path"]').filter({ has: page.getByTestId("board-column") });
				await expect(lanes).toHaveCount(1);
				const clip = await lanes.evaluate((element) => (element as HTMLElement).style.clipPath);
				try {
					await lanes.evaluate((element) => { (element as HTMLElement).style.clipPath = "none"; });
					await expectOccludes(archive, page.locator('[data-testid="board-column"] [data-session-id]'), false);
				} finally {
					await lanes.evaluate((element, clip) => { (element as HTMLElement).style.clipPath = clip; }, clip);
				}
			}
			await toggle.click();
			await expect(toggle).toHaveAttribute("aria-expanded", "false");

			const scroller = page.getByTestId("sidebar-projects-scroller");
			await scroller.getByRole("button", { name: /Show .* more/ }).click();
			await scroller.evaluate((element) => { element.scrollTop = 48; });
			const project = page.locator('[data-project-drag-row][data-project-id="fake-proj"]');
			await expectSurfaceTransparency(project, transparency);
			await expectOccludes(project, scroller.locator("[data-session-row]"));
			// The shell itself must still let the native vibrancy show through.
			if (transparency) await expect(page.locator("body")).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
		});
	}
}
