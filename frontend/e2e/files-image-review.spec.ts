import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";

const png = readFileSync("src/renderer/assets/ao-logo.png");

for (const mixed of [false, true]) {
	for (const width of [650, 400]) {
		test(`@P0 ${mixed ? "mixed text/image" : "image-only"} review scrolls and collapses at ${width}px`, async ({ page }) => {
			await page.setViewportSize({ width, height: 600 });
			await page.route("**/api/v1/sessions/review-fixture/workspace/file/blob?*", async (route) => {
				// Exercise the virtualizer's resize handling after image decoding.
				await new Promise((resolve) => setTimeout(resolve, 150));
				await route.fulfill({ contentType: "image/png", body: png });
			});
			await page.goto(`/e2e/workspace-review/harness.html?mixed=${mixed ? 1 : 0}`);
			const review = page.getByRole("region", { name: "Workspace review" });
			const scroller = review.locator(".ao-pierre-surface");
			const images = review.getByRole("img", { name: /^After version of / });
			await expect(images).toHaveCount(3);
			await expect.poll(() => images.evaluateAll((nodes) => nodes.every((node) => (node as HTMLImageElement).naturalWidth > 0))).toBe(true);
			await expect.poll(() => scroller.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
			const lastImage = review.getByRole("img", { name: "After version of last.png" });
			await expect(lastImage).not.toBeInViewport();
			await scroller.hover();
			await page.mouse.wheel(0, 3000);
			await expect(lastImage).toBeInViewport({ ratio: 0.9 });
			await expect.poll(() => scroller.evaluate((node) => node.scrollTop)).toBeGreaterThan(0);
			// The review scrolls; the surrounding app/document stays fixed.
			expect(await page.evaluate(() => document.scrollingElement!.scrollTop)).toBe(0);

			const collapseFirst = review.getByRole("button", { name: "Collapse first.png", exact: true });
			await collapseFirst.click();
			await expect(review.getByRole("button", { name: "Expand first.png", exact: true })).toHaveAttribute("aria-expanded", "false");
			await expect(review.getByRole("img", { name: "After version of first.png" })).toHaveCount(0);
			await expect(images).toHaveCount(2);
			await review.getByRole("button", { name: "Expand first.png", exact: true }).click();
			await expect(images).toHaveCount(3);
			await expect(collapseFirst).toHaveAttribute("aria-expanded", "true");

			await review.getByRole("button", { name: "Collapse all files", exact: true }).click();
			await expect(images).toHaveCount(0);
			for (const file of ["first.png", "second.png", "last.png", ...(mixed ? ["example.ts"] : [])]) {
				await expect(review.getByRole("button", { name: `Expand ${file}`, exact: true })).toHaveAttribute("aria-expanded", "false");
			}
			await expect.poll(() => scroller.evaluate((node) => node.scrollHeight <= node.clientHeight)).toBe(true);
			await review.getByRole("button", { name: "Expand all files", exact: true }).click();
			await expect(images).toHaveCount(3);
			await expect.poll(() => images.evaluateAll((nodes) => nodes.every((node) => (node as HTMLImageElement).naturalWidth > 0))).toBe(true);
			await expect.poll(() => scroller.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
			if (mixed) await expect(review.getByRole("button", { name: "Collapse example.ts", exact: true })).toHaveAttribute("aria-expanded", "true");
			await scroller.hover();
			await page.mouse.wheel(0, 3000);
			await expect(lastImage).toBeInViewport({ ratio: 0.9 });

			// The changed-files rail must reach footer rows, including a collapsed image.
			await review.getByRole("button", { name: "Collapse last.png", exact: true }).click();
			await scroller.hover();
			await page.mouse.wheel(0, -3000);
			await expect.poll(() => scroller.evaluate((node) => node.scrollTop)).toBe(0);
			await review.getByRole("button", { name: "Reveal last.png", exact: true }).click();
			await expect(review.getByRole("button", { name: "Collapse last.png", exact: true })).toBeInViewport();
			await expect(lastImage).toBeVisible();
			await expect.poll(() => scroller.evaluate((node) => node.scrollTop)).toBeGreaterThan(0);
			expect(await page.evaluate(() => document.scrollingElement!.scrollTop)).toBe(0);

			const firstHeader = review.getByRole("button", { name: "Collapse first.png", exact: true }).locator("..");
			await expect(firstHeader).toHaveJSProperty("clientHeight", 36);
			for (const file of ["first.png", "second.png", "last.png", ...(mixed ? ["example.ts"] : [])]) {
				await review.getByRole("checkbox", { name: `Mark ${file} as viewed`, exact: true }).check();
				await expect(review.getByRole("button", { name: `Expand ${file}`, exact: true })).toHaveAttribute("aria-expanded", "false");
			}
			const total = mixed ? 4 : 3;
			await expect(review.getByRole("checkbox", { checked: true })).toHaveCount(total);
			await expect(images).toHaveCount(0);
			await page.reload();
			await expect(review.getByRole("checkbox", { checked: true })).toHaveCount(total);
			await review.getByRole("checkbox", { name: "Mark first.png as not viewed", exact: true }).uncheck();
			await expect(review.getByRole("checkbox", { checked: true })).toHaveCount(total - 1);
			await expect(review.getByRole("button", { name: "Expand first.png", exact: true })).toHaveAttribute("aria-expanded", "false");
		});
	}
}
