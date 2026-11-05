import { expect, test, type Locator } from "@playwright/test";
import { readFileSync } from "node:fs";
import { installFakeAgent } from "./support/fake-bridge";

const sessionId = "chat-render-wrap";
const title = "Branch comparison";
const renderPath = `/api/v1/sessions/${sessionId}/renders/wide-table`;
const bootstrap = readFileSync("../backend/internal/renderpage/render_bootstrap.js", "utf8");

function renderPage(loadId: number) {
	return `<!doctype html><html data-load-id="${loadId}"><head>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style id="ao-theme">:root{--background:#fff;--foreground:#111}</style>
<script>${bootstrap}</script>
<style>
body{margin:0;font:14px/1.5 sans-serif;background:var(--background);color:var(--foreground)}
.report{width:1600px;min-width:1600px}
.table-scroll{max-width:1100px;overflow-x:auto}
table{width:1800px;min-width:1800px;border-collapse:collapse;white-space:nowrap}
th,td{width:600px;padding:8px;text-align:left;border-bottom:1px solid #777}
code{white-space:pre}
</style></head><body><main class="report"><div class="table-scroll"><table>
<thead><tr><th>Behavior</th><th>Prompt workaround</th><th>Branch option</th></tr></thead>
<tbody>
<tr><td>When the switch happens</td><td>After the agent starts, if it follows the instruction</td><td>Before the agent starts, done by AO</td></tr>
<tr><td>Branch shown in the UI</td><td>The original session branch remains visible in the supervisor</td><td><code>ao_spawn_--branch_feature/this-is-a-long-unbroken-branch-name-that-must-wrap-inside-the-table-cell</code></td></tr>
<tr><td>Link PR to the session</td><td>Only through the worker's reported branches</td><td>Direct match on the session's branch</td></tr>
</tbody></table></div>
<div id="diagram" style="white-space:pre">alpha\n    beta</div>
<span id="code-lines" style="display:block;white-space:pre-wrap">alpha\n    beta</span>
<div id="line-breaks" style="white-space:pre-line">alpha\n    beta</div>
<pre id="pre-lines">alpha\n    beta</pre>
<textarea aria-label="Code editor" style="white-space:pre">alpha\n    beta</textarea>
</main></body></html>`;
}

async function overflow(frame: Locator) {
	return frame.contentFrame().locator("html").evaluate((root) => {
		const wrapper = root.querySelector(".table-scroll");
		if (!wrapper) throw new Error("rendered table wrapper not found");
		return {
			root: root.scrollWidth - root.clientWidth,
			wrapper: wrapper.scrollWidth - wrapper.clientWidth,
		};
	});
}

