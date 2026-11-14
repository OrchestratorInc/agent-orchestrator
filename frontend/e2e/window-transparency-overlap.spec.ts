import { writeFile } from "node:fs/promises";
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

// Keep captures in test-results, which CI uploads on failure.
async function saveCaptures(captures: [name: string, image: Buffer][]) {
	for (const [name, image] of captures) {
		const path = test.info().outputPath(`${name}.png`);
		await writeFile(path, image);
		await test.info().attach(name, { path, contentType: "image/png" });
	}
}

const setVisibility = (underneath: Locator, hidden: boolean) => underneath.evaluateAll((elements, hidden) => {
	for (const element of elements) {
		if (hidden) (element as HTMLElement).style.visibility = "hidden";
		else (element as HTMLElement).style.removeProperty("visibility");
	}
}, hidden);

// Bracket the capture with the content hidden between two captures with it
// shown. If those two differ, the surface changed on its own (still settling
// after it opened, for example), so the attempt says nothing about what is
// underneath and is retried. If they match but the hidden capture differs, the
// content underneath shows through.
async function expectOccludes(surface: Locator, underneath: Locator, occludes = true) {
	const bounds = await surface.boundingBox();
	expect(bounds).not.toBeNull();
	const cards = await underneath.evaluateAll((elements) => elements.map((element) => {
		const rect = element.getBoundingClientRect();
		return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right };
	}));
	expect(cards.some((rect) => rect.top < bounds!.y + bounds!.height && rect.bottom > bounds!.y
		&& rect.left < bounds!.x + bounds!.width && rect.right > bounds!.x)).toBe(true);
	try {
		const attempts = 5;
		for (let attempt = 1; attempt <= attempts; attempt++) {
			const shown = await surface.screenshot({ animations: "disabled" });
			await setVisibility(underneath, true);
			const hidden = await surface.screenshot({ animations: "disabled" });
			await setVisibility(underneath, false);
			const shownAgain = await surface.screenshot({ animations: "disabled" });
			const captures: [string, Buffer][] = [
				["surface-with-content-underneath", shown],
				["surface-with-content-hidden", hidden],
				["surface-with-content-underneath-again", shownAgain],
			];
			if (shown.equals(shownAgain)) {
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
				}, { before: shown.toString("base64"), after: hidden.toString("base64") });
				if (occludes && changedPixels) await saveCaptures(captures.slice(0, 2));
				const drifted = attempt > 1 ? ` (after ${attempt - 1} attempt${attempt > 2 ? "s" : ""} where the surface changed on its own)` : "";
				if (occludes) expect(changedPixels, `content underneath must not affect the surface's pixels${drifted}`).toBe(0);
				else expect(changedPixels, "the comparison must detect content when clipping is removed").toBeGreaterThan(0);
				return;
			}
			test.info().annotations.push({ type: "surface changed on its own", description: `attempt ${attempt} of ${attempts}` });
			if (attempt === attempts) {
				await saveCaptures(captures);
				throw new Error(`surface kept changing on its own: the captures before and after hiding the content underneath differed in all ${attempts} attempts`);
			}
			await surface.page().waitForTimeout(100);
		}
	} finally {
		await setVisibility(underneath, false);
	}
}

for (const theme of ["dark", "light"]) {
	for (const transparency of [false, true]) {
		test(`@P0 sticky project occludes sessions and the archive opens: ${theme}, transparency ${transparency}`, async ({ page }) => {
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

			// The archive is a modal now, not a sheet over the lanes.
			await page.getByRole("button", { name: "Archived sessions, 8", exact: true }).click();
			const archive = page.getByRole("dialog", { name: "Archived sessions" });
			await expect(archive).toBeVisible();
			await expect(archive.getByText("Archived session 0")).toBeVisible();
			await page.keyboard.press("Escape");
			await expect(archive).toBeHidden();

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
