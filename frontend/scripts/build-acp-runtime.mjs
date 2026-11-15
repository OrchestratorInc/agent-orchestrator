import { createHash } from "node:crypto";
import {
	cpSync,
	existsSync,
	mkdirSync,
	readFileSync,
	renameSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import {
	archiveExtraction,
	createWorkDirectory,
	npmInvocation,
	patchClaudeContextUsage,
	patchClaudeContextWindowProbe,
	patchClaudeFoldedPromptSettlement,
	patchClaudeHibernationCheck,
	patchClaudeRetryDetails,
	patchClaudeStaleIdleDebt,
	patchClaudeSteerSettlement,
	pruneNodeDistribution,
	runtimeSourceFiles,
} from "./build-acp-runtime-helpers.mjs";

const NODE_VERSION = "22.23.5";
const DOWNLOAD_ATTEMPTS = 3;
const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const sourceDir = join(frontendRoot, "acp-runtime");
const outDir = join(frontendRoot, "resources", "acp-runtime");

const platform = { darwin: "darwin", linux: "linux", win32: "win" }[process.platform];
const arch = { x64: "x64", arm64: "arm64" }[process.arch];
if (!platform || !arch) {
	throw new Error(`ACP runtime packaging is unsupported on ${process.platform}/${process.arch}`);
}

const extension = process.platform === "win32" ? "zip" : "tar.gz";
const archiveName = `node-v${NODE_VERSION}-${platform}-${arch}.${extension}`;
const baseURL = `https://nodejs.org/dist/v${NODE_VERSION}`;
const runtimeSources = runtimeSourceFiles();
const signature = createHash("sha256");
for (const source of runtimeSources) {
	signature.update(readFileSync(join(sourceDir, source)));
}
const buildSignature = signature
	.update(readFileSync(fileURLToPath(import.meta.url)))
	.update(readFileSync(join(scriptsDir, "build-acp-runtime-helpers.mjs")))
	.update(`node=${NODE_VERSION};platform=${platform};arch=${arch}`)
	.digest("hex");
const markerPath = join(outDir, ".ao-acp-runtime.json");
const expectedNode = process.platform === "win32"
	? join(outDir, "node", "node.exe")
	: join(outDir, "node", "bin", "node");
const expectedAdapter = join(
	outDir,
	"node_modules",
	"@agentclientprotocol",
	"claude-agent-acp",
	"dist",
	"index.js",
);
const claudeAdapter = join(
	outDir,
	"node_modules",
	"@agentclientprotocol",
	"claude-agent-acp",
	"dist",
	"acp-agent.js",
);
if (existsSync(markerPath) && existsSync(expectedNode) && existsSync(expectedAdapter)) {
	const marker = JSON.parse(readFileSync(markerPath, "utf8"));
	if (marker.signature === buildSignature) process.exit(0);
}

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });
for (const source of runtimeSources) {
	cpSync(join(sourceDir, source), join(outDir, source));
}

const npm = npmInvocation(["ci", "--omit=dev", "--omit=optional", "--ignore-scripts"]);
run(npm.command, npm.args, { cwd: outDir });
patchClaudeRetryDetails(claudeAdapter);
patchClaudeContextUsage(claudeAdapter);
patchClaudeContextWindowProbe(claudeAdapter);
patchClaudeHibernationCheck(claudeAdapter);
patchClaudeFoldedPromptSettlement(claudeAdapter);
patchClaudeStaleIdleDebt(claudeAdapter);
patchClaudeSteerSettlement(claudeAdapter);

const anthropicDir = join(outDir, "node_modules", "@anthropic-ai");
const nativeClaudePackages = [
	"claude-agent-sdk-darwin-arm64",
	"claude-agent-sdk-darwin-x64",
	"claude-agent-sdk-linux-arm64",
	"claude-agent-sdk-linux-arm64-musl",
	"claude-agent-sdk-linux-x64",
	"claude-agent-sdk-linux-x64-musl",
	"claude-agent-sdk-win32-arm64",
	"claude-agent-sdk-win32-x64",
];
for (const name of nativeClaudePackages) {
	rmSync(join(anthropicDir, name), { recursive: true, force: true });
}

const workDir = createWorkDirectory(outDir);
try {
	const [sums, archive] = await Promise.all([
		download(`${baseURL}/SHASUMS256.txt`),
		download(`${baseURL}/${archiveName}`),
	]);
	const expectedLine = sums.toString("utf8").split(/\r?\n/).find((line) => line.endsWith(`  ${archiveName}`));
	if (!expectedLine) throw new Error(`${archiveName} is absent from Node's SHASUMS256.txt`);
	const expected = expectedLine.split(/\s+/)[0];
	const actual = createHash("sha256").update(archive).digest("hex");
	if (actual !== expected) throw new Error(`Node archive checksum mismatch: expected ${expected}, got ${actual}`);

	const archivePath = join(workDir, archiveName);
	writeFileSync(archivePath, archive);
	const extraction = archiveExtraction(archivePath, workDir);
	run(extraction.command, extraction.args);
	const extracted = join(workDir, basename(archiveName, `.${extension}`));
	const nodeOut = join(outDir, "node");
	if (!existsSync(extracted)) throw new Error(`Node archive did not contain ${extracted}`);
	renameSync(extracted, nodeOut);
	pruneNodeDistribution(nodeOut);
} finally {
	rmSync(workDir, { recursive: true, force: true });
}

for (const name of nativeClaudePackages) {
	if (existsSync(join(anthropicDir, name))) {
		throw new Error(`ACP runtime contains packaged Claude Code dependency ${name}`);
	}
}
writeFileSync(markerPath, `${JSON.stringify({ signature: buildSignature, node: NODE_VERSION }, null, 2)}\n`);

function run(command, args, options = {}) {
	const result = spawnSync(command, args, { stdio: "inherit", windowsHide: true, ...options });
	if (result.error) throw result.error;
	if (result.status !== 0) throw new Error(`${command} exited ${result.status}`);
}

async function download(url) {
	let lastError;
	for (let attempt = 1; attempt <= DOWNLOAD_ATTEMPTS; attempt += 1) {
		try {
			const response = await fetch(url);
			if (response.ok) return Buffer.from(await response.arrayBuffer());
			lastError = new Error(`HTTP ${response.status}`);
		} catch (error) {
			lastError = error;
		}
		if (attempt < DOWNLOAD_ATTEMPTS) await new Promise((resolve) => setTimeout(resolve, 1000 * attempt));
	}
	throw new Error(`download ${url}: ${lastError?.message ?? String(lastError)}`);
}
