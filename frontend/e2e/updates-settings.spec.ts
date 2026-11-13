import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

test("downloaded update keeps the full version readable and actions aligned", async ({ page }) => {
	await page.setViewportSize({ width: 1010, height: 700 });
	await page.emulateMedia({ colorScheme: "dark" });
	await installFakeBridge(page, {
		version: "0.12.7-nightly.202608240525",
		updateSettings: { enabled: true, channel: "nightly", nightlyAck: true, feature: null },
		updateStatus: {
			state: "downloaded",
			version: "0.12.8-nightly.202608241447",
			checkedAt: new Date("2026-08-24T17:11:00.000Z").getTime(),
		},
	});

	await page.goto("/#/settings");
	await page.getByRole("button", { name: "Updates" }).click();

	await expect(page.getByTestId("update-status-line")).toContainText("Downloaded. Restart to finish updating.");

	// The heading carries the base version; the full nightly stamp sits on its
	// own monospace line. As one heading it wrapped mid-token and swallowed the
	// row, and the primary action grew across it.
	const version = page.getByTestId("app-version");
	await expect(version).toHaveText("v0.12.7");
	await expect(version).toHaveAttribute("aria-label", "Current version - v0.12.7-nightly.202608240525");
	await expect(page.getByText("0.12.7-nightly.202608240525", { exact: true })).toBeVisible();

	await expect(page.getByRole("button", { name: "Restart & install", exact: true })).toBeVisible();
	await expect(page.getByRole("button", { name: "Check for updates" })).toBeVisible();
	await expect(page.getByRole("switch", { name: "Automatic updates" })).toBeChecked();
	await expect(page.getByRole("button", { name: "Updates channel", exact: true })).toContainText("Nightly");
	await expect(page.locator(".nightly-warning")).toBeVisible();

	const lineCount = await version.evaluate((element) => element.getClientRects().length);
	expect(lineCount).toBe(1);

	const restartBox = await page.getByRole("button", { name: "Restart & install", exact: true }).boundingBox();
	const checkBox = await page.getByRole("button", { name: "Check for updates" }).boundingBox();
	expect(restartBox).not.toBeNull();
	expect(checkBox).not.toBeNull();
	expect(Math.abs((restartBox?.height ?? 0) - (checkBox?.height ?? 0))).toBeLessThan(1);
	// The actions row must not overrun the version block.
	const versionBox = await version.boundingBox();
	expect(restartBox?.x ?? 0).toBeGreaterThan((versionBox?.x ?? 0) + (versionBox?.width ?? 0));
});

test("@P0 live update flow stays synchronized without reopening Settings", async ({ page }) => {
	await installFakeBridge(page, {
		version: "1.0.0",
		updateStatus: { state: "available", version: "2.0.0" },
		updateSettings: { enabled: false, channel: "latest", nightlyAck: false, feature: null },
	});
	await page.goto("/#/settings");
	await page.getByRole("button", { name: "Updates", exact: true }).click();
	await page.getByRole("button", { name: "Update to v2.0.0" }).click();
	await expect(page.getByTestId("sidebar-update-downloading")).toContainText("Downloading update");
	await expect(page.getByTestId("update-status-line")).toContainText(/Starting download…|Downloading…/);

	// A missed push is recovered by one local status read, without feed checks.
	await page.evaluate(() => {
		window.__aoFakeUpdates!.setStatus({ state: "downloading", version: "2.0.0", percent: 42, transferred: 42_000_000, total: 100_000_000 });
	});
	await expect(page.getByTestId("sidebar-update-downloading").getByRole("progressbar")).toHaveAttribute("aria-valuenow", "42");
	await expect(page.getByTestId("update-status-line")).toContainText("42.0 / 100.0 MB");
	await expect(page.getByTestId("app-version")).toHaveText("v1.0.0");
	await expect(page.getByText("Updating to v2.0.0")).toBeVisible();

	await page.evaluate(() => {
		window.__aoFakeUpdates!.setStatus({ state: "preparing", version: "2.0.0", percent: 100, staged: { version: "2.0.0", stagedAt: 10, escalated: false, ready: false } });
	});
	await expect(page.getByTestId("sidebar-update-ready")).toContainText("Ready to install");
	await expect(page.getByTestId("update-status-line")).toContainText("Preparing update…");
	await expect(page.getByRole("button", { name: "Restart & install", exact: true })).toHaveCount(0);

	await page.evaluate(() => {
		window.__aoFakeUpdates!.setStatus({ state: "downloaded", version: "2.0.0" });
	});
	await expect(page.getByTestId("sidebar-update-ready")).toContainText("Ready to install");
	await expect(page.getByTestId("sidebar-update-ready")).toHaveAccessibleName("Restart to install update v2.0.0");
	await expect(page.getByRole("button", { name: "Restart & install", exact: true })).toBeEnabled();
});

