// @vitest-environment node
import { describe, expect, it } from "vitest";
import { discoverMacTerminalApps } from "./mac-terminal-apps";

describe("macOS terminal discovery", () => {
	it("accepts registered apps without a terminal-name allowlist", async () => {
		const apps = [
			{ bundleId: "dev.warp.Warp-Stable", name: "Warp", appPath: "/Applications/Warp.app" },
			{ bundleId: "org.example.future-shell", name: "Future Shell", appPath: "/Volumes/Tools/Renamed.app" },
		];
		expect(await discoverMacTerminalApps(async () => JSON.stringify(apps))).toEqual(apps);
	});

	it("keeps only embedded PNG icons, without forwarding icon paths or remote URLs", async () => {
		const app = { bundleId: "org.example.shell", name: "Shell", appPath: "/Applications/Shell.app" };
		const iconDataUrl = "data:image/png;base64,dGVzdA==";
		expect(await discoverMacTerminalApps(async () => JSON.stringify([{ ...app, iconDataUrl }]))).toEqual([{ ...app, iconDataUrl }]);
		for (const icon of ["https://example.test/icon.png", "/Applications/Shell.app/icon.png", "data:image/png;base64,"]) {
			expect(await discoverMacTerminalApps(async () => JSON.stringify([{ ...app, iconDataUrl: icon }]))).toEqual([app]);
		}
	});

	it("ignores malformed metadata rather than exposing unsafe paths or names", async () => {
		const app = { bundleId: "org.example.shell", name: "Shell", appPath: "/Applications/Shell.app" };
		const entries = [null, {}, { ...app, bundleId: 42 }, { ...app, name: "" }, { ...app, name: "  " },
			{ ...app, appPath: "relative.app" }, { ...app, appPath: "/tmp/script.sh" }, app];
		expect(await discoverMacTerminalApps(async () => JSON.stringify(entries))).toEqual([app]);
	});

	it.each(["not JSON", "{}"])("rejects a failed discovery response: %s", async (response) => {
		await expect(discoverMacTerminalApps(async () => response)).rejects.toThrow();
	});
});
