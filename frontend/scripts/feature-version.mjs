export function computeFeatureVersion(baseVersion, pr, shortSha, nowIso) {
	const bare = String(baseVersion).replace(/^(desktop-)?v/, "");
	const now = new Date(nowIso);
	const ts =
		String(now.getUTCFullYear()) +
		String(now.getUTCMonth() + 1).padStart(2, "0") +
		String(now.getUTCDate()).padStart(2, "0") +
		String(now.getUTCHours()).padStart(2, "0") +
		String(now.getUTCMinutes()).padStart(2, "0");
	return `${bare}-pr${pr}.${ts}+${shortSha}`;
}

export function parseFeatureBuild(version) {
	const bare = String(version).replace(/^(desktop-)?v/, "");
	const m = /^(.+)-pr(\d+)\.\d{12}/.exec(bare);
	if (!m) return null;
	return { base: m[1], pr: Number(m[2]) };
}

if (import.meta.url === `file://${process.argv[1]}`) {
	const [, , baseVersion, pr, shortSha] = process.argv;
	if (!baseVersion || !pr || !shortSha) {
		process.stderr.write("usage: node feature-version.mjs <baseVersion> <pr> <shortSha>\n");
		process.exit(2);
	}
	process.stdout.write(computeFeatureVersion(baseVersion, pr, shortSha, new Date().toISOString()));
}
