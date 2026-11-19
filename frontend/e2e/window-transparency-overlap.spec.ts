import { writeFile } from "node:fs/promises";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";
import { THEMES } from "../src/renderer/lib/theme-palettes";

// Check actual paint with production components/CSS. A transparent sticky
// header or archive can still pass visibility and geometry assertions.
async function colorAlpha(target: Locator, token?: string) {
	return target.evaluate((element, token) => {
		// Resolve inherited tokens through a real CSS color property. Canvas color
		// parsing alone does not resolve var() or the sidebar's overlay recipe.
		const probe = token ? document.createElement("span") : null;
		if (probe) {
			probe.style.cssText = `position:absolute;pointer-events:none;background-color:var(${token})`;
			element.appendChild(probe);
		}
		const color = getComputedStyle(probe ?? element).backgroundColor;
		probe?.remove();
		const context = document.createElement("canvas").getContext("2d")!;
		context.fillStyle = color;
		context.fillRect(0, 0, 1, 1);
		return context.getImageData(0, 0, 1, 1).data[3];
	}, token);
}

async function expectSurfaceTransparency(target: Locator, transparent: boolean) {
	expect(await colorAlpha(target)).toBe(transparent ? 0 : 255);
}

const macUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36";
type ThemeMode = "dark" | "light";

async function preparePage(page: Page, platform: string, themeStyle: string, theme: ThemeMode, transparency: boolean) {
	await page.setViewportSize({ width: 1400, height: 650 });
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		platform,
		expandProject: true,
		workers: [
			...Array.from({ length: 12 }, (_, i) => ({ id: `active-${i}`, title: `Active session ${i}`, status: "working" })),
			...Array.from({ length: 8 }, (_, i) => ({ id: `archived-${i}`, title: `Archived session ${i}`, status: "terminated" })),
		],
	});
	await page.addInitScript(({ theme, themeStyle, transparency }) => {
		localStorage.setItem("ao.theme", theme);
		localStorage.setItem("ao.theme-style", themeStyle);
		localStorage.setItem("ao.windowTranslucency", String(transparency));
	}, { theme, themeStyle, transparency });
	await page.goto("/#/projects/fake-proj");
	await expect(page.getByTestId("board")).toBeVisible({ timeout: 30_000 });
	await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
}

