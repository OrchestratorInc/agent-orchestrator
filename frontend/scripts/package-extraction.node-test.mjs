import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { crc32, deflateRawSync } from "node:zlib";

// Generate a ZIP without a fixture download or another ZIP dependency.
function createArchive(entries) {
	const files = [];
	const directory = [];
	let offset = 0;
	for (const [name, contents] of entries) {
		const filename = Buffer.from(name);
		const compressed = deflateRawSync(contents);
		const local = Buffer.alloc(30);
		local.writeUInt32LE(0x04034b50, 0);
		local.writeUInt16LE(20, 4);
		local.writeUInt16LE(8, 8);
		local.writeUInt32LE(crc32(contents), 14);
		local.writeUInt32LE(compressed.length, 18);
		local.writeUInt32LE(contents.length, 22);
		local.writeUInt16LE(filename.length, 26);
		const central = Buffer.alloc(46);
		central.writeUInt32LE(0x02014b50, 0);
		central.writeUInt16LE(20, 4);
		local.copy(central, 6, 4, 30);
		central.writeUInt32LE(offset, 42);
		files.push(local, filename, compressed);
		directory.push(central, filename);
		offset += local.length + filename.length + compressed.length;
	}
	const centralDirectory = Buffer.concat(directory);
	const end = Buffer.alloc(22);
	end.writeUInt32LE(0x06054b50, 0);
	end.writeUInt16LE(entries.length, 8);
	end.writeUInt16LE(entries.length, 10);
	end.writeUInt32LE(centralDirectory.length, 12);
	end.writeUInt32LE(offset, 16);
	return Buffer.concat([...files, centralDirectory, end]);
}

test("Forge's packager completes extraction of large ZIP entries", { timeout: 30_000 }, async (t) => {
	const require = createRequire(import.meta.url);
	const forgeRequire = createRequire(require.resolve("@electron-forge/core"));
	const packagerRequire = createRequire(forgeRequire.resolve("@electron/packager"));
	const extractorPath = packagerRequire.resolve("extract-zip");
	const directory = await mkdtemp(path.join(tmpdir(), "ao-package-extraction-"));
	t.after(() => rm(directory, { recursive: true, force: true }));
	const zipPath = path.join(directory, "electron.zip");
	const output = path.join(directory, "out");
	// A large deflated entry exercises the backpressure/resume bug in yauzl@2.
	// Thousands of tiny entries do not reproduce it.
	const contents = Buffer.alloc(64 * 1024 * 1024, 0x61);
	await writeFile(zipPath, createArchive([
		["electron", contents],
		["resources/second.txt", Buffer.from("nested entry\n")],
	]));

	// Run without a test runner or a timer keeping the extractor's event loop
	// alive. A stalled promise can otherwise look like a successful exit 0.
	const result = spawnSync(process.execPath, ["-e", `
		const { pathToFileURL } = require("node:url");
		import(pathToFileURL(process.argv[1]).href)
			.then(({ default: extract }) => extract(process.argv[2], { dir: process.argv[3] }))
			.then(() => process.stdout.write("extraction complete\\n"))
			.catch((error) => { console.error(error); process.exitCode = 1; });
	`, extractorPath, zipPath, output], { encoding: "utf8", timeout: 20_000 });
	assert.ifError(result.error);
	assert.equal(result.status, 0, result.stderr);
	assert.equal(result.stdout, "extraction complete\n", result.stderr);
	assert.deepEqual(await readFile(path.join(output, "electron")), contents);
	assert.equal(await readFile(path.join(output, "resources", "second.txt"), "utf8"), "nested entry\n");
});