for (const width of [1400, 900]) {
	test(`@P0 download progress track spans the text column at ${width}px`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		await installFakeBridge(page, {
			version: "0.14.10-nightly.202611042028",
			updateSettings: { enabled: true, channel: "nightly", nightlyAck: true, feature: null },
			updateStatus: { state: "downloading", version: "0.14.10-nightly.202611052338", percent: 10, transferred: 25_600_000, total: 250_400_000 },
		});
		await page.goto("/#/settings");
		await page.getByRole("button", { name: "Updates", exact: true }).click();

		const progress = page.getByRole("progressbar", { name: "Download progress" });
		const statusLine = page.getByTestId("update-status-line");
		const action = page.getByRole("button", { name: /^Updating…/ });
		const widths: number[] = [];
		for (const percent of [0, 10, 57, 100]) {
			await page.evaluate((value) => {
				window.__aoFakeUpdates!.setStatus({ state: "downloading", version: "0.14.10-nightly.202611052338", percent: value, transferred: value * 1_000_000, total: 100_000_000 });
			}, percent);
			await expect(progress).toHaveAttribute("value", String(percent));
			const bar = (await progress.boundingBox())!;
			const line = (await statusLine.boundingBox())!;
			const button = (await action.boundingBox())!;
			// Starts at the text column and runs up to the actions, not just under
			// the longest text line (the old track stopped under "Built …").
			expect(Math.abs(bar.x - line.x)).toBeLessThan(1);
			expect(button.x - (bar.x + bar.width)).toBeGreaterThanOrEqual(0);
			expect(button.x - (bar.x + bar.width)).toBeLessThan(48);
			widths.push(bar.width);
		}
		// The track never depends on progress; only the fill does.
		expect(Math.max(...widths) - Math.min(...widths)).toBeLessThan(1);
	});
}

for (const [label, version, settingsText, sidebarLabel, shortVersion] of [
	["prefixed nightly", "v0.14.10-nightly.202611052338", "Updating to v0.14.10-nightly.202611052338", "Download update v0.14.10-nightly.202611052338", "v0.14.10"],
	["prefixed stable", "v2.0.0", "Updating to v2.0.0", "Download update v2.0.0", "v2.0.0"],
	["bare stable", "2.0.0", "Updating to v2.0.0", "Download update v2.0.0", "v2.0.0"],
] as const) {
	test(`@P0 update labels show one v for a ${label} version`, async ({ page }) => {
		await installFakeBridge(page, {
			version: "0.14.10-nightly.202611042028",
			updateSettings: { enabled: true, channel: "nightly", nightlyAck: true, feature: null },
			updateStatus: { state: "available", version },
		});
		await page.goto("/#/settings");
		await page.getByRole("button", { name: "Updates", exact: true }).click();
		await expect(page.getByTestId("update-status-line")).toContainText(settingsText);
		// The sidebar says "Update available"; the row button's label carries the
		// full version behind exactly one "v", and the row shows the base one dimmed.
		const available = page.getByTestId("sidebar-update-available");
		await expect(available).toContainText("Update available");
		await expect(available).toContainText(shortVersion);
		await expect(available).toHaveAccessibleName(sidebarLabel);
		await page.evaluate((v) => {
			window.__aoFakeUpdates!.setStatus({ state: "downloading", version: v, percent: 5 });
		}, version);
		await expect(page.getByTestId("update-status-line")).toContainText(settingsText);
		expect(await page.locator("body").innerText()).not.toMatch(/\bvv\d/);
	});
}