async function sidebarPaintStates(page: Page, theme: ThemeMode) {
	await page.bringToFront();
	const row = page.locator("[data-session-row]").filter({ hasText: "Active session 0" }).first();
	const readPaint = () => row.evaluate((element, theme) => {
		const context = document.createElement("canvas").getContext("2d")!;
		const rgba = (color: string) => {
			context.clearRect(0, 0, 1, 1);
			context.fillStyle = color;
			context.fillRect(0, 0, 1, 1);
			return Array.from(context.getImageData(0, 0, 1, 1).data);
		};
		const probe = document.createElement("span");
		element.appendChild(probe);
		const resolve = (value: string) => {
			probe.style.backgroundColor = value;
			return rgba(getComputedStyle(probe).backgroundColor);
		};
		const tokens = Object.fromEntries([
			"--foreground", "--muted-foreground", "--color-text-passive", "--color-bg-sidebar",
			"--color-bg-secondary", "--color-bg-tertiary", "--muted", "--secondary", "--accent", "--sidebar-accent",
		].map((token) => [token, resolve(`var(${token})`)]));
		const sidebar = getComputedStyle(document.documentElement).getPropertyValue("--sidebar");
		const expectedTint = resolve(theme === "dark"
			? `color-mix(in srgb, ${sidebar} 70%, transparent)`
			: `color-mix(in srgb, color-mix(in srgb, ${sidebar}, white 75%) 72%, transparent)`);
		probe.remove();
		const highlight = getComputedStyle(element.querySelector("[data-nav-row-highlight]")!);
		return {
			tint: rgba(getComputedStyle(document.querySelector(".app-shell-root")!, "::before").backgroundColor),
			expectedTint,
			tokens,
			ink: rgba(getComputedStyle(element.querySelector("[data-session-name]")!).color),
			highlight: rgba(highlight.backgroundColor),
			highlightOpacity: Number(highlight.opacity),
		};
	}, theme);
	await page.mouse.move(1390, 640);
	const idle = await readPaint();
	await row.hover();
	const hover = await readPaint();
	await page.mouse.move(1390, 640);
	await page.keyboard.press("Escape");
	await expect(row).not.toHaveAttribute("data-preview-open", "");
	await row.locator('button[aria-keyshortcuts="F2"]').focus();
	await page.keyboard.press("Tab");
	await expect(row.locator(":focus-visible")).toBeVisible();
	const focus = await readPaint();
	await row.locator('button[aria-keyshortcuts="F2"]').click();
	await expect(row.locator('[aria-current="page"]')).toBeVisible();
	await page.mouse.move(1390, 640);
	const active = await readPaint();
	return { idle, hover, focus, active };
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
	// Locator screenshots round fractional bounds outward. At 125% Windows DPI
	// that captures pixels below a 32.5px header, outside the occlusion contract.
	const x = Math.ceil(bounds!.x);
	const y = Math.ceil(bounds!.y);
	const clip = { x, y, width: Math.floor(bounds!.x + bounds!.width) - x, height: Math.floor(bounds!.y + bounds!.height) - y };
	const cards = await underneath.evaluateAll((elements) => elements.map((element) => {
		const rect = element.getBoundingClientRect();
		return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right };
	}));
	expect(cards.some((rect) => rect.top < bounds!.y + bounds!.height && rect.bottom > bounds!.y
		&& rect.left < bounds!.x + bounds!.width && rect.right > bounds!.x)).toBe(true);
	try {
		const attempts = 5;
		for (let attempt = 1; attempt <= attempts; attempt++) {
			const shown = await surface.page().screenshot({ clip, animations: "disabled" });
			await setVisibility(underneath, true);
			const hidden = await surface.page().screenshot({ clip, animations: "disabled" });
			await setVisibility(underneath, false);
			const shownAgain = await surface.page().screenshot({ clip, animations: "disabled" });
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

for (const { platform, themeStyle } of [
	{ platform: "MacIntel", themeStyle: "orchestrate" },
	...THEMES.map((theme) => ({ platform: "Win32", themeStyle: theme.id })),
]) {
test.describe(`${platform} ${themeStyle}`, () => {
	test.use({ userAgent: platform === "MacIntel"
		? macUserAgent
		: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36" });
for (const theme of ["dark", "light"] as const) {
	for (const transparency of [false, true]) {
		test(`@P0 sticky project occludes sessions and the archive opens: ${platform} ${themeStyle} ${theme}, transparency ${transparency}`, async ({ page, browser }) => {
			await preparePage(page, platform, themeStyle, theme, transparency);
			if (transparency) {
				await expect(page.locator("html")).toHaveAttribute("data-window-translucent", "true");
				await expect(page.locator("html")).toHaveAttribute("data-window-material", platform === "Win32" ? "acrylic" : "vibrancy");
			}
			const workingPanel = page.locator(".center-panel-surface");
			if (platform === "Win32") {
				await expectSurfaceTransparency(workingPanel, transparency);
				for (const surface of [page.locator("body"), workingPanel]) {
					for (const token of ["--color-bg-terminal", "--color-bg-terminal-opaque", "--term-bg"]) {
						expect(await colorAlpha(surface, token), `${token} stays solid for Windows terminals`).toBe(255);
					}
				}
				if (transparency) {
					const sidebarFill = await colorAlpha(page.getByTestId("sidebar-projects-scroller"), "--sidebar-accent");
					expect(sidebarFill).toBeGreaterThan(0);
					expect(sidebarFill).toBeLessThan(255);
				}
			}

			// The archive is a modal now, not a sheet over the lanes.
			await page.getByRole("button", { name: "Archived sessions, 8", exact: true }).click();
			const archive = page.getByRole("dialog", { name: "Archived sessions" });
			await expect(archive).toBeVisible();
			await expect(archive.getByText("Archived session 0")).toBeVisible();
			// The modal keeps its designed backdrop-blur/96% overlay recipe, but
			// its nested controls must inherit solid theme fills above the glass.
			for (const token of ["--color-bg-primary", "--color-bg-secondary", "--color-bg-terminal"]) {
				expect(await colorAlpha(archive, token), `${token} stays solid in floating dialogs`).toBe(255);
			}
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
			if (platform === "Win32" && transparency) {
				await expect(page.locator("html")).toHaveAttribute("data-window-material", "acrylic");
				// Read actual paint after Vite's CSS imports.
				const alpha = await page.evaluate(() => {
					const tint = getComputedStyle(document.querySelector(".app-shell-root")!, "::before").backgroundColor;
					const context = document.createElement("canvas").getContext("2d")!;
					context.fillStyle = tint;
					context.fillRect(0, 0, 1, 1);
					return context.getImageData(0, 0, 1, 1).data[3];
				});
				expect(alpha).toBeGreaterThan(0);
				expect(alpha).toBeLessThan(255);
			}
			if (platform === "Win32" && themeStyle === "orchestrate" && theme === "dark" && transparency) {
				const sessions = project.locator("..").locator(":scope > .sidebar-expanded-chrome");
				await sessions.evaluate((element) => (element as HTMLElement).style.clipPath = "none");
				try {
					await expectOccludes(project, scroller.locator("[data-session-row]"), false);
				} finally {
					await sessions.evaluate((element) => (element as HTMLElement).style.removeProperty("clip-path"));
				}
			}
			if (platform === "Win32" && transparency) {
				// Native composition must keep its clear page cutout; exercise the
				// browser host's published state above the translucent content.
				await workingPanel.evaluate((panel) => {
					panel.parentElement!.classList.add("center-panel-shell--session");
					document.documentElement.dataset.nativeBrowserComposition = "true";
					document.documentElement.dataset.browserNativeLive = "true";
				});
				await expectSurfaceTransparency(workingPanel, true);
			}
			if (platform === "Win32" && transparency) {
				// Exact macOS styling shares its desktop-dependent contrast. This
				// checks parity, not a guaranteed contrast floor over arbitrary wallpaper.
				const mac = await browser.newPage({ userAgent: macUserAgent, baseURL: new URL(page.url()).origin });
				try {
					await preparePage(mac, "MacIntel", themeStyle, theme, true);
					await expect(mac.locator("html")).toHaveAttribute("data-window-material", "vibrancy");
					const macScroller = mac.getByTestId("sidebar-projects-scroller");
					await macScroller.getByRole("button", { name: /Show .* more/ }).click();
					await macScroller.evaluate((element) => { element.scrollTop = 0; });
					await scroller.evaluate((element) => { element.scrollTop = 0; });
					const windowsPaint = await sidebarPaintStates(page, theme);
					const macPaint = await sidebarPaintStates(mac, theme);
					for (const state of Object.values(windowsPaint)) expect(state.tint).toEqual(state.expectedTint);
					expect(windowsPaint).toEqual(macPaint);
				} finally {
					await mac.close();
				}
			}
		});
	}
}
});
}
