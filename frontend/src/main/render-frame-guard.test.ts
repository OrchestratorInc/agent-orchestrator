import { describe, expect, it } from "vitest";
import { blocksRenderFrameNavigation } from "./render-frame-guard";

const page = "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/r1";

describe("blocksRenderFrameNavigation", () => {
	it("lets a frame load its render and move within it", () => {
		expect(blocksRenderFrameNavigation("about:blank", `${page}#ao-theme=x`)).toBe(false);
		expect(blocksRenderFrameNavigation(page, `${page}#section`)).toBe(false);
	});

	it("blocks a render frame from leaving its page", () => {
		expect(blocksRenderFrameNavigation(page, "https://phish.example/login")).toBe(true);
		expect(blocksRenderFrameNavigation(page, "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/r2")).toBe(true);
		expect(blocksRenderFrameNavigation(page, "not a url")).toBe(true);
	});

	it("holds an artifact frame on its page too", () => {
		const artifact = "http://127.0.0.1:3001/api/v1/sessions/p-1/artifact-files/report/index.html";
		expect(blocksRenderFrameNavigation(artifact, `${artifact}#totals`)).toBe(false);
		expect(blocksRenderFrameNavigation(artifact, "http://127.0.0.1:3001/api/v1/sessions/p-1/artifact-files/report/other.html")).toBe(true);
		expect(blocksRenderFrameNavigation(artifact, "https://phish.example/login")).toBe(true);
	});

	it("holds an artifact framed from its own inline origin on its page", () => {
		const inline = "http://ao-inline-artifact.x.localhost:3001/q3/report.html";
		expect(blocksRenderFrameNavigation(inline, `${inline}#totals`)).toBe(false);
		expect(blocksRenderFrameNavigation(inline, "http://ao-inline-artifact.x.localhost:3001/q3/other.html")).toBe(true);
		expect(blocksRenderFrameNavigation(inline, "https://phish.example/login")).toBe(true);
	});

	it("leaves every other frame alone", () => {
		expect(blocksRenderFrameNavigation("https://docs.example/", "https://elsewhere.example/")).toBe(false);
	});
});
