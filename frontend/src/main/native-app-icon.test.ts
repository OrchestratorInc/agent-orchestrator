// @vitest-environment node
import { mkdir, mkdtemp, readdir, realpath, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { readNativeAppIcon } from "./native-app-icon";

const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==", "base64");
let root: string;
afterEach(async () => { if (root) await rm(root, { recursive: true, force: true }); });

async function fixture(resource = "Brand.icns") {
	root = await mkdtemp(path.join(os.tmpdir(), "ao-app-icon-"));
	const app = path.join(root, "Arbitrary Editor.app");
	const resources = path.join(app, "Contents", "Resources");
	await mkdir(resources, { recursive: true });
	await writeFile(path.join(resources, resource), "fixture");
	const fileIcon = vi.fn().mockResolvedValue("generic file-type icon");
	const run = vi.fn(async (command: string, args: readonly string[]) => {
		if (command === "/usr/bin/plutil") return resource;
		await writeFile(args[args.indexOf("--out") + 1]!, png);
		return "";
	});
	return { app, resources, options: { platform: "darwin" as const, cacheDir: path.join(root, "cache"), fileIcon, run } };
}

describe("native application icons", () => {
	it("reads the declared bundle resource instead of the generic .app file-type icon", async () => {
		const { app, resources, options } = await fixture();
		expect(await readNativeAppIcon(app, options)).toBe(`data:image/png;base64,${png.toString("base64")}`);
		expect(options.run).toHaveBeenCalledWith("/usr/bin/plutil", ["-extract", "CFBundleIconFile", "raw", "-o", "-", path.join(app, "Contents", "Info.plist")]);
		expect(options.run).toHaveBeenCalledWith("/usr/bin/sips", expect.arrayContaining([await realpath(path.join(resources, "Brand.icns"))]));
		expect(options.fileIcon).not.toHaveBeenCalled();
		expect(await readdir(options.cacheDir)).toEqual([]);
	});

	it("supports icon names without an extension, as used by Finder", async () => {
		const { app, resources, options } = await fixture("Finder.icns");
		options.run.mockImplementation(async (command, args) => {
			if (command === "/usr/bin/plutil") return "Finder";
			await writeFile(args[args.indexOf("--out") + 1]!, png);
			return "";
		});
		await readNativeAppIcon(app, options);
		expect(options.run).toHaveBeenCalledWith("/usr/bin/sips", expect.arrayContaining([await realpath(path.join(resources, "Finder.icns"))]));
	});

	it.each(["../Outside.icns", "/tmp/Outside.icns", "bad\\icon", "bad\0icon"])("rejects an invalid declared icon: %s", async (declared) => {
		const { app, options } = await fixture();
		options.run.mockResolvedValue(declared);
		await expect(readNativeAppIcon(app, options)).rejects.toThrow("Invalid");
		expect(options.run).toHaveBeenCalledTimes(1);
	});

	it("rejects resource symlinks that escape the app", async () => {
		const { app, resources, options } = await fixture();
		const outside = path.join(root, "outside.icns");
		await writeFile(outside, "fixture");
		await symlink(outside, path.join(resources, "Escaped.icns"));
		options.run.mockResolvedValue("Escaped.icns");
		await expect(readNativeAppIcon(app, options)).rejects.toThrow("outside");
	});

	it.each(["failure", "invalid PNG"])("cleans temporary files after conversion %s", async (failure) => {
		const { app, options } = await fixture();
		options.run.mockImplementation(async (command, args) => {
			if (command === "/usr/bin/plutil") return "Brand.icns";
			if (failure === "failure") throw new Error("conversion failed");
			await writeFile(args[args.indexOf("--out") + 1]!, "generic placeholder");
			return "";
		});
		await expect(readNativeAppIcon(app, options)).rejects.toThrow();
		expect(await readdir(options.cacheDir)).toEqual([]);
	});

	it("keeps the generic renderer fallback for terminal-only macOS editors", async () => {
		const { options } = await fixture();
		expect(await readNativeAppIcon("/opt/bin/nvim", options)).toBe("");
		expect(options.fileIcon).not.toHaveBeenCalled();
		expect(options.run).not.toHaveBeenCalled();
	});

	it.each(["win32", "linux"] as const)("keeps native executable icon lookup on %s", async (platform) => {
		const { options } = await fixture();
		expect(await readNativeAppIcon("/opt/editor", { ...options, platform })).toBe("generic file-type icon");
		expect(options.fileIcon).toHaveBeenCalledExactlyOnceWith("/opt/editor");
		expect(options.run).not.toHaveBeenCalled();
	});
});
