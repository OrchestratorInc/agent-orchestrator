import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";

// The packaged macOS app ships cloudflared, so turning on remote access never
// waits for an install (or needs Homebrew). Windows and Linux keep the
// winget/apt install offered in the app.
const VERSION = "2026.10.2";
const RELEASE_BASE = `https://github.com/cloudflare/cloudflared/releases/download/${VERSION}`;
const OUTPUT_DIR = path.resolve("cloudflared");
const DOWNLOAD_ATTEMPTS = 3;

// archive: the release asset; binary: the cloudflared inside it, which is the
// checksum Cloudflare lists in the release notes.
const TARGETS = {
	"darwin-arm64": {
		asset: "cloudflared-darwin-arm64.tgz",
		archive: "529a07089ac2d46762578389230fa148e5cb6ce2689200fc006956864042d17a",
		binary: "823f993ef362fba5e0e288db6f3452d8de34291632971c7a40b79548420e9a18",
	},
	"darwin-x64": {
		asset: "cloudflared-darwin-amd64.tgz",
		archive: "b64efd5a2772fc124b855f60eb3d33ba1a4f2ba67b19ddc58d32b705d42b82c9",
		binary: "4be2a4c0b76f6be4a84bea590743e85041949c294efcee4de86a140a9f11869a",
	},
};

if (process.platform !== "darwin") process.exit(0);
const target = TARGETS[`${process.platform}-${process.arch}`];
if (!target) throw new Error(`cloudflared ${VERSION} is not packaged for ${process.platform}-${process.arch}`);

const binaryPath = path.join(OUTPUT_DIR, "cloudflared");
await mkdir(OUTPUT_DIR, { recursive: true });
if (sha256(await readOrEmpty(binaryPath)) !== target.binary) {
	const archive = await download(`${RELEASE_BASE}/${target.asset}`);
	// Check the archive before tar reads it.
	if (sha256(archive) !== target.archive) throw new Error(`cloudflared archive checksum mismatch: got ${sha256(archive)}`);
	const work = await mkdtemp(path.join(OUTPUT_DIR, ".work-"));
	try {
		await writeFile(path.join(work, target.asset), archive);
		const tar = spawnSync("tar", ["-xzf", target.asset, "cloudflared"], { cwd: work, stdio: "inherit" });
		if (tar.error) throw tar.error;
		if (tar.status !== 0) throw new Error(`tar exited ${tar.status}`);
		const extracted = path.join(work, "cloudflared");
		if (sha256(await readFile(extracted)) !== target.binary) throw new Error("cloudflared binary checksum mismatch");
		await chmod(extracted, 0o755);
		await rename(extracted, binaryPath);
	} finally {
		await rm(work, { recursive: true, force: true });
	}
}

const licensePath = path.join(OUTPUT_DIR, "LICENSE-cloudflared");
if ((await readOrEmpty(licensePath)).length === 0) {
	const license = await download(`https://raw.githubusercontent.com/cloudflare/cloudflared/${VERSION}/LICENSE`);
	await writeFile(licensePath, license);
}

console.log(`Prepared cloudflared ${VERSION} for ${process.platform}-${process.arch}`);

function sha256(bytes) {
	return createHash("sha256").update(bytes).digest("hex");
}

async function readOrEmpty(file) {
	try {
		return await readFile(file);
	} catch (error) {
		if (error?.code === "ENOENT") return Buffer.alloc(0);
		throw error;
	}
}

async function download(url) {
	let lastError;
	for (let attempt = 1; attempt <= DOWNLOAD_ATTEMPTS; attempt += 1) {
		try {
			const response = await fetch(url, { redirect: "follow" });
			if (response.ok) return Buffer.from(await response.arrayBuffer());
			lastError = new Error(`HTTP ${response.status}`);
		} catch (error) {
			lastError = error;
		}
		if (attempt < DOWNLOAD_ATTEMPTS) await new Promise((resolve) => setTimeout(resolve, 1000 * attempt));
	}
	throw new Error(`download ${url}: ${lastError?.message ?? String(lastError)}`);
}
