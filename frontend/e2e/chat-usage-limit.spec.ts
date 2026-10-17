import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

test.describe("usage-limit recovery @P0", () => {
	test.beforeEach(async ({ page }) => { await installFakeBridge(page); });

	test("offers opt-in, cancellation, then Resume when reset time arrives", async ({ page }) => {
		await page.clock.install();
		await page.goto("/e2e/usage-limit/harness.html?delay=60000");
		const recoveryNotice = page.getByTestId("usage-limit-notice").filter({ has: page.locator("time") });
		await expect(recoveryNotice).toBeVisible();
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
		await page.getByRole("button", { name: "Auto-resume at reset" }).click();
		await expect(page.getByText(/Auto-resume is scheduled/)).toBeVisible();
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
		await page.getByRole("button", { name: "Cancel auto-resume" }).click();
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Auto-resume at reset" })).toBeVisible();
		await page.clock.fastForward(61_000);
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toBeEnabled();
		await page.getByRole("button", { name: "Resume", exact: true }).click();
		await expect(recoveryNotice).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Auto-resume at reset" })).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Cancel auto-resume" })).toHaveCount(0);
		await expect(page.getByText("The agent ran into a problem")).toHaveCount(0);
	});

	test("replaces scheduled cancellation with Resume at reset on a narrow screen and dismisses recovery after retry", async ({ page }) => {
		await page.setViewportSize({ width: 390, height: 844 });
		await page.clock.install();
		await page.goto("/e2e/usage-limit/harness.html?delay=60000");
		const recoveryNotice = page.getByTestId("usage-limit-notice").filter({ has: page.locator("time") });
		await page.getByRole("button", { name: "Auto-resume at reset" }).click();

		const cancel = page.getByRole("button", { name: "Cancel auto-resume" });
		await expect(cancel).toBeEnabled();
		await expect(cancel).toBeInViewport({ ratio: 1 });
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
		expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

		await page.clock.fastForward(61_000);
		await expect(cancel).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Auto-resume at reset" })).toHaveCount(0);
		await expect(page.getByText(/Auto-resume is scheduled/)).toHaveCount(0);
		const resume = page.getByRole("button", { name: "Resume", exact: true });
		await expect(resume).toBeEnabled();
		await expect(resume).toBeInViewport({ ratio: 1 });
		await expect(recoveryNotice).toBeVisible();
		expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

		await resume.click();
		await expect(recoveryNotice).toHaveCount(0);
		await expect(cancel).toHaveCount(0);
		await expect(resume).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Auto-resume at reset" })).toHaveCount(0);
	});

	test("keeps controls usable on a narrow screen and never guesses a reset", async ({ page }) => {
		await page.setViewportSize({ width: 390, height: 844 });
		await page.goto("/e2e/usage-limit/harness.html?unknown=1");
		await expect(page.getByText("The provider did not supply a reset time.")).toBeVisible();
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toBeEnabled();
		await expect(page.getByRole("button", { name: "Auto-resume at reset" })).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Cancel auto-resume" })).toHaveCount(0);
		expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
		await page.getByRole("button", { name: "Resume", exact: true }).click();
		await expect(page.getByText("The provider did not supply a reset time.")).toHaveCount(0);
		await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
	});
});
