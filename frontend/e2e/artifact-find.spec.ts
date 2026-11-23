import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import type { ArtifactSearchEngine } from "../../backend/internal/renderpage/artifact_find";

const source = readFileSync("../backend/internal/renderpage/artifact_find.js", "utf8").replace("export function", "function");

for (const mode of ["document", "nested", "horizontal", "shadow", "zoom", "multiline"] as const) {
	test(`artifact Find reveals the occurrence in ${mode} scrollports @T0`, async ({ page }) => {
		await page.setViewportSize({ width: 1000, height: 600 });
		const text = `${"filler line\n".repeat(160)}needle${mode === "multiline" ? "\nsecond" : ""}`;
		const paragraph = `<p style="white-space:pre;margin:0;font:16px/24px monospace">${text}</p>`;
		if (mode === "document") await page.setContent(paragraph);
		else if (mode === "horizontal") await page.setContent(`<div data-scrollport style="width:300px;overflow:auto;border:7px solid"><pre>${"x".repeat(400)}needle</pre></div>`);
		else await page.setContent(`<div data-scrollport style="height:280px;width:600px;overflow:auto;border:7px solid;${mode === "zoom" ? "transform:scale(.75);transform-origin:0 0" : ""}"><div style="height:150px"></div><div id="inner" data-scrollport style="height:200px;overflow:auto">${mode === "shadow" ? "<x-content></x-content>" : paragraph}</div><div style="height:600px"></div></div>`);
		if (mode === "shadow") await page.locator("x-content").evaluate((host, html) => { host.attachShadow({ mode: "open" }).innerHTML = html; }, paragraph);
		const result = await page.evaluate(async ({ source, query }) => {
			const engine = (new Function(`${source};return createArtifactSearchEngine({});`) as () => ArtifactSearchEngine)();
			const matches = await engine.search(document.body, query);
			const selection = window.getSelection();
			const before = selection?.toString();
			// A second reveal must remain stable and preserve selection/focus.
			engine.reveal(matches[0]); engine.reveal(matches[0]);
			const rect = matches[0][0].getBoundingClientRect();
			const ports = [...document.querySelectorAll<HTMLElement>("[data-scrollport]")].map((port) => {
				const box = port.getBoundingClientRect(), sx = box.width / port.offsetWidth, sy = box.height / port.offsetHeight;
				return { left: box.left + port.clientLeft * sx, right: box.left + (port.clientLeft + port.clientWidth) * sx, top: box.top + port.clientTop * sy, bottom: box.top + (port.clientTop + port.clientHeight) * sy };
			});
			return { count: matches.length, rect: { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right }, ports, selected: selection?.toString() === before };
		}, { source, query: mode === "multiline" ? "needle second" : "needle" });
		expect(result.count).toBe(1); expect(result.selected).toBe(true);
		for (const port of [...result.ports, { left: 0, right: 1000, top: 0, bottom: 600 }]) {
			expect(result.rect.top).toBeGreaterThanOrEqual(port.top - 1);
			expect(result.rect.bottom).toBeLessThanOrEqual(port.bottom + 1);
			expect(result.rect.left).toBeGreaterThanOrEqual(port.left - 1);
			expect(result.rect.right).toBeLessThanOrEqual(port.right + 1);
		}
	});
}

test("artifact Find reveals the beginning of an oversized multiline match @T0", async ({ page }) => {
	await page.setContent('<div style="height:120px;overflow:auto"><p style="white-space:pre">before\nneedle\nsecond\nthird\nfourth\nfifth\nsixth\nlast</p></div>');
	const top = await page.evaluate(async (source) => {
		const engine = (new Function(`${source};return createArtifactSearchEngine({});`) as () => ArtifactSearchEngine)();
		const matches = await engine.search(document.body, "needle second third fourth fifth sixth last");
		engine.reveal(matches[0]);
		return { match: matches[0][0].getBoundingClientRect().top, port: document.querySelector("div")!.getBoundingClientRect().top };
	}, source);
	expect(Math.abs(top.match - top.port)).toBeLessThanOrEqual(1);
});
