// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { enforceAutomaticBrowserPolicy } from "./automatic-browser-policy";

describe("automatic browser policy", () => {
	it("allows the built-in browser to handle agent commands", async () => {
		const open = vi.fn();
		await expect(enforceAutomaticBrowserPolicy("internal", "open", { url: "https://example.com" }, open)).resolves.toBeUndefined();
		expect(open).not.toHaveBeenCalled();
	});
	it("opens an external URL but reports that agent automation is unavailable", async () => {
		const open = vi.fn().mockResolvedValue(undefined);
		await expect(enforceAutomaticBrowserPolicy("external", "open", { url: "https://example.com" }, open)).rejects.toMatchObject({ code: "BROWSER_AUTOMATION_DISABLED" });
		expect(open).toHaveBeenCalledWith("https://example.com/");
	});
	it("blocks automatic opening and rejects unsafe external URLs", async () => {
		const open = vi.fn();
		await expect(enforceAutomaticBrowserPolicy("blocked", "open", { url: "https://example.com" }, open)).rejects.toMatchObject({ code: "BROWSER_AUTOMATION_DISABLED" });
		await expect(enforceAutomaticBrowserPolicy("external", "open", { url: "file:///secret" }, open)).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		expect(open).not.toHaveBeenCalled();
	});
});
