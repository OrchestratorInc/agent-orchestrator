import { readdirSync, writeFileSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { writeBlockmap } from "./blockmap.mjs";

export function selectInstallers(filenames, version) {
	const versioned = filenames.filter((f) => f.includes(version));
	const isDarwinZip = (f) => f.endsWith(".zip") && f.includes("darwin");
	return {
		win: versioned.filter((f) => f.endsWith(".exe")),
		linux: versioned.filter((f) => f.endsWith(".AppImage")),
		macArm64: versioned.filter((f) => isDarwinZip(f) && f.includes("arm64")),
		macX64: versioned.filter((f) => isDarwinZip(f) && !f.includes("arm64")),
	};
}

export function feedFilename(channel, platform) {
	const suffix = platform === "mac" ? "-mac" : platform === "linux" ? "-linux" : "";
	return `${channel}${suffix}.yml`;
}

export function buildYml(version, files, releaseDate, important = false, minimumSystemVersion = null) {
	const lines = [`version: ${version}`, "files:"];
	for (const f of files) {
		lines.push(`  - url: ${f.url}`);
		lines.push(`    sha512: ${f.sha512}`);
		lines.push(`    size: ${f.size}`);
	}
	lines.push(`path: ${files[0].url}`);
	lines.push(`sha512: ${files[0].sha512}`);
	lines.push(`releaseDate: '${releaseDate}'`);
	if (important) lines.push("important: true");
	if (minimumSystemVersion) lines.push(`minimumSystemVersion: ${minimumSystemVersion}`);
	return lines.join("\n") + "\n";
}

export const MAC_MINIMUM_DARWIN_VERSION = "22.0.0";

export async function generateFeeds(dir, rawVersion, channel, releaseDate, important = false) {
	const version = rawVersion.split("+")[0];
	const sel = selectInstallers(readdirSync(dir), version);
	const groups = [
		{ platform: "win", names: sel.win },
		{ platform: "linux", names: sel.linux },
		{ platform: "mac", names: [...sel.macArm64, ...sel.macX64] },
	];
	for (const { platform, names } of groups) {
		if (names.length === 0) continue;
		const files = [];
		for (const name of names) {
			const { sha512, size } =
				platform === "mac"
					? hashFile(join(dir, name))
					: await writeBlockmap(join(dir, name));
			files.push({ url: name, sha512, size });
		}
		const minimumSystemVersion = platform === "mac" ? MAC_MINIMUM_DARWIN_VERSION : null;
		writeFileSync(
			join(dir, feedFilename(channel, platform)),
			buildYml(version, files, releaseDate, important, minimumSystemVersion),
		);
	}
}

if (import.meta.url === `file://${process.argv[1]}`) {
	const [, , dir, version, channel] = process.argv;
	if (!dir || !version || !channel) {
		process.stderr.write("usage: node feed.mjs <dir> <version> <channel>\n");
		process.exit(2);
	}
	const important = process.argv.includes("--important");
	generateFeeds(dir, version, channel, new Date().toISOString(), important).catch((err) => {
		process.stderr.write(`${err.stack || err}\n`);
		process.exit(1);
	});
}

export function hashFile(filePath) {
	const data = readFileSync(filePath);
	const sha512 = createHash("sha512").update(data).digest("base64");
	const size = statSync(filePath).size;
	return { sha512, size };
}
