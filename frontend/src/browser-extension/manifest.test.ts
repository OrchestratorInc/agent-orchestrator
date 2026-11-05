import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import manifest from "./manifest.json";
import { HOST_NAME } from "./protocol";

// Must equal backend/internal/browserchrome.ExtensionID: the native host
// manifest only admits this extension origin.
const EXTENSION_ID = "eefgahhblbmjpccfcnpkhkadgcccnibg";

function extensionId(key: string): string {
	const digest = createHash("sha256").update(Buffer.from(key, "base64")).digest("hex").slice(0, 32);
	return [...digest].map((c) => String.fromCharCode("a".charCodeAt(0) + Number.parseInt(c, 16))).join("");
}

describe("extension manifest", () => {
	it("pins the extension ID the AO native host allows", () => {
		expect(extensionId(manifest.key)).toBe(EXTENSION_ID);
		expect(HOST_NAME).toBe("dev.ao.browser_bridge");
	});

	it("shows Orchestrator's logo in Chrome", () => {
		const sizes = { "16": "icon-16.png", "32": "icon-32.png", "48": "icon-48.png", "128": "icon-128.png" };
		expect(manifest.icons).toEqual(sizes);
		expect(manifest.action.default_icon).toEqual(sizes);
		for (const [size, file] of Object.entries(sizes)) {
			const png = readFileSync(path.join(process.cwd(), "src/browser-extension/icons", file));
			expect(png.readUInt32BE(16)).toBe(Number(size));
			expect(png.readUInt32BE(20)).toBe(Number(size));
		}
	});

	it("asks only for what Chrome mode needs and never declares content scripts", () => {
		expect(manifest.manifest_version).toBe(3);
		expect([...manifest.permissions].sort()).toEqual(
			["alarms", "debugger", "nativeMessaging", "scripting", "storage", "tabGroups", "tabs", "webNavigation", "webRequest"].sort(),
		);
		expect(manifest.host_permissions).toEqual(["<all_urls>"]);
		// Content runs only in tabs an approved session owns, injected on demand.
		expect(manifest).not.toHaveProperty("content_scripts");
	});
});
