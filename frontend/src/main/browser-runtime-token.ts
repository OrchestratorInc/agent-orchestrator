import { createHmac, randomBytes } from "node:crypto";
import { closeSync, constants, fchmodSync, mkdirSync, openSync, readFileSync, writeFileSync, type PathLike } from "node:fs";
import path from "node:path";

const KEY_FILE = "browser-runtime.key";
const KEY_BYTES = 32;
const KEY_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const DERIVATION_LABEL = "ao-browser-runtime-token-v1";
const PUBLISH_ATTEMPTS = 3;
const APPEND_FLAGS = constants.O_WRONLY | constants.O_CREAT | constants.O_APPEND | (constants.O_NOFOLLOW ?? 0);

export type BrowserRuntimeKeyFs = {
	closeSync: (fd: number) => void;
	fchmodSync: (fd: number, mode: number) => void;
	mkdirSync: (target: PathLike, options: { recursive: true; mode: number }) => void;
	openSync: (target: PathLike, flags: number, mode: number) => number;
	readFileSync: (target: PathLike, encoding: "utf8") => string;
	writeFileSync: (target: number, data: string) => void;
};

const nodeKeyFs: BrowserRuntimeKeyFs = { closeSync, fchmodSync, mkdirSync, openSync, readFileSync, writeFileSync };

export function browserRuntimeKeyPath(dataDir: string): string {
	return path.join(dataDir, KEY_FILE);
}

/** The owner-only key survives app restarts; the derived token reaches the daemon only through private stdin. */
export function resolveBrowserRuntimeToken(dataDir: string, io: BrowserRuntimeKeyFs = nodeKeyFs): string {
	return createHmac("sha256", readOrCreateKey(dataDir, io)).update(DERIVATION_LABEL).digest("base64url");
}

function readOrCreateKey(dataDir: string, io: BrowserRuntimeKeyFs): Buffer {
	io.mkdirSync(dataDir, { recursive: true, mode: 0o750 });
	const keyPath = browserRuntimeKeyPath(dataDir);
	const existing = readKey(keyPath, io);
	if (existing) return existing;

	// Append candidates instead of displacing a key or reclaiming another owner's
	// lock. Once a valid line is complete, later appends cannot replace that winner.
	for (let attempt = 0; attempt < PUBLISH_ATTEMPTS; attempt += 1) {
		const candidate = randomBytes(KEY_BYTES).toString("base64url");
		try {
			const fd = io.openSync(keyPath, APPEND_FLAGS, 0o600);
			try {
				io.fchmodSync(fd, 0o600);
				io.writeFileSync(fd, `\n${candidate}\n`);
			} finally {
				io.closeSync(fd);
			}
		} catch (error) {
			const winner = readKey(keyPath, io);
			if (winner) return winner;
			throw error;
		}
		const winner = readKey(keyPath, io);
		if (winner) return winner;
		// Short writes from concurrent appenders can split a candidate. Retry, but
		// never return a candidate unless a complete record on disk backs it.
	}
	throw new Error(`could not establish a browser runtime key at ${keyPath}`);
}

function readKey(keyPath: string, io: BrowserRuntimeKeyFs): Buffer | null {
	let raw: string;
	try {
		raw = io.readFileSync(keyPath, "utf8");
	} catch {
		return null;
	}
	// Keep existing one-line keys readable. New candidates start with LF; only
	// their terminated records are eligible, even if an incomplete tail parses.
	const records = raw.includes("\n") ? raw.split("\n").slice(0, -1) : [raw];
	for (const record of records) {
		const trimmed = record.trim();
		if (!KEY_PATTERN.test(trimmed)) continue;
		const decoded = Buffer.from(trimmed, "base64url");
		if (decoded.length === KEY_BYTES) return decoded;
	}
	return null;
}
