import { expect, test, type Page } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

// REGRESSION: the Shortcuts search bar is `position: sticky`, so rows scroll
// underneath it. The pinned header's background was
// `var(--color-bg-settings-row)`, which the default dark theme defines as
// `transparent` — the field had nothing behind it, and the row currently
// scrolling under it painted straight through the search input, overlapping
// the placeholder ("Toggle sidebar" on top of "Search commands or key
// combinations"). The header has to paint an opaque surface (`--card`, the
// color SettingsDialog draws behind it) so rows disappear cleanly under the
// pinned bar. Light theme is unaffected: it aliases the row token to --card.

const SEARCH_PLACEHOLDER = "Search commands or key combinations";

async function openShortcuts(page: Page): Promise<void> {
	await installFakeBridge(page, { daemonPort: Number(process.env.AO_E2E_PORT ?? 5173) });
	await page.goto("/#/settings");
	await page.getByRole("button", { name: "Shortcuts" }).click();
	await expect(page.getByPlaceholder(SEARCH_PLACEHOLDER)).toBeVisible();
}

/** Alpha of a CSS color (0 = see-through, 1 = paints over what is behind it). */
async function backgroundAlpha(page: Page, color: string): Promise<number> {
	return page.evaluate((value) => {
		const canvas = document.createElement("canvas");
		canvas.width = 1;
		canvas.height = 1;
		const context = canvas.getContext("2d");
		if (!context) throw new Error("2D canvas unavailable");
		// Sentinel: fillStyle assignment silently ignores unparsable colors, and
		// a fresh context defaults to opaque black — without this, a malformed
		// value would pass the opacity assertion vacuously.
		context.fillStyle = "rgba(1, 2, 3, 0)";
		context.fillStyle = value;
		if (context.fillStyle === "rgba(1, 2, 3, 0)") throw new Error(`unparsable color: ${value}`);
		context.fillRect(0, 0, 1, 1);
		return context.getImageData(0, 0, 1, 1).data[3] / 255;
	}, color);
}

type Layout = {
	searchBottom: number;
	searchTop: number;
	firstRowTop: number;
	headerTop: number;
	headerBottom: number;
	scrollerTop: number;
	headerBackground: string;
};

async function layout(page: Page): Promise<Layout> {
	return page.evaluate((placeholder) => {
		const input = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`);
		if (!input) throw new Error("shortcuts search input not found");
		const header = input.closest("label")?.parentElement;
		if (!header) throw new Error("sticky header not found above the search input");
		const panel = header.parentElement;
		if (!panel) throw new Error("sticky panel owning the header not found");
		let scroller: HTMLElement | null = header;
		while (scroller && !/(auto|scroll)/.test(getComputedStyle(scroller).overflowY)) {
			scroller = scroller.parentElement;
		}
		if (!scroller) throw new Error("settings dialog scroll container (overflow-y-auto) not found");
		// Scoped to the panel that owns the header (sibling rows live beside the
		// sticky bar) so foreign rows sharing the row class elsewhere in the
		// document can't hijack the measurement.
		const rows = [...panel.querySelectorAll<HTMLElement>("[class*='min-h-(--size-settings-row)']")];
		if (rows.length === 0) throw new Error("shortcuts rows inside the sticky panel not found");
		const search = input.getBoundingClientRect();
		const firstRow = rows[0].getBoundingClientRect();
		const headerBox = header.getBoundingClientRect();
		return {
			searchTop: search.top,
			searchBottom: search.bottom,
			firstRowTop: firstRow.top,
			headerTop: headerBox.top,
			headerBottom: headerBox.bottom,
			scrollerTop: scroller.getBoundingClientRect().top,
			headerBackground: getComputedStyle(header).backgroundColor,
		};
	}, SEARCH_PLACEHOLDER);
}

/** Scroll until the first row has entered the pinned header's band. */
async function scrollRowUnderSearch(page: Page): Promise<void> {
	await page.evaluate((placeholder) => {
		const input = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`);
		if (!input) throw new Error("shortcuts search input not found");
		const header = input.closest("label")?.parentElement;
		if (!header) throw new Error("sticky header not found above the search input");
		const panel = header.parentElement;
		if (!panel) throw new Error("sticky panel owning the header not found");
		let scroller: HTMLElement | null = header;
		while (scroller && !/(auto|scroll)/.test(getComputedStyle(scroller).overflowY)) {
			scroller = scroller.parentElement;
		}
		if (!scroller) throw new Error("settings dialog scroll container (overflow-y-auto) not found");
		const rows = [...panel.querySelectorAll<HTMLElement>("[class*='min-h-(--size-settings-row)']")];
		if (rows.length === 0) throw new Error("shortcuts rows inside the sticky panel not found");
		const rowTop = rows[0].getBoundingClientRect().top;
		scroller.scrollTop += rowTop - scroller.getBoundingClientRect().top - 8;
	}, SEARCH_PLACEHOLDER);
}

for (const viewport of [
	{ width: 900, height: 860 },
	{ width: 1440, height: 900 },
	{ width: 760, height: 620 },
]) {
	for (const scheme of ["dark", "light"] as const) {
		test(`shortcuts search bar stays clear of the list @P0 ${viewport.width}x${viewport.height} ${scheme}`, async ({ page }) => {
			await page.setViewportSize(viewport);
			await page.emulateMedia({ colorScheme: scheme });
			await openShortcuts(page);
			// The old and new tokens differ only in dark (light aliases the old
			// token to --card), so pin the resolved regime — otherwise a theme
			// resolution change could silently exercise only the light pass.
			await expect(page.locator("html")).toHaveAttribute("data-theme", scheme);

			// At rest the search bar and the first row are separate boxes.
			const resting = await layout(page);
			expect(resting.firstRowTop - resting.searchBottom).toBeGreaterThan(0);

			// Scrolled, the first row sits inside the pinned header's band — the
			// case that used to bleed through the translucent field.
			await scrollRowUnderSearch(page);
			await expect
				.poll(async () => {
					const l = await layout(page);
					return l.firstRowTop < l.headerBottom && l.headerTop <= l.scrollerTop + 1;
				})
				.toBe(true);
			const scrolled = await layout(page);
			expect(scrolled.headerTop).toBeLessThanOrEqual(scrolled.scrollerTop + 1);
			expect(scrolled.firstRowTop).toBeLessThan(scrolled.headerBottom);

			// The header must paint an opaque surface over the row behind it.
			expect(await backgroundAlpha(page, scrolled.headerBackground)).toBe(1);

			// Search and row rendering still behave after the fix.
			const search = page.getByPlaceholder(SEARCH_PLACEHOLDER);
			await search.fill("next tab");
			await expect(page.getByText("Next tab", { exact: true })).toBeVisible();
			await expect(page.getByText("Previous tab", { exact: true })).not.toBeVisible();
			await search.fill("");
			await expect(page.getByText("Previous tab", { exact: true })).toBeVisible();
		});
	}
}
