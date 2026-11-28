import { expect, test } from "@playwright/test";
import { openInspector } from "./support/open-inspector";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. Use stable session URLs
// so fixture title changes do not prevent these tests from reaching Reviews.

test("the Reviews tab renders the reviewer and the PR's comments for a session that owns PRs", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-ready");

	const inspector = await openInspector(page);

	await inspector.getByRole("tab", { name: "Reviews" }).click();

	await expect(inspector.getByText("Reviews start once the worker opens a pull request.")).toHaveCount(0);
	await expect(inspector.getByRole("button", { name: /Select reviewer agent/ })).toBeVisible();
	await expect(inspector.getByRole("switch", { name: "Auto-review" })).toBeVisible();
	await expect(inspector.getByRole("button", { name: "Review now" })).toBeVisible();
	const block = inspector.getByTestId("review-pr-block").first();
	await expect(block).toBeVisible();
	await expect(block.getByRole("tab", { name: /^Open/ })).toHaveAttribute("aria-selected", "true");
	await expect(inspector.getByRole("button", { name: "Open terminal" })).toHaveCount(0);
});

test("the Reviews tab says where reviews start for a worker with no PR yet", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");

	const inspector = await openInspector(page);
	await inspector.getByRole("tab", { name: "Reviews" }).click();
	await expect(inspector.getByText("Reviews start once the worker opens a pull request.")).toBeVisible();
	await expect(inspector.getByRole("button", { name: "Review now" })).toBeDisabled();
});

test("reviewer controls and comment rows fit the inspector at minimum width", async ({ page }) => {
	await page.addInitScript(() => {
		window.localStorage.setItem("ao.inspector.widthPx", "350");
	});
	await page.setViewportSize({ height: 900, width: 1100 });
	await page.goto("/#/projects/ao-demo/sessions/demo-needs-input");

	const inspector = await openInspector(page);
	await inspector.getByRole("tab", { name: "Reviews" }).click();

	const reviewerSelect = inspector.getByRole("button", { name: /Select reviewer agent/ });
	const reviewNow = inspector.getByRole("button", { name: "Review now" });
	await expect(reviewerSelect).toBeVisible();
	await expect(reviewNow).toBeVisible();

	const block = inspector.getByTestId("review-pr-block").first();
	await expect(block).toBeVisible();
	const rows = block.getByTestId("review-comment");
	await expect(rows.first()).toBeVisible();
	await expect(block.getByText("prateek").first()).toBeVisible();

	// A comment not yet sent to the agent starts open with Send to agent in reach;
	// its header folds it to two lines and opens it again.
	const firstRow = rows.first();
	const header = firstRow.locator("[aria-expanded]").first();
	if ((await header.getAttribute("aria-expanded")) === "true") {
		await expect(firstRow.getByRole("button", { name: "Send to agent" })).toBeVisible();
		await header.click();
	}
	await expect(header).toHaveAttribute("aria-expanded", "false");
	await header.click();
	await expect(header).toHaveAttribute("aria-expanded", "true");
	await expect(firstRow.getByRole("button", { name: "Collapse" })).toBeVisible();

	const overflows = await inspector.evaluate((root) => {
		const targets = [
			root.querySelector<HTMLElement>('[role="tabpanel"]'),
			...root.querySelectorAll<HTMLElement>('[data-testid="review-pr-block"]'),
			...root.querySelectorAll<HTMLElement>('[data-testid="review-comment"]'),
		].filter((element): element is HTMLElement => Boolean(element));

		return targets.flatMap((element) => {
			const bounds = element.getBoundingClientRect();
			const culprits = Array.from(element.querySelectorAll<HTMLElement>("*"))
				.filter((child) => {
					const childBounds = child.getBoundingClientRect();
					if (childBounds.width === 0 || childBounds.height === 0) return false;
					return childBounds.left < bounds.left - 1 || childBounds.right > bounds.right + 1;
				})
				.slice(0, 3)
				.map((child) => child.getAttribute("aria-label") || child.className || child.tagName);
			return culprits.length > 0
				? [{ culprits, target: element.dataset.testid || element.className || element.getAttribute("role") }]
				: [];
		});
	});

	expect(overflows).toEqual([]);
});
