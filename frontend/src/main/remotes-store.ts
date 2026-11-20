import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

// Saved desktop connections. Older records have no hostId and must be paired
// again before a saved password can be sent to their address.
export type RemoteEntry = {
	label: string;
	url: string;
	password: string;
	hostId?: string;
	accountUserId?: string;
	/** An encrypted password this launch could not decrypt, kept so a write does not lose it. */
	sealedPassword?: string;
	/** Set beside sealedPassword when that password is the account's token, not a pairing password. */
	accountToken?: true;
	/** The pairing password swapped for the token in `password`, used again if the host forgets the token. */
	fallbackPassword?: string;
	/** An encrypted fallbackPassword this launch could not decrypt. */
	sealedFallbackPassword?: string;
};

export const DEVICE_TOKEN_PREFIX = "aod_";

export function holdsPairingPassword(entry: Pick<RemoteEntry, "password">): boolean {
	return entry.password !== "" && !entry.password.startsWith(DEVICE_TOKEN_PREFIX) && !ACCOUNT_TOKEN.test(entry.password);
}

/** An AO account's token for a host: 32 random bytes in hex. A pairing password never has this shape. */
export const ACCOUNT_TOKEN = /^[0-9a-f]{64}$/;

/** Whether the saved credential is the account's token; read from the marker when the password cannot be decrypted. */
export function holdsAccountToken(entry: Pick<RemoteEntry, "password" | "accountToken">): boolean {
	return entry.password ? ACCOUNT_TOKEN.test(entry.password) : entry.accountToken === true;
}

export type PasswordCipher = { encrypt(plain: string): string; decrypt(sealed: string): string };

// Resolved on every read and write: the OS keychain may only be usable once the app is ready.
let currentCipher: () => PasswordCipher | null = () => null;

/** Saved passwords are encrypted with the cipher this returns; null keeps them in plaintext. */
export function setPasswordCipher(resolve: () => PasswordCipher | null): void {
	currentCipher = resolve;
}

type StoredRemote = Omit<RemoteEntry, "password"> & { password?: string };

function unsealFallback(plain: string | undefined, sealed: string | undefined, cipher: PasswordCipher | null): Pick<RemoteEntry, "fallbackPassword" | "sealedFallbackPassword"> {
	if (sealed === undefined) return plain ? { fallbackPassword: plain } : {};
	if (cipher) {
		try {
			return { fallbackPassword: cipher.decrypt(sealed) };
		} catch {
			// Encrypted by another keychain; kept below.
		}
	}
	return { sealedFallbackPassword: sealed };
}

function sealFallback(plain: string | undefined, sealed: string | undefined, cipher: PasswordCipher | null): Pick<RemoteEntry, "fallbackPassword" | "sealedFallbackPassword"> {
	if (plain) return cipher ? { sealedFallbackPassword: cipher.encrypt(plain) } : { fallbackPassword: plain };
	return sealed ? { sealedFallbackPassword: sealed } : {};
}

function unseal({ password, sealedPassword, accountToken, fallbackPassword, sealedFallbackPassword, ...stored }: StoredRemote, cipher: PasswordCipher | null): RemoteEntry {
	const rest = { ...stored, ...unsealFallback(fallbackPassword, sealedFallbackPassword, cipher) };
	if (sealedPassword === undefined) return { ...rest, password: password ?? "" };
	if (cipher) {
		try {
			return { ...rest, password: cipher.decrypt(sealedPassword) };
		} catch {
			// Encrypted by another keychain; keep it and let the host ask for a new pairing.
		}
	}
	return { ...rest, password: "", sealedPassword, ...(accountToken ? { accountToken } : {}) };
}

function seal({ password, sealedPassword, accountToken, fallbackPassword, sealedFallbackPassword, ...entry }: RemoteEntry, cipher: PasswordCipher | null): StoredRemote {
	const rest = { ...entry, ...sealFallback(fallbackPassword, sealedFallbackPassword, cipher) };
	if (cipher && password) return { ...rest, sealedPassword: cipher.encrypt(password), ...(ACCOUNT_TOKEN.test(password) ? { accountToken: true as const } : {}) };
	if (!password && sealedPassword) return { ...rest, sealedPassword, ...(accountToken ? { accountToken } : {}) };
	return { ...rest, password };
}

export class RemotesFilePermissionError extends Error {
	constructor(
		readonly path: string,
		readonly mode: number,
	) {
		super(
			`${path} holds connection passwords and is readable by others (mode ${mode.toString(8).padStart(4, "0")}) — run: chmod 600 ${path}`,
		);
	}
}

function isMissing(error: unknown): boolean {
	return (error as NodeJS.ErrnoException | undefined)?.code === "ENOENT";
}

export async function readRemotes(path: string): Promise<RemoteEntry[]> {
	const cipher = currentCipher();
	return (await readStoredRemotes(path)).map((stored) => unseal(stored, cipher));
}

