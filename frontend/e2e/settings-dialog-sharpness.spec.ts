import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

// REGRESSION: "Settings is blurry" (#5873, #6016, #6373).
//
// Two earlier fixes changed z-order and only had z-order tests. For #6373 the
// real Electron app showed the soft text came from the window zoom on a 1x
// display, not from the dialog: its pixels were identical with the backdrop
// blur removed and with the dialog snapped to whole device pixels.
//
// This spec pins the paths that would resample the dialog's text for real:
// once settled, the dialog and its ancestors must have no transform, scale,
// filter, or backdrop-filter, no will-change hint pinning a stale raster
// scale, and no running open animation. The backdrop stays below the dialog.

const port = Number(process.env.AO_E2E_PORT ?? 5173);

test("settings: settled dialog has no resampling styles and the backdrop stays below it", async ({ page }) => {
	await installFakeBridge(page, { daemonPort: port });
	await page.goto("/#/settings");
	const dialog = page.getByRole("dialog").filter({ has: page.getByTestId("settings-page") });
	await expect(dialog).toBeVisible();

	const settled = await dialog.evaluate(async (element) => {
		await Promise.all(element.getAnimations({ subtree: true }).map((animation) => animation.finished.catch(() => undefined)));
		const chain = [];
		for (let node: Element | null = element; node; node = node.parentElement) {
			const style = getComputedStyle(node);
			chain.push({
				node: node === element ? "dialog" : node.tagName.toLowerCase(),
				transform: style.transform,
				scale: style.scale,
				filter: style.filter,
				backdropFilter: style.backdropFilter,
				willChange: style.willChange,
			});
		}
		const overlay = document.querySelector<HTMLElement>('[data-testid="settings-dialog-overlay"]');
		return {
			chain,
			runningAnimations: element.getAnimations({ subtree: false }).filter((animation) => animation.playState === "running").length,
			overlayZ: Number(overlay && getComputedStyle(overlay).zIndex),
			dialogZ: Number(getComputedStyle(element).zIndex),
		};
	});

	expect(settled.runningAnimations).toBe(0);
	for (const entry of settled.chain) {
		expect(entry, JSON.stringify(entry)).toMatchObject({
			transform: "none",
			scale: "none",
			filter: "none",
			backdropFilter: "none",
			willChange: "auto",
		});
	}
	expect(settled.overlayZ).toBeLessThan(settled.dialogZ);
});

