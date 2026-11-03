import * as fs from "node:fs";
import path from "node:path";
import { browserRuntimeKeyPath, resolveBrowserRuntimeToken } from "../browser-runtime-token.ts";

const [role, dataDir, barriers] = process.argv.slice(2);
const keyPath = browserRuntimeKeyPath(dataDir);
const lockPath = `${keyPath}.lock`;
const marker = (name) => path.join(barriers, name);
const mark = (name) => fs.writeFileSync(marker(name), "ready\n");
function wait(name) {
	const deadline = Date.now() + 10_000;
	while (!fs.existsSync(marker(name))) {
		if (Date.now() > deadline) throw new Error(`timed out waiting for ${name}`);
		Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 1);
	}
}

let paused = false;
const io = {
	...fs,
	readFileSync(target, encoding) {
		const value = fs.readFileSync(target, encoding);
		if (role === "stale-reader" && String(target) === lockPath && !paused) {
			paused = true;
			mark("stale-reader-observed-lock");
			wait("new-owner-before-rename");
		}
		return value;
	},
	renameSync(oldPath, newPath) {
		if (role === "new-owner" && String(oldPath) === keyPath && !paused) {
			paused = true;
			mark("new-owner-before-rename");
			wait("stale-reader-returned");
		}
		fs.renameSync(oldPath, newPath);
	},
};
const token = resolveBrowserRuntimeToken(dataDir, io);
if (role === "stale-reader") mark("stale-reader-returned");
process.stdout.write(token);
