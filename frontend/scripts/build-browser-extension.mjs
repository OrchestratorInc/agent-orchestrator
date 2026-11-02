// Builds the AO Browser Bridge Chrome extension (unpacked) into
// frontend/browser-extension/. The daemon ships it beside its binary and
// `ao browser chrome setup` installs it under the AO data directory.
import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(frontend, "src", "browser-extension");
export const outDir = path.join(frontend, "browser-extension");

// Each script is a self-contained classic script: the service worker,
// the on-demand content script, and the three extension pages.
export const entries = [
	["background", "background.ts"],
	["content", "content.ts"],
	["consent", "pages/consent.ts"],
	["popup", "pages/popup.ts"],
	["options", "pages/options.ts"],
];

export async function buildBrowserExtension() {
	rmSync(outDir, { recursive: true, force: true });
	mkdirSync(outDir, { recursive: true });
	for (const [name, entry] of entries) {
		await build({
			configFile: false,
			logLevel: "warn",
			publicDir: false,
			build: {
				outDir,
				emptyOutDir: false,
				minify: false,
				sourcemap: false,
				target: "chrome120",
				lib: { entry: path.join(source, entry), formats: ["iife"], name: `aoBridge_${name}`, fileName: () => `${name}.js` },
			},
		});
	}
	for (const file of ["consent.html", "popup.html", "options.html", "pages.css"]) {
		copyFileSync(path.join(source, "pages", file), path.join(outDir, file));
	}
	const manifest = JSON.parse(readFileSync(path.join(source, "manifest.json"), "utf8"));
	writeFileSync(path.join(outDir, "manifest.json"), `${JSON.stringify(manifest, null, "\t")}\n`);
	return outDir;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	const dir = await buildBrowserExtension();
	console.log(`AO Browser Bridge built in ${path.relative(process.cwd(), dir) || dir}`);
}
