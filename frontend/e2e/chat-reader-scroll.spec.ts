import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";
import type {} from "./scroll/harness";

test.use({ viewport: { width: 1100, height: 800 } });

for (const remote of [false, true]) {
	test.describe(`real Timeline reader ownership (remoteHost=${remote}) @P0`, () => {
		test.beforeEach(async ({ page }) => {
			page.on("pageerror", (error) => console.error(error.stack));
			await installFakeBridge(page);
			await page.emulateMedia({ reducedMotion: "reduce" });
			await page.goto("/e2e/scroll/harness.html");
			await page.waitForFunction(() => Boolean(window.scrollHarness));
			await page.evaluate((remote) => window.scrollHarness.open(remote), remote);
			await page.evaluate(() => document.fonts.ready);
			await expect(page.locator("[data-chat-scroll-marker]")).toHaveCount(60);
			await expect.poll(() => page.getByRole("log").evaluate((node) => node.scrollHeight - node.clientHeight - node.scrollTop)).toBeLessThan(1);
			await page.waitForTimeout(1100);
		});

		test("minimap marker stays selected while synthetic snapshots stream", async ({ page }) => {
			await page.locator("[data-chat-scroll-marker]").nth(30).click();
			await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
			await page.waitForTimeout(300);
			const selected = await page.getByRole("log").evaluate((node) => node.scrollTop);
			await page.evaluate(() => window.scrollHarness.stream());
			await expect.poll(() => page.getByRole("log").evaluate((node) => node.scrollTop)).toBe(selected);
			await page.getByRole("button", { name: "Jump to latest" }).click();
			await expect.poll(() => page.getByRole("log").evaluate((node) => node.scrollHeight - node.clientHeight - node.scrollTop)).toBeLessThan(1);
			const previousHeight = await page.getByRole("log").evaluate((node) => node.scrollHeight);
			await page.evaluate(() => window.scrollHarness.stream());
			await expect.poll(() => page.getByRole("log").evaluate((node) => node.scrollHeight)).toBeGreaterThan(previousHeight);
			await expect.poll(() => page.getByRole("log").evaluate((node) => node.scrollHeight - node.clientHeight - node.scrollTop)).toBeLessThan(1);
		});

		test("custom PageUp survives a snapshot before its queued scroll event", async ({ page }) => {
			const positions = await page.evaluate(() => {
				const log = document.querySelector<HTMLElement>('[role="log"]')!;
				document.querySelector('[role="scrollbar"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true, cancelable: true }));
				const selected = log.scrollTop;
				window.scrollHarness.grow();
				return { selected, afterCommit: log.scrollTop };
			});
			expect(positions.afterCommit).toBe(positions.selected);
			await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
			await page.waitForTimeout(300);
			const anchor = await page.getByRole("log").evaluate((log) => {
				const row = Array.from(log.querySelectorAll<HTMLElement>("[data-index]")).find((row) => row.getBoundingClientRect().bottom > log.getBoundingClientRect().top)!;
				return { index: row.dataset.index!, top: row.getBoundingClientRect().top };
			});
			await page.evaluate(() => window.scrollHarness.stream());
			expect(await page.locator(`[data-index="${anchor.index}"]`).evaluate((node) => node.getBoundingClientRect().top)).toBe(anchor.top);
		});

		test("wheel and thumb navigation hold their position through measured growth", async ({ page }) => {
			const log = page.getByRole("log");
			await log.hover();
			await page.mouse.wheel(0, -500);
			await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
			await page.waitForTimeout(300);
			const selected = await log.evaluate((node) => node.scrollTop);
			await page.evaluate(() => window.scrollHarness.stream());
			expect(await log.evaluate((node) => node.scrollTop)).toBe(selected);
			await page.getByRole("button", { name: "Jump to latest" }).click();
			const track = await page.getByRole("scrollbar").boundingBox();
			if (!track) throw new Error("Missing minimap track");
			await page.mouse.move(track.x + 1, track.y + track.height - 10);
			await page.mouse.down();
			await page.mouse.move(track.x + 1, track.y + track.height / 2, { steps: 5 });
			await page.waitForTimeout(300);
			const dragged = await log.evaluate((node) => node.scrollTop);
			await page.evaluate(() => window.scrollHarness.stream());
			expect(await log.evaluate((node) => node.scrollTop)).toBe(dragged);
			await page.mouse.up();
		});
	});
}
