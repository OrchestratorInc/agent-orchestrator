#!/usr/bin/env node
// Verifies that the docs still name the newest stable release and npm version.
// Run on a schedule with --mode sync, or in any branch with --mode review to
// make version bumps part of normal review.
// Usage: node scripts/check-docs-version.mjs [--mode sync|review] [docs-root]
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { join } from "node:path";

const mode = process.argv.includes("--mode")
	? process.argv[process.argv.indexOf("--mode") + 1]
	: "sync";
const positional = process.argv.slice(2).filter((a, i, argv) => !a.startsWith("-") && argv[i - 1] !== "--mode");
const root = positional[0] ?? new URL("..", import.meta.url).pathname;

const https = (url) =>
	JSON.parse(execFileSync("curl", ["-sf", "--max-time", "15", url], { encoding: "utf8" }));

const github = https("https://api.github.com/repos/OrchestratorInc/agent-orchestrator/releases/latest");
const latestTag = github.tag_name; // e.g. "v0.14.10"
const published = new Date(github.published_at).toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric" });
const npm = https("https://registry.npmjs.org/@aoagents/ao");
const npmVersion = npm["dist-tags"].latest;

const problems = [];
const check = (file, label, pattern, expect) => {
	const text = readFileSync(join(root, "content", file), "utf8");
	if (!pattern.test(text)) {
		problems.push(`${file}: ${label} is out of date - expected ${expect}`);
		return true;
	}
	return false;
};

const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const tagLink = `\\[${esc(latestTag)}\\]`;
check("index.mdx", "stable release line", new RegExp(`latest stable release, ${tagLink}.*published ${esc(published)}`), `${latestTag} published ${published}`);
check("index.ja.mdx", "stable release line (ja)", new RegExp(tagLink), `${latestTag}`);
check("index.zh-CN.mdx", "stable release line (zh)", new RegExp(tagLink), `${latestTag}`);
check("installation.mdx", "npm quick-start line", new RegExp(`\`${esc(npmVersion)}\` is the final npm release`), `npm version ${npmVersion}`);
for (const f of ["installation.ja.mdx", "installation.zh-CN.mdx"]) {
	check(f, "npm quick-start line (translated)", new RegExp(`\`${esc(npmVersion)}\``), `npm version ${npmVersion}`);
}

if (problems.length === 0) {
	console.log(`docs versions are current: ${latestTag} (${published}), npm ${npmVersion}`);
	process.exit(0);
}
if (mode === "review") {
	console.error("Docs do not match the latest release yet:");
	for (const p of problems) console.error(`  - ${p}`);
	process.exit(1);
}
console.log(`DOCS_GAP\nLatest stable: ${latestTag} published ${published}, npm ${npmVersion}`);
for (const p of problems) console.log(`  - ${p}`);
process.exit(0);
