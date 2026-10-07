import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

// REGRESSION: "Settings is blurry" (#5873, #6016, #6373).
//
// Two earlier fixes changed z-order and only had z-order tests. Measured in the
// real Electron app for #6373, the soft text came from a persisted 189% window
// zoom on a 1x display: the dialog's pixels were identical with the backdrop
// blur removed and with the dialog snapped to whole device pixels.
//
// This spec checks both sides. (1) The settled dialog must not be rendered
// through anything that resamples its text: no transform, scale, filter or
// backdrop-filter on the dialog or its ancestors, no will-change hint pinning a
// stale raster scale, and no running open animation. (2) A non-100% zoom must
// be visible in Settings with a way back to 100%.

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

test("settings: a non-100% window zoom is shown with a reset", async ({ page }) => {
	await installFakeBridge(page, { daemonPort: port });
	await page.addInitScript(() => {
		const bridge = (window as unknown as { ao: { window: Record<string, unknown> } }).ao.window;
		let factor = 1.2 ** 3.5;
		let listener: ((value: number) => void) | null = null;
		bridge.getZoomFactor = async () => factor;
		bridge.onZoomFactor = (next: (value: number) => void) => {
			listener = next;
			return () => undefined;
		};
		bridge.resetZoom = async () => {
			factor = 1;
			listener?.(factor);
		};
	});
	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();

	await expect(page.getByText("189%")).toBeVisible();
	await page.getByRole("button", { name: "Reset to 100%" }).click();
	await expect(page.getByText("100%", { exact: true })).toBeVisible();
	await expect(page.getByRole("button", { name: "Reset to 100%" })).toHaveCount(0);
});
