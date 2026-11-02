import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

for (const { harness, providerLabel } of [
	{ harness: "claude-code", providerLabel: "Claude Code" },
	{ harness: "codex", providerLabel: "Codex" },
]) {
	test.describe(`${providerLabel} usage-limit recovery @P0`, () => {
		test.beforeEach(async ({ page }) => { await installFakeBridge(page); });

		test("offers opt-in, cancellation, then Resume when reset time arrives", async ({ page }) => {
			await page.clock.install();
			await page.goto(`/e2e/usage-limit/harness.html?harness=${harness}&delay=60000`);
			await expect(page.getByRole("note")).toHaveText("UI-only simulation. No daemon runs here; auto-resume does not execute.");
			await expect(page.getByRole("tab", { name: `Usage-limit recovery · ${providerLabel}`, exact: true })).toBeVisible();
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

		test("keeps overdue auto-resume cancellable on a narrow screen before manual recovery", async ({ page }) => {
			await page.setViewportSize({ width: 390, height: 844 });
			await page.clock.install();
			await page.goto(`/e2e/usage-limit/harness.html?harness=${harness}&delay=60000`);
			await expect(page.getByRole("note")).toHaveText("UI-only simulation. No daemon runs here; auto-resume does not execute.");
			await expect(page.getByRole("tab", { name: `Usage-limit recovery · ${providerLabel}`, exact: true })).toBeVisible();
			const recoveryNotice = page.getByTestId("usage-limit-notice").filter({ has: page.locator("time") });
			await page.getByRole("button", { name: "Auto-resume at reset" }).click();

			const cancel = page.getByRole("button", { name: "Cancel auto-resume" });
			await expect(cancel).toBeEnabled();
			await expect(cancel).toBeInViewport({ ratio: 1 });
			await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
			expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

			await page.clock.fastForward(61_000);
			await expect(cancel).toBeEnabled();
			await expect(cancel).toBeInViewport({ ratio: 1 });
			await expect(page.getByRole("button", { name: "Auto-resume at reset" })).toHaveCount(0);
			await expect(page.getByText(/Auto-resume is scheduled/)).toHaveCount(0);
			await expect(page.getByText("Auto-resume is waiting to retry.", { exact: true })).toBeVisible();
			await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
			expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

			await cancel.click();
			await expect(cancel).toHaveCount(0);
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

		test("keeps Cancel and Resume available without loading the paginated prompt", async ({ page }) => {
			await page.setViewportSize({ width: 390, height: 844 });
			await page.goto(`/e2e/usage-limit/harness.html?harness=${harness}&paginated=1&armed=1&delay=-1000`);
			await expect(page.getByRole("note")).toHaveText("UI-only simulation. No daemon runs here; auto-resume does not execute.");
			const prompt = page.getByText("Continue improving the session experience.", { exact: true });
			await expect(prompt).toHaveCount(0);
			await expect(page.getByRole("button", { name: "Load earlier messages" })).toBeVisible();
			const cancel = page.getByRole("button", { name: "Cancel auto-resume" });
			await expect(cancel).toBeEnabled();
			await cancel.click();
			const resume = page.getByRole("button", { name: "Resume", exact: true });
			await expect(resume).toBeEnabled();
			await expect(prompt).toHaveCount(0);
			await resume.click();
			await expect(resume).toHaveCount(0);
		});

		test("keeps controls usable on a narrow screen and never guesses a reset", async ({ page }) => {
			await page.setViewportSize({ width: 390, height: 844 });
			await page.goto(`/e2e/usage-limit/harness.html?harness=${harness}&unknown=1`);
			await expect(page.getByRole("note")).toHaveText("UI-only simulation. No daemon runs here; auto-resume does not execute.");
			await expect(page.getByRole("tab", { name: `Usage-limit recovery · ${providerLabel}`, exact: true })).toBeVisible();
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
}
