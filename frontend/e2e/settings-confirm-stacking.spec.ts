import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

// REGRESSION: settings-dialog stacking vs ConfirmDialog (#5873, then #5944).
//
// #5873 raised the settings dialog content to z-overlay+1 to keep it above its
// own blurred scrim; #5944 kept that value while deduping the class list. Every
// ConfirmDialog lives on the z-overlay layer and portals AFTER the settings
// surface, so a confirm opened from inside Settings painted UNDER it: present
// in the DOM, visually buried, unusable. This guard first opened the
// Developer-mode Cloud toggle's confirm; that toggle is gone now Cloud is on
// for everyone, so it opens Keyboard shortcuts' "Reset all", a confirm that
// opens from Settings for everyone.
//
// toBeVisible() and plain clicks cannot catch this: Radix marks the covered
// background pointer-events:none, so an occluded confirm still has size and
// still receives synthetic clicks, and hit testing skips the covering
// surface. The check that fails on the bug is paint order itself: with
// pointer events forced back on everywhere, the topmost element at the
// confirm's title and buttons must belong to the confirm.

const port = Number(process.env.AO_E2E_PORT ?? 5173);

test("settings: confirm opened from Settings stacks above it and runs its action", async ({ page }) => {
	await installFakeBridge(page, { daemonPort: port });
	// One shortcut already reassigned, so "Reset all" has something to restore.
	await page.addInitScript(() => {
		const bridge = (window as unknown as { ao: { keybindings: { get: () => Promise<unknown>; set: (overrides: unknown) => Promise<unknown> } } }).ao;
		bridge.keybindings.get = async () => ({
			"toggle-sidebar": [{ key: "j", code: "KeyJ", ctrl: true, meta: false, shift: true, alt: false }],
		});
	});

	await page.goto("/#/settings");
	await expect(page.getByTestId("settings-page")).toBeVisible();
	await page.getByRole("button", { name: "Shortcuts", exact: true }).click();
	await page.getByRole("button", { name: "Reset all", exact: true }).click();

	const confirm = page.getByRole("dialog", { name: "Reset all" });
	await expect(confirm).toBeVisible();
	await expect(confirm).toContainText("Restore every shortcut to its original assignment?");

	// Stacking check: the confirm must paint above Settings.
	const covered = await page.evaluate(() => {
		const confirmDialog = [...document.querySelectorAll<HTMLElement>('[role="dialog"]')]
			.find((dialog) => dialog.textContent?.includes("Restore every shortcut"));
		if (!confirmDialog) return ["no confirm dialog"];
		const force = document.createElement("style");
		force.textContent = "* { pointer-events: auto !important; }";
		document.head.append(force);
		try {
			const targets = [...confirmDialog.querySelectorAll<HTMLElement>("h2, button")];
			return targets.flatMap((target) => {
				const box = target.getBoundingClientRect();
				const top = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
				return top && confirmDialog.contains(top) ? [] : [`${target.textContent?.trim()} is under ${top?.outerHTML.slice(0, 120) ?? "nothing"}`];
			});
		} finally {
			force.remove();
		}
	});
	expect(covered).toEqual([]);
	await expect(page.getByTestId("settings-page")).toBeVisible();

	// Confirming runs the action: the confirm closes through onConfirm (Cancel
	// would leave the shortcut reassigned) and says the defaults are back.
	await confirm.getByRole("button", { name: "Reset all" }).click();
	await expect(confirm).toBeHidden();
	await expect(page.getByText("Keyboard shortcuts restored to original assignments")).toBeVisible();
	await expect(page.getByRole("button", { name: "Reset all", exact: true })).toBeDisabled();
});