for (const width of [390, 1440]) {
	test(`HTML table wrapping fits a ${width}px viewport and shares state with the expanded page @T0`, async ({ page }) => {
		test.setTimeout(60_000);
		await page.setViewportSize({ width, height: 1000 });
		await installFakeAgent(page, { workers: [{ id: sessionId, title, mode: "chat" }] });
		const now = "2026-10-17T08:00:00Z";
		let renderLoads = 0;
		await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
			const path = new URL(route.request().url()).pathname;
			if (path === renderPath) {
				await route.fulfill({ contentType: "text/html", body: renderPage(++renderLoads) });
				return;
			}
			if (path.endsWith("/conversation")) {
				await route.fulfill({
					json: {
						conversationId: "conversation-render-wrap",
						sessionId,
						harness: "codex",
						mode: "chat",
						controller: "ready",
						latestSequence: 1,
						oldestSequence: 1,
						hasMoreBefore: false,
						turns: [{ id: "turn-1", state: "completed", requestedAt: now, startedAt: now, completedAt: now }],
						messages: [],
						activities: [{
							kind: "activity", id: "render-1", turnId: "turn-1", sequence: 1, revision: 0,
							activityKind: "system", status: "completed", summary: title, createdAt: now,
							detail: { event: "render", render: { id: "wide-table", title, height: 80, path: renderPath } },
						}],
						settings: {},
					},
				});
				return;
			}
			if (path.endsWith("/conversation/models")) {
				await route.fulfill({ json: { models: [], selected: {} } });
				return;
			}
			if (path.endsWith("/conversation/skills")) {
				await route.fulfill({ json: { skills: [] } });
				return;
			}
			await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
		});
		await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
		await expect(page.getByRole("log", { name: "Conversation" })).toBeVisible({ timeout: 30_000 });
		const closeInspector = page.getByRole("button", { name: "Close inspector panel" });
		if (await closeInspector.isVisible()) await closeInspector.click();
		const collapseSidebar = page.getByRole("button", { name: "Collapse sidebar", exact: true });
		if (await collapseSidebar.isVisible()) await collapseSidebar.click();

		const inline = page.getByTitle(title, { exact: true });
		await expect(inline).toBeVisible();
		const inlineRoot = inline.contentFrame().locator("html");
		await expect(inlineRoot).toHaveAttribute("data-load-id", "1");
		await expect.poll(async () => (await overflow(inline)).root).toBeGreaterThan(0);
		await expect.poll(async () => (await overflow(inline)).wrapper).toBeGreaterThan(0);
		const authoredHeight = await inlineRoot.locator(".report").evaluate((report) => Math.ceil(report.getBoundingClientRect().height));
		await expect.poll(() => inline.evaluate((frame) => frame.getBoundingClientRect().height)).toBe(authoredHeight);
		const originalHeight = await inline.evaluate((frame) => frame.getBoundingClientRect().height);
		const wrap = page.getByRole("button", { name: "Wrap content", exact: true });
		await expect(wrap).toHaveAttribute("aria-pressed", "false");
		await wrap.click();
		await expect(wrap).toHaveAttribute("aria-pressed", "true");
		await expect.poll(() => overflow(inline)).toEqual({ root: 0, wrapper: 0 });
		await expect.poll(() => inline.evaluate((frame) => frame.getBoundingClientRect().height)).toBeGreaterThan(originalHeight);
		await expect(inlineRoot).toHaveAttribute("data-load-id", "1");
		expect(renderLoads).toBe(1);
		for (const id of ["diagram", "code-lines", "line-breaks", "pre-lines"]) {
			const layout = await inlineRoot.locator(`#${id}`).evaluate((element) => {
				const text = element.firstChild;
				if (!(text instanceof Text)) throw new Error("whitespace sample text not found");
				const range = document.createRange();
				range.setStart(text, 0);
				range.setEnd(text, 1);
				const first = range.getBoundingClientRect();
				const secondStart = text.data.indexOf("beta");
				range.setStart(text, secondStart);
				range.setEnd(text, secondStart + 1);
				const second = range.getBoundingClientRect();
				return { lineOffset: second.top - first.top, indentation: second.left - first.left };
			});
			expect(layout.lineOffset, `${id} preserves its newline`).toBeGreaterThan(0);
			if (id === "line-breaks") expect(layout.indentation).toBe(0);
			else expect(layout.indentation, `${id} preserves its indentation`).toBeGreaterThan(0);
		}
		const editor = inline.contentFrame().getByRole("textbox", { name: "Code editor" });
		await expect(editor).toHaveCSS("white-space-collapse", "preserve");
		await expect(editor).toHaveCSS("text-wrap-mode", "wrap");
		await inline.evaluate((frame: HTMLIFrameElement) => frame.contentWindow?.postMessage({
			jsonrpc: "2.0", method: "ui/notifications/host-context-changed",
			params: { theme: "light", styles: { variables: { "--background": "#123456" } } },
		}, "*"));
		await expect.poll(() => inline.contentFrame().locator("#ao-theme").textContent()).toContain("--background:#123456");
		await expect.poll(() => overflow(inline)).toEqual({ root: 0, wrapper: 0 });
		await inlineRoot.evaluate(() => window.dispatchEvent(new MessageEvent("message", {
			source: window,
			data: { jsonrpc: "2.0", method: "ui/notifications/host-context-changed", params: { wrapContent: false } },
		})));
		await expect.poll(() => overflow(inline)).toEqual({ root: 0, wrapper: 0 });

		await page.getByRole("button", { name: "Expand page" }).click();
		const dialog = page.getByRole("dialog");
		await expect(dialog).toBeVisible();
		const expanded = dialog.getByTitle(title, { exact: true });
		const expandedWrap = dialog.getByRole("button", { name: "Wrap content", exact: true });
		await expect(expandedWrap).toHaveAttribute("aria-pressed", "true");
		await expect.poll(() => overflow(expanded)).toEqual({ root: 0, wrapper: 0 });
		await expandedWrap.click();
		await expect(expandedWrap).toHaveAttribute("aria-pressed", "false");
		await expect.poll(async () => (await overflow(expanded)).wrapper).toBeGreaterThan(0);
		await expect(expanded.contentFrame().locator("html")).toHaveAttribute("data-load-id", "2");
		expect(renderLoads).toBe(2);

		await page.keyboard.press("Escape");
		await expect(dialog).toBeHidden();
		await expect(wrap).toHaveAttribute("aria-pressed", "false");
		await expect.poll(async () => (await overflow(inline)).root).toBeGreaterThan(0);
		await expect.poll(async () => (await overflow(inline)).wrapper).toBeGreaterThan(0);
		await expect(inlineRoot).toHaveAttribute("data-load-id", "1");
		expect(renderLoads).toBe(2);

		const image = `data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="600"><rect width="1200" height="600" fill="blue"/></svg>')}`;
		await inlineRoot.locator(".report").evaluate((report, src) => {
			report.insertAdjacentHTML("beforeend", `<img src="${src}" width="1200" height="600" alt="Chart with HTML dimensions">
<img src="${src}" style="width:1200px;height:600px" alt="Chart with CSS dimensions">
<video poster="${src}" width="1200" height="600" style="height:600px"></video>`);
		}, image);
		const media = inlineRoot.locator("img,video");
		const mediaSizes = () => media.evaluateAll((elements) => elements.map((element) => {
			const { width, height } = element.getBoundingClientRect();
			return { width, height };
		}));
		const authoredSizes = Array.from({ length: 3 }, () => ({ width: 1200, height: 600 }));
		await expect.poll(mediaSizes).toEqual(authoredSizes);
		await wrap.click();
		await expect.poll(async () => (await mediaSizes()).every(({ width, height }) =>
			width > 0 && width < 1200 && Math.abs(width / height - 2) < 0.01,
		)).toBe(true);
		await wrap.click();
		await expect.poll(mediaSizes).toEqual(authoredSizes);
	});
}