async function readStoredRemotes(path: string): Promise<StoredRemote[]> {
	let mode: number;
	try {
		mode = (await stat(path)).mode & 0o777;
	} catch (error) {
		if (isMissing(error)) return [];
		throw error;
	}
	// Mirrors the CLI: a world-readable credential file is refused, not tolerated.
	// Windows is exempt for the same reason it is in the CLI (cli/remote.go:154):
	// Node reports 0o666 for every writable file there, so the check would refuse
	// every remotes.json on that platform and take saved hosts down with it.
	if (process.platform !== "win32" && mode & 0o077) throw new RemotesFilePermissionError(path, mode);

	const parsed = JSON.parse(await readFile(path, "utf8")) as { remotes?: StoredRemote[] };
	return parsed.remotes ?? [];
}

/** Rewrites plaintext passwords encrypted, when a cipher is available. */
export async function sealSavedPasswords(path: string): Promise<void> {
	return serializeMutation(async () => {
		if (!currentCipher() || !(await readStoredRemotes(path)).some((stored) => stored.password || stored.fallbackPassword)) return;
		await writeRemotes(path, await readRemotes(path));
	});
}

// Write beside the existing file so interruption never truncates the only
// saved copy. The temporary file is 0600 before its contents are written.
async function writeRemotes(path: string, remotes: RemoteEntry[]): Promise<void> {
	await mkdir(dirname(path), { recursive: true, mode: 0o700 });
	const temporary = `${path}.${randomUUID()}.tmp`;
	let renamed = false;
	try {
		const cipher = currentCipher();
		const stored = remotes.map((entry) => seal(entry, cipher));
		await writeFile(temporary, `${JSON.stringify({ remotes: stored }, null, 2)}\n`, {
			encoding: "utf8",
			mode: 0o600,
			flag: "wx",
		});
		await rename(temporary, path);
		renamed = true;
	} finally {
		if (!renamed) await rm(temporary, { force: true });
	}
}

// A concurrent add/update/remove must not overwrite another call's read-modify-write.
let mutationQueue: Promise<void> = Promise.resolve();

function serializeMutation<T>(operation: () => Promise<T>): Promise<T> {
	const queued = mutationQueue.then(operation, operation);
	mutationQueue = queued.then(() => undefined, () => undefined);
	return queued;
}

export async function addRemote(path: string, entry: RemoteEntry): Promise<void> {
	return serializeMutation(async () => {
		const existing = await readRemotes(path);
		await writeRemotes(path, [
			...existing.filter((candidate) => candidate.url !== entry.url && (!entry.hostId || candidate.hostId !== entry.hostId)),
			entry,
		]);
	});
}

/** An edit: only the fields it carries change. */
export type RemoteChanges = Partial<RemoteEntry>;

/**
 * Absent fields keep their saved value. Written with an explicit `??` per field
 * rather than a spread because Electron's structured clone preserves a key whose
 * value is undefined — `{ password: undefined }` must not wipe a working
 * password, which is exactly what "leave blank to keep it" sends.
 */
export function applyRemoteChanges(entry: RemoteEntry, changes: RemoteChanges): RemoteEntry {
	return {
		label: changes.label ?? entry.label,
		url: changes.url ?? entry.url,
		password: changes.password ?? entry.password,
		hostId: entry.hostId,
		accountUserId: changes.accountUserId ?? entry.accountUserId,
		sealedPassword: changes.password === undefined ? entry.sealedPassword : undefined,
		accountToken: changes.password === undefined ? entry.accountToken : undefined,
		fallbackPassword: changes.password === undefined ? entry.fallbackPassword : changes.fallbackPassword,
		sealedFallbackPassword: changes.password === undefined ? entry.sealedFallbackPassword : undefined,
	};
}

export async function updateRemote(path: string, url: string, changes: RemoteChanges): Promise<RemoteEntry> {
	return serializeMutation(async () => {
		const existing = await readRemotes(path);
		const current = existing.find((candidate) => candidate.url === url);
		if (!current) throw new Error(`no saved host for ${url}`);
		const updated = applyRemoteChanges(current, changes);
		// Re-pointing a host MOVES its entry: the row keeps its place, and any other
		// row already sitting on the new url is absorbed rather than left as a twin.
		const remotes = existing
			.map((candidate) => (candidate === current ? updated : candidate))
			.filter((candidate) => candidate === updated
				|| (candidate.url !== updated.url && (!updated.hostId || candidate.hostId !== updated.hostId)));
		await writeRemotes(path, remotes);
		return updated;
	});
}

export async function removeRemote(path: string, url: string): Promise<void> {
	return serializeMutation(async () => {
		const existing = await readRemotes(path);
		const remaining = existing.filter((candidate) => candidate.url !== url);
		// Removing what is not there is not an error, but it is not a write either.
		if (remaining.length === existing.length) return;
		await writeRemotes(path, remaining);
	});
}
