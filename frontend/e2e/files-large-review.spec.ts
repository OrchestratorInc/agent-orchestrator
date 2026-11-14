import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";
import { openInspector } from "./support/open-inspector";
import { largeReviewFiles as files, largeReviewPatch as patch } from "./support/large-review-fixture";

const sessionId = "demo-working";

test("@P0 thousands of changed files stay bounded and responsive", async ({ page }, testInfo) => {
	test.setTimeout(60_000);
	await installFakeBridge(page);
	const requested: string[][] = [];
	const errors: string[] = [];
	page.on("pageerror", (error) => errors.push(error.message));
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${sessionId}/workspace/manifest*`, (route) => route.fulfill({ json: {
		sessionId, workspaceVersion: "large-workspace", files,
		sections: { uncommitted: files, committed: [], staged: files, unstaged: [], untracked: [] },
		summary: { files: files.length, additions: 150 * files.length, deletions: 150 * files.length }, truncated: false,
	} }));
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${sessionId}/workspace/diffs*`, (route) => {
		const { paths } = route.request().postDataJSON() as { paths: string[] };
		requested.push(paths);
		return route.fulfill({ json: { sessionId, workspaceVersion: "large-workspace", groups: [{
			repository: "", patch: paths.map(patch).join(""), includedPaths: paths, deferred: [], truncated: false,
		}] } });
	});
	await page.goto(`/#/projects/ao-demo/sessions/${sessionId}`);
	const inspector = await openInspector(page);
	// Keep a heartbeat running during mount, patch parsing, and navigation.
	await page.evaluate(() => {
		const gaps: number[] = [];
		let previous = performance.now();
		let active = true;
		function tick(now: number) {
			gaps.push(now - previous);
			previous = now;
			if (active) requestAnimationFrame(tick);
		}
		requestAnimationFrame(tick);
		Object.assign(window, { finishReviewHeartbeat: () => { active = false; return gaps; } });
	});
	const started = Date.now();
	await inspector.getByRole("tab", { name: /Files?$/ }).click();
	await expect(inspector.getByText("Files 1–96 of 5123")).toBeVisible();
	await expect.poll(() => requested.flat().length).toBe(96);
	const openedMs = Date.now() - started;
	// The pane must not resume loading the rest of the repository after settling.
	await page.waitForTimeout(500);
	expect(requested.flat()).toEqual(files.slice(0, 96).map((file) => file.path));
	await inspector.getByRole("button", { name: "Next files", exact: true }).click();
	await expect(inspector.getByText("Files 97–192 of 5123")).toBeVisible();
	await expect.poll(() => requested.flat().length).toBe(192);
	await inspector.getByRole("textbox", { name: "Filter files", exact: true }).fill("file-05122.ts");
	await expect(inspector.getByRole("button", { name: `Collapse ${files[5122].path}`, exact: true })).toBeVisible();
	await expect.poll(() => requested.flat().length).toBe(193);
	const gaps = await page.evaluate(() => (window as unknown as { finishReviewHeartbeat: () => number[] }).finishReviewHeartbeat());
	expect(gaps.length).toBeGreaterThan(5);
	// A generous event-loop budget catches multi-second renderer stalls while
	// allowing CI scheduling noise; file/request bounds above are deterministic.
	expect(Math.max(...gaps)).toBeLessThan(2000);
	expect(errors).toEqual([]);
	await testInfo.attach("large-review-responsiveness", { body: JSON.stringify({ files: files.length, openedMs, frames: gaps.length, maxFrameGapMs: Math.max(...gaps), requestedFiles: requested.flat().length }), contentType: "application/json" });
});
