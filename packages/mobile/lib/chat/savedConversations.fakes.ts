import { createCipheriv, createDecipheriv, randomBytes } from "node:crypto";

// In-memory stand-ins for the native modules savedConversations reaches, for
// tests under node: the cache directory (expo-file-system), AES-GCM
// (expo-crypto, done for real with node:crypto, additional data included) and
// the secure store. A test file mocks each module with these:
//   vi.mock("expo-file-system", () => import("./savedConversations.fakes").then((fakes) => fakes.fileSystemModule));

/** Every file written, by path ("cache/saved-chats/<name>"). */
export const disk = new Map<string, { bytes: Uint8Array; modified: number }>();
/** The secure store's values, by key. */
export const secrets = new Map<string, string>();
/** Secure store keys whose value does not read back, as one restored from another phone's backup without its Keystore key. */
export const unreadableSecrets = new Set<string>();
/** Paths whose delete throws, as a file the OS will not let go of. */
export const undeletable = new Set<string>();
/** Paths whose next write is cut short: part of it lands, then it throws, as the app killed mid-write. */
export const interruptedWrites = new Set<string>();
let lastModified = 0;

export function resetSavedChatFakes() {
	disk.clear();
	secrets.clear();
	unreadableSecrets.clear();
	undeletable.clear();
	interruptedWrites.clear();
	lastModified = 0;
}

/** The names of the files in the saved-chats directory, sorted. */
export const savedFileNames = () => [...disk.keys()].filter((path) => path.startsWith("cache/saved-chats/")).map((path) => path.slice("cache/saved-chats/".length)).sort();

type Part = string | { uri: string };
const joined = (parts: Part[]) => parts.map((part) => (typeof part === "string" ? part : part.uri)).join("/");

class Directory {
	uri: string;
	constructor(...parts: Part[]) {
		this.uri = joined(parts);
	}
	private children() {
		return [...disk.keys()].filter((path) => path.startsWith(`${this.uri}/`)).map((path) => path.slice(this.uri.length + 1));
	}
	get exists() {
		return this.children().length > 0;
	}
	create() {}
	delete() {
		for (const name of this.children()) disk.delete(`${this.uri}/${name}`);
	}
	list() {
		return this.children().filter((name) => !name.includes("/")).map((name) => new File(this, name));
	}
}

class File {
	uri: string;
	name: string;
	constructor(directory: { uri: string }, name: string) {
		this.uri = `${directory.uri}/${name}`;
		this.name = name;
	}
	get exists() {
		return disk.has(this.uri);
	}
	write(content: string | Uint8Array) {
		// Distinct, increasing times even within one millisecond, so the newest is known.
		lastModified = Math.max(Date.now(), lastModified + 1);
		const bytes = typeof content === "string" ? new TextEncoder().encode(content) : new Uint8Array(content);
		if (interruptedWrites.delete(this.uri)) {
			// As the native write: the file is truncated first, then only part of it is written.
			disk.set(this.uri, { bytes: bytes.slice(0, Math.floor(bytes.byteLength / 2)), modified: lastModified });
			throw new Error(`write to ${this.uri} cut short`);
		}
		disk.set(this.uri, { bytes, modified: lastModified });
	}
	async bytes() {
		const file = disk.get(this.uri);
		if (!file) throw new Error(`no file at ${this.uri}`);
		return file.bytes;
	}
	info() {
		const file = disk.get(this.uri);
		return { exists: Boolean(file), size: file?.bytes.byteLength, modificationTime: file?.modified };
	}
	delete() {
		if (undeletable.has(this.uri)) throw new Error(`cannot delete ${this.uri}`);
		if (!disk.delete(this.uri)) throw new Error(`no file at ${this.uri}`);
	}
	/** As expo-file-system: an existing destination fails the move, unless `overwrite` deletes it just before the rename. */
	moveSync(destination: File, options: { overwrite?: boolean } = {}) {
		const file = disk.get(this.uri);
		if (!file) throw new Error(`no file at ${this.uri}`);
		if (disk.has(destination.uri)) {
			if (!options.overwrite) throw new Error(`${destination.uri} exists`);
			disk.delete(destination.uri);
		}
		disk.delete(this.uri);
		disk.set(destination.uri, file);
		this.uri = destination.uri;
		this.name = destination.name;
	}
}

export const fileSystemModule = { Directory, File, Paths: { cache: new Directory("cache") } };

class AESEncryptionKey {
	constructor(readonly raw: Buffer) {}
	static async generate() {
		return new AESEncryptionKey(randomBytes(32));
	}
	static async import(text: string, _encoding: "base64") {
		return new AESEncryptionKey(Buffer.from(text, "base64"));
	}
	async encoded(_encoding: "base64") {
		return this.raw.toString("base64");
	}
}

class AESSealedData {
	constructor(readonly bytes: Uint8Array) {}
	static fromCombined(bytes: Uint8Array) {
		return new AESSealedData(bytes);
	}
	async combined() {
		return this.bytes;
	}
}

type AesOptions = { additionalData?: Uint8Array };

async function aesEncryptAsync(plain: Uint8Array, key: AESEncryptionKey, options: AesOptions = {}) {
	const iv = randomBytes(12);
	const cipher = createCipheriv("aes-256-gcm", key.raw, iv);
	if (options.additionalData) cipher.setAAD(options.additionalData);
	const body = Buffer.concat([cipher.update(plain), cipher.final()]);
	return new AESSealedData(new Uint8Array(Buffer.concat([iv, body, cipher.getAuthTag()])));
}

async function aesDecryptAsync(sealed: AESSealedData, key: AESEncryptionKey, options: AesOptions = {}) {
	const combined = Buffer.from(sealed.bytes);
	if (combined.length < 28) throw new Error("too short to be sealed data");
	const decipher = createDecipheriv("aes-256-gcm", key.raw, combined.subarray(0, 12));
	if (options.additionalData) decipher.setAAD(options.additionalData);
	decipher.setAuthTag(combined.subarray(combined.length - 16));
	return new Uint8Array(Buffer.concat([decipher.update(combined.subarray(12, combined.length - 16)), decipher.final()]));
}

export const cryptoModule = { AESEncryptionKey, AESSealedData, aesEncryptAsync, aesDecryptAsync };

export const secureStoreModule = {
	WHEN_UNLOCKED_THIS_DEVICE_ONLY: 5,
	getItemAsync: async (key: string) => {
		if (unreadableSecrets.has(key)) throw new Error(`could not decrypt the value for ${key}`);
		return secrets.get(key) ?? null;
	},
	setItemAsync: async (key: string, value: string) => {
		unreadableSecrets.delete(key);
		secrets.set(key, value);
	},
	deleteItemAsync: async (key: string) => {
		unreadableSecrets.delete(key);
		secrets.delete(key);
	},
};
