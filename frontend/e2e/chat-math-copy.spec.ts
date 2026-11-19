import { expect, test, type Page } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

const reply = "Before $x^2$.\n\n$$\nE = mc^2\n$$\n\n- one \\(y\\)\n- two\n\nAfter.";

async function copyWholeReply(page: Page, streaming: boolean) {
	await page.evaluate(([text, live]) => window.mathCopyHarness.render(text, live), [reply, streaming] as const);
	await expect(page.locator("#reply .katex")).toHaveCount(3);
	return page.evaluate(() => {
		window.getSelection()!.selectAllChildren(document.getElementById("reply")!);
		const event = new ClipboardEvent("copy", { clipboardData: new DataTransfer(), bubbles: true, cancelable: true });
		document.getElementById("reply")!.dispatchEvent(event);
		return event.clipboardData!.getData("text/plain");
	});
}

test("copying a reply with formulas keeps their TeX and the reply's block breaks @T0", async ({ page }) => {
	test.setTimeout(60_000);
	await installFakeBridge(page);
	await page.goto("/e2e/math-copy/harness.html");
	await page.waitForFunction(() => Boolean(window.mathCopyHarness));
	const expected = "Before $x^2$.\n\n$$\nE = mc^2\n$$\n\none $y$\ntwo\n\nAfter.";
	expect.soft(await copyWholeReply(page, false), "settled reply").toBe(expected);
	// Streamed blocks render as sibling roots with no whitespace between them.
	expect.soft(await copyWholeReply(page, true), "streamed reply").toBe(expected);
});
