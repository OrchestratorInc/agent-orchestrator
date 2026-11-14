import { describe, expect, it, vi } from "vitest";
import { chromeExtensionsPageCommand, openChromeExtensionsPage } from "./chrome-extensions-page";

describe("chrome extensions page", () => {
	it("launches Chrome itself with the page on each platform", () => {
		expect(chromeExtensionsPageCommand("darwin")).toEqual(["open", ["-a", "Google Chrome", "chrome://extensions"]]);
		expect(chromeExtensionsPageCommand("win32")).toEqual(["cmd", ["/c", "start", "", "chrome", "chrome://extensions"]]);
		expect(chromeExtensionsPageCommand("linux")).toEqual(["google-chrome", ["chrome://extensions"]]);
	});

	it("reports a failed launch", async () => {
		const run = vi.fn(async () => {
			throw new Error("Chrome is not installed");
		});
		await expect(openChromeExtensionsPage("darwin", run)).rejects.toThrow("Chrome is not installed");
	});
});
