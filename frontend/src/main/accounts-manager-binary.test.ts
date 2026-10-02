import { describe, expect, it } from "vitest";
import path from "node:path";

import { resolveAccountsManagerBinary } from "./accounts-manager-binary";

describe("resolveAccountsManagerBinary", () => {
	it("honors an explicit binary path", () => {
		expect(
			resolveAccountsManagerBinary({
				explicitPath: "/custom/manager",
				isPackaged: true,
				resourcesPath: "/Resources",
				appPath: "/app",
				platform: "darwin",
			}),
		).toBe("/custom/manager");
	});

	it("resolves the packaged platform binary", () => {
		expect(
			resolveAccountsManagerBinary({
				isPackaged: true,
				resourcesPath: "/Resources",
				appPath: "/app",
				platform: "linux",
			}),
		).toBe(path.join("/Resources", "accounts-manager", "ao-accounts-manager"));
		expect(
			resolveAccountsManagerBinary({
				isPackaged: true,
				resourcesPath: "C:\\Resources",
				appPath: "C:\\app",
				platform: "win32",
			}),
		).toContain("ao-accounts-manager.exe");
	});

	it("uses a validated Windows dev manifest path", () => {
		const appPath = path.resolve("/repo/frontend");
		const binary = path.join(appPath, "accounts-manager", "dev-1", "ao-accounts-manager.exe");
		expect(
			resolveAccountsManagerBinary({
				isPackaged: false,
				resourcesPath: "/unused",
				appPath,
				platform: "win32",
				readDevManifest: () => JSON.stringify({ path: binary }),
			}),
		).toBe(binary);
	});

	it("rejects a Windows dev manifest outside the build directory", () => {
		const appPath = path.resolve("/repo/frontend");
		expect(
			resolveAccountsManagerBinary({
				isPackaged: false,
				resourcesPath: "/unused",
				appPath,
				platform: "win32",
				readDevManifest: () => JSON.stringify({ path: "/tmp/unrelated.exe" }),
			}),
		).toBe(path.join(appPath, "accounts-manager", "ao-accounts-manager.exe"));
	});
});
