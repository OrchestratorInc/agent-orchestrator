import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readFile, realpath, rm } from "node:fs/promises";
import path from "node:path";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const PNG_HEADER = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

type IconOptions = {
	platform: NodeJS.Platform;
	cacheDir: string;
	fileIcon: (filePath: string) => Promise<string>;
	run?: (command: string, args: readonly string[]) => Promise<string>;
};

/** macOS file-type icons can be generic for .app directories. Read the bundle's declared icon instead. */
export async function readNativeAppIcon(appPath: string, options: IconOptions): Promise<string> {
	if (options.platform !== "darwin") return options.fileIcon(appPath);
	// A bare CLI has no application branding; callers keep their existing fallback.
	if (!appPath.toLowerCase().endsWith(".app")) return "";
	const run = options.run ?? (async (command, args) => {
		const { stdout } = await execFileAsync(command, args, { encoding: "utf8", timeout: 5_000, maxBuffer: 1024 * 1024 });
		return stdout;
	});
	const declared = (await run("/usr/bin/plutil", ["-extract", "CFBundleIconFile", "raw", "-o", "-", path.join(appPath, "Contents", "Info.plist")])).trim();
	if (!declared || declared !== path.basename(declared) || /[\\\x00-\x1f]/.test(declared)) throw new Error("Invalid application icon resource.");
	const resources = await realpath(path.join(appPath, "Contents", "Resources"));
	const icon = await realpath(path.join(resources, path.extname(declared) ? declared : `${declared}.icns`));
	const relative = path.relative(resources, icon);
	if (!relative || relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) throw new Error("Application icon is outside its resources.");
	await mkdir(options.cacheDir, { recursive: true });
	const temporary = await mkdtemp(path.join(options.cacheDir, "app-"));
	try {
		const output = path.join(temporary, "icon.png");
		await run("/usr/bin/sips", ["-s", "format", "png", "-Z", "64", icon, "--out", output]);
		const png = await readFile(output);
		if (png.length > 1024 * 1024 || !png.subarray(0, PNG_HEADER.length).equals(PNG_HEADER)) throw new Error("Application icon could not be decoded.");
		return `data:image/png;base64,${png.toString("base64")}`;
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
}
