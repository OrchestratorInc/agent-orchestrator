// Reads the desktop's harness logo registry, which is the source of truth for
// every brand mark: frontend/src/renderer/components/AgentAvatar.tsx maps each
// harness to one asset, or to a { light, dark } pair, under
// frontend/src/renderer/assets/agents/.
//
// Shared by scripts/sync-harness-logos.mjs, which rasterises those assets for the
// app, and by lib/harnessLogo.test.ts, which fails when the two drift apart.

import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";

export const DESKTOP_AVATAR = join("frontend", "src", "renderer", "components", "AgentAvatar.tsx");
export const DESKTOP_ASSETS = join("frontend", "src", "renderer", "assets", "agents");

/** The first 16 hex digits of a file's SHA-256, recorded so the test can spot a stale sync. */
export function sourceHash(bytes) {
	return createHash("sha256").update(bytes).digest("hex").slice(0, 16);
}

/**
 * Every harness the desktop draws a mark for, keyed by harness id, with the
 * asset file names under DESKTOP_ASSETS. `dark` is set only when the desktop
 * draws a different file on a dark background.
 *
 * @param {string} repoRoot
 * @returns {Record<string, { light: string; dark?: string }>}
 */
export function readDesktopLogos(repoRoot) {
	const source = readFileSync(join(repoRoot, DESKTOP_AVATAR), "utf8");

	const files = new Map();
	for (const [, name, file] of source.matchAll(/^import (\w+) from "\.\.\/assets\/agents\/([^"]+)";$/gm)) {
		files.set(name, file);
	}

	const block = source.match(/^const LOGOS: AgentLogoSources = \{\n([\s\S]*?)\n\};$/m);
	if (!block) throw new Error(`No LOGOS map found in ${DESKTOP_AVATAR}`);

	const fileFor = (name, key) => {
		const file = files.get(name);
		if (!file) throw new Error(`${DESKTOP_AVATAR}: "${key}" uses ${name}, which is not an asset import`);
		return file;
	};

	const logos = {};
	for (const line of block[1].split("\n")) {
		if (!line.trim()) continue;
		const entry = line.match(/^\t(?:"([^"]+)"|(\w+)): (?:\{ light: (\w+), dark: (\w+) \}|(\w+)),$/);
		if (!entry) throw new Error(`${DESKTOP_AVATAR}: cannot read LOGOS entry ${JSON.stringify(line)}`);
		const [, quoted, bare, light, dark, single] = entry;
		const key = quoted ?? bare;
		if (single) {
			logos[key] = { light: fileFor(single, key) };
		} else {
			const lightFile = fileFor(light, key);
			const darkFile = fileFor(dark, key);
			logos[key] = lightFile === darkFile ? { light: lightFile } : { light: lightFile, dark: darkFile };
		}
	}
	return logos;
}
