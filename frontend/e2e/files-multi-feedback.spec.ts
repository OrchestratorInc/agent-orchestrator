import { expect, test, type Page } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";
import { openInspector } from "./support/open-inspector";

const sessionId = "demo-working";
const goFile = "backend/internal/session/lifecycle.go";
const tsFile = "frontend/src/renderer/lib/format.ts";

function addedLinesPatch(path: string, lines: string[]) {
	return [
		`diff --git a/${path} b/${path}`,
		"index 1111111..2222222 100644",
		`--- a/${path}`,
		`+++ b/${path}`,
		`@@ -1,2 +1,${lines.length + 2} @@`,
		" // header",
		...lines.map((line) => `+${line}`),
		" // footer",
		"",
	].join("\n");
}

const patch = addedLinesPatch(goFile, ["func first() {}", "func second() {}"]) + addedLinesPatch(tsFile, ["export const third = 3;"]);

// The preview session has no workspace behind it, so serve two changed files
// and capture what the review sends to the agent.
async function stubWorkspace(page: Page, sent: string[]) {
	await installFakeBridge(page);
	const files = [goFile, tsFile].map((path, index) => ({ path, status: "modified", additions: 2 - index, deletions: 0, size: 512, binary: false, editable: true, fileFingerprint: `fp-${index}` }));
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${sessionId}/workspace/manifest*`, (route) =>
		route.fulfill({
			json: {
				sessionId,
				workspaceVersion: "workspace-1",
				files,
				sections: { committed: [], staged: [], unstaged: files, untracked: [] },
				commits: [],
				summary: { additions: 3, deletions: 0, files: 2 },
				truncated: false,
			},
		}),
	);
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${sessionId}/workspace/diffs*`, (route) =>
		route.fulfill({
			json: {
				sessionId,
				workspaceVersion: "workspace-1",
				groups: [{ repository: "", patch, truncated: false, includedPaths: [goFile, tsFile], deferred: [] }],
			},
		}),
	);
	await page.route(`http://127.0.0.1:8080/api/v1/sessions/${sessionId}/send`, async (route) => {
		sent.push((route.request().postDataJSON() as { message: string }).message);
		await route.fulfill({ json: {} });
	});
}

async function commentOnLine(page: Page, code: string, feedback: string) {
	const inspector = page.locator("#inspector");
	await inspector.getByText(code, { exact: true }).hover();
	await inspector.locator("[data-utility-button]:visible").click();
	await page.keyboard.type(feedback);
}

test("@P0 inline comments on several lines and files go to the agent in one message", async ({ page }) => {
	const sent: string[] = [];
	await stubWorkspace(page, sent);
	await page.goto(`/#/projects/ao-demo/sessions/${sessionId}`);
	const inspector = await openInspector(page);
	await inspector.getByRole("tab", { name: /Files?$/ }).click();
	await expect(page.getByRole("button", { name: `Collapse ${goFile}` })).toBeVisible();

	await commentOnLine(page, "func first() {}", "Rename first.");
	await commentOnLine(page, "func second() {}", "Drop second.");
	await commentOnLine(page, "export const third = 3;", "Inline third.");

	// Every box stays open with its own text, and the newest one holds the caret.
	const boxes = inspector.getByRole("textbox", { name: /^Feedback for / });
	await expect(boxes).toHaveCount(3);
	await expect(boxes.nth(0)).toHaveValue("Rename first.");
	await expect(boxes.nth(1)).toHaveValue("Drop second.");
	await expect(boxes.nth(2)).toHaveValue("Inline third.");
	await expect(boxes.nth(2)).toBeFocused();
	// Every box says the send covers all three.
	await expect(inspector.getByText("⌘/Ctrl + Enter to send all 3")).toHaveCount(3);

	// One bar under the files says how many comments are ready and sends them together.
	const bar = inspector.getByTestId("file-feedback-bar");
	await expect(bar).toContainText("3 comments ready");
	await bar.getByRole("button", { name: "Send all 3" }).click();

	await expect.poll(() => sent.length).toBe(1);
	expect(sent[0]).toContain("3 inline feedback comments");
	for (const text of ["Rename first.", "Drop second.", "Inline third.", `- Path: ${goFile}`, `- Path: ${tsFile}`]) expect(sent[0]).toContain(text);
	await expect(boxes).toHaveCount(0);
});
