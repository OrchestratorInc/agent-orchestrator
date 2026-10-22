import { createHmac, randomBytes } from "node:crypto";
import {
	chmodSync,
	linkSync,
	mkdirSync,
	readFileSync,
	renameSync,
	rmSync,
	writeFileSync,
	type PathLike,
} from "node:fs";
import path from "node:path";

// Owner-only key under the AO data dir. The daemon's browser runtime token is
// derived from it rather than stored directly, so the file is a key and not a
// replayable credential, and it lives beside the other owner-only AO secrets
// (cloud auth tokens, remotes credentials, telemetry authority).
const KEY_FILE = "browser-runtime.key";
const KEY_BYTES = 32;
const KEY_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const DERIVATION_LABEL = "ao-browser-runtime-token-v1";
// Publishing is attempted a few times so a writer that is not holding the lock
// (an older build, or a stale lock holder) cannot cost us the key.
const PUBLISH_ATTEMPTS = 3;
// The critical section is a handful of file operations, so a lock older than
// this belongs to a process that died holding it.
const LOCK_STALE_MS = 30_000;
const LOCK_TIMEOUT_MS = 5_000;
const LOCK_POLL_MS = 20;

/**
 * The filesystem surface this module needs, injected so the repair protocol can
 * be exercised against a real disk with an interleaving forced at a chosen point.
 */
export type BrowserRuntimeKeyFs = {
	chmodSync: (target: PathLike, mode: number) => void;
	linkSync: (target: PathLike, destination: PathLike) => void;
	mkdirSync: (target: PathLike, options: { recursive: true; mode: number }) => void;
	readFileSync: (target: PathLike, encoding: "utf8") => string;
	renameSync: (oldPath: PathLike, newPath: PathLike) => void;
	rmSync: (target: PathLike, options: { force: true }) => void;
	writeFileSync: (target: PathLike, data: string, options: { mode: number }) => void;
};

const nodeKeyFs: BrowserRuntimeKeyFs = {
	chmodSync,
	linkSync,
	mkdirSync,
	readFileSync,
	renameSync,
	rmSync,
	writeFileSync,
};

export function browserRuntimeKeyPath(dataDir: string): string {
	return path.join(dataDir, KEY_FILE);
}

/**
 * The token the desktop app presents to the daemon's browser runtime broker.
 *
 * It must survive an app restart: AO_KEEP_DAEMON (and a wrapper that pins
 * AO_APP_RUN_ID) lets a daemon outlive the Electron process that spawned it, and
 * the next launch adopts that daemon instead of replacing it. A per-launch
 * random token made such an adoption permanently unauthenticatable — the daemon
 * held the previous launch's token — so the browser panel could never come back
 * for the life of the app. Deriving the token from a per-data-dir key lets an
 * adopted daemon recognize its own app across restarts.
 *
 * Never place the result in argv or the daemon environment; it is handed over
 * through the child's private stdin pipe.
 */
export function resolveBrowserRuntimeToken(dataDir: string, io: BrowserRuntimeKeyFs = nodeKeyFs): string {
	return createHmac("sha256", readOrCreateKey(dataDir, io)).update(DERIVATION_LABEL).digest("base64url");
}

/**
 * Read the data dir's key, creating it on first use.
 *
 * Creating and repairing the key is serialized across launches with a lock file
 * published by link(). Without it, a launch could read a damaged key, stall,
 * find that another launch had already repaired it, and then move that launch's
 * valid key aside — leaving the launch that returned a token holding a token the
 * key on disk no longer backs. Under the lock the damaged file is re-checked and
 * moved aside only while it is still damaged.
 *
 * link() needs a filesystem with hard-link support (NTFS, APFS, ext4 and
 * friends). Where it is unavailable the error propagates and the caller falls
 * back to a per-launch token rather than publishing without a lock.
 */
