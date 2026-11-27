import { describe, expect, it } from "vitest";
import { resolveBrowserViewport } from "./browser-device-presets";

describe("resolveBrowserViewport", () => {
	it("lays devices out as mobile pages at their size", () => {
		expect(resolveBrowserViewport("iphone-12-pro")).toEqual({ width: 390, height: 844, mobile: true });
		expect(resolveBrowserViewport(" iPad-Air ")).toEqual({ width: 820, height: 1180, mobile: true });
	});

	it("knows desktop sizes, as desktop pages unless asked", () => {
		expect(resolveBrowserViewport("desktop")).toEqual({ width: 1280, height: 800, mobile: false });
		expect(resolveBrowserViewport("full-hd")).toEqual({ width: 1920, height: 1080, mobile: false });
	});

	it("takes WIDTHxHEIGHT within 240 to 2560", () => {
		expect(resolveBrowserViewport("800x600")).toEqual({ width: 800, height: 600, mobile: false });
		expect(resolveBrowserViewport("360x740", true)).toEqual({ width: 360, height: 740, mobile: true });
		expect(resolveBrowserViewport("200x600")).toBeNull();
		expect(resolveBrowserViewport("800x3000")).toBeNull();
	});

	it("refuses anything else", () => {
		expect(resolveBrowserViewport("big-phone")).toBeNull();
		expect(resolveBrowserViewport("800 x 600")).toBeNull();
		expect(resolveBrowserViewport("")).toBeNull();
		expect(resolveBrowserViewport("constructor")).toBeNull();
		expect(resolveBrowserViewport("__proto__")).toBeNull();
	});
});
