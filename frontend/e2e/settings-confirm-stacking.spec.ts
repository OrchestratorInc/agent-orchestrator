import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

const port = Number(process.env.AO_E2E_PORT ?? 5173);

test("settings: confirm opened from Settings stacks above it and runs its action", async ({ page }) => {
	await installFakeBridge(page, { daemonPort: port });
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

	await confirm.getByRole("button", { name: "Reset all" }).click();
	await expect(confirm).toBeHidden();
	await expect(page.getByText("Keyboard shortcuts restored to original assignments")).toBeVisible();
	await expect(page.getByRole("button", { name: "Reset all", exact: true })).toBeDisabled();
});
