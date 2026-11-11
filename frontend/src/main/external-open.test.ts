import { describe, expect, it, vi } from "vitest";
import { isAllowedAppExternalURL, openAllowedAppExternalURL } from "./external-open";

describe("isAllowedAppExternalURL", () => {
	it("allows web and mail handoff URLs from the app renderer", () => {
		expect(isAllowedAppExternalURL("https://github.com/Untrivial-ai/agent-orchestrator/issues/new")).toBe(true);
		expect(isAllowedAppExternalURL("http://localhost:5173/help")).toBe(true);
		expect(isAllowedAppExternalURL("mailto:prateek@untrivial.ai?subject=AO%20feedback")).toBe(true);
	});

	it("blocks local, privileged, and script schemes", () => {
		expect(isAllowedAppExternalURL("file:///Users/alice/private.txt")).toBe(false);
		expect(isAllowedAppExternalURL("app://renderer/index.html")).toBe(false);
		expect(isAllowedAppExternalURL("javascript:alert(1)")).toBe(false);
	});

	it("allows only the exact macOS privacy settings pane", () => {
		expect(isAllowedAppExternalURL("x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")).toBe(true);
		expect(isAllowedAppExternalURL("x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture")).toBe(true);
		expect(isAllowedAppExternalURL("x-apple.systempreferences:com.apple.preference.security")).toBe(false);
		expect(isAllowedAppExternalURL("x-apple.systempreferences:com.apple.Sharing-Settings.extension")).toBe(false);
	});

	it("opens allowed URLs through the native shell opener", async () => {
		const openExternal = vi.fn().mockResolvedValue(undefined);

		await openAllowedAppExternalURL("mailto:prateek@untrivial.ai?subject=AO%20feedback", { openExternal });

		expect(openExternal).toHaveBeenCalledWith("mailto:prateek@untrivial.ai?subject=AO%20feedback");
	});

	it("rejects unsupported URLs before reaching the shell opener", async () => {
		const openExternal = vi.fn().mockResolvedValue(undefined);

		await expect(openAllowedAppExternalURL("file:///Users/alice/private.txt", { openExternal })).rejects.toThrow(
			"Unsupported external URL",
		);
		expect(openExternal).not.toHaveBeenCalled();
	});
});