function readOrCreateKey(dataDir: string, io: BrowserRuntimeKeyFs): Buffer {
	io.mkdirSync(dataDir, { recursive: true, mode: 0o750 });
	const keyPath = browserRuntimeKeyPath(dataDir);
	// Fast path: a usable key is never displaced, so it needs no lock. Only the
	// create-and-repair path below can change what is on disk.
	const usable = readKey(keyPath, io);
	if (usable) return usable;
	const resolved = withKeyLock(keyPath, io, () => {
		const current = readKey(keyPath, io);
		if (current) return current;
		for (let attempt = 0; attempt < PUBLISH_ATTEMPTS; attempt += 1) {
			displaceUnusableKey(keyPath, io);
			const published = publishKey(keyPath, io);
			if (published) return published;
		}
		return null;
	});
	if (resolved) return resolved;
	throw new Error(`could not establish a browser runtime key at ${keyPath}`);
}

/**
 * Publish a candidate key without overwriting one another launch already
 * published: the candidate is written to a private temporary file and linked
 * into place, which fails when the destination exists. Returns null when another
 * writer owns the key path.
 */
function publishKey(keyPath: string, io: BrowserRuntimeKeyFs): Buffer | null {
	const candidate = randomBytes(KEY_BYTES).toString("base64url");
	const temporary = `${keyPath}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`;
	io.writeFileSync(temporary, `${candidate}\n`, { mode: 0o600 });
	try {
		io.linkSync(temporary, keyPath);
	} catch (error) {
		io.rmSync(temporary, { force: true });
		if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
		return readKey(keyPath, io);
	}
	io.rmSync(temporary, { force: true });
	io.chmodSync(keyPath, 0o600);
	return Buffer.from(candidate, "base64url");
}

/**
 * Move an unusable key aside so the lock holder can publish a working one. The
 * caller holds the lock and has just re-read the key, so anything valid at this
 * point arrived after that read and belongs to another launch — re-reading here
 * keeps a valid winner from being displaced by a stale decision.
 */
function displaceUnusableKey(keyPath: string, io: BrowserRuntimeKeyFs): void {
	if (readKey(keyPath, io)) return;
	const displaced = `${keyPath}.unusable-${randomBytes(6).toString("hex")}`;
	try {
		io.renameSync(keyPath, displaced);
	} catch {
		// Already gone, or another writer replaced it; either way there is
		// nothing to move aside.
		return;
	}
	io.rmSync(displaced, { force: true });
}

/**
 * Run fn while holding this data dir's key lock, or return null when the lock
 * stays contended. The lock is a linked file, so acquiring it is atomic and
 * releasing it cannot outlive the process by more than one stale sweep.
 */
function withKeyLock<T>(keyPath: string, io: BrowserRuntimeKeyFs, fn: () => T): T | null {
	const lockPath = `${keyPath}.lock`;
	const candidate = `${lockPath}.candidate-${process.pid}-${randomBytes(6).toString("hex")}`;
	io.writeFileSync(candidate, `${Date.now()}\n`, { mode: 0o600 });
	const deadline = Date.now() + LOCK_TIMEOUT_MS;
	try {
		for (;;) {
			try {
				io.linkSync(candidate, lockPath);
				break;
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
			}
			if (lockHeldForMs(lockPath, io) > LOCK_STALE_MS) {
				io.rmSync(lockPath, { force: true });
				continue;
			}
			if (Date.now() >= deadline) return null;
			sleepMs(LOCK_POLL_MS);
		}
		try {
			return fn();
		} finally {
			io.rmSync(lockPath, { force: true });
		}
	} finally {
		io.rmSync(candidate, { force: true });
	}
}

function lockHeldForMs(lockPath: string, io: BrowserRuntimeKeyFs): number {
	const heldSince = Number.parseInt(safeRead(lockPath, io) ?? "", 10);
	if (!Number.isFinite(heldSince)) return 0;
	return Date.now() - heldSince;
}

function readKey(keyPath: string, io: BrowserRuntimeKeyFs): Buffer | null {
	const trimmed = (safeRead(keyPath, io) ?? "").trim();
	if (!KEY_PATTERN.test(trimmed)) return null;
	const decoded = Buffer.from(trimmed, "base64url");
	return decoded.length === KEY_BYTES ? decoded : null;
}

function safeRead(target: PathLike, io: BrowserRuntimeKeyFs): string | null {
	try {
		return io.readFileSync(target, "utf8");
	} catch {
		return null;
	}
}

function sleepMs(ms: number): void {
	Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}