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

	it("leaves every other frame alone", () => {
		expect(blocksRenderFrameNavigation("https://docs.example/", "https://elsewhere.example/")).toBe(false);
	});
});
