import { chacha20poly1305 } from "@noble/ciphers/chacha.js";
import { ed25519, x25519 } from "@noble/curves/ed25519.js";
import { hkdf } from "@noble/hashes/hkdf.js";
import { sha256 } from "@noble/hashes/sha2.js";

// The phone's half of the handshake in backend/internal/httpd/lan_e2ee.go.

const VERSION = 1;
const ascii = (text: string) => Uint8Array.from(text, (c) => c.charCodeAt(0));
const LABEL = ascii("ao-e2ee-v1\n");
const KEYS_INFO = ascii("ao-e2ee-v1 keys");
export const MAX_PLAIN = 16 * 1024;

export class E2EEError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "E2EEError";
	}
}

/** One direction's records: each seal or open uses the next nonce. */
export type RecordCipher = { seal(plain: Uint8Array): Uint8Array; open(record: Uint8Array): Uint8Array };

function recordCipher(key: Uint8Array): RecordCipher {
	let count = 0;
	const nonce = () => {
		const bytes = new Uint8Array(12);
		const view = new DataView(bytes.buffer);
		view.setUint32(4, Math.floor(count / 2 ** 32));
		view.setUint32(8, count >>> 0);
		count++;
		return bytes;
	};
	return {
		seal: (plain) => chacha20poly1305(key, nonce()).encrypt(plain),
		open: (record) => chacha20poly1305(key, nonce()).decrypt(record),
	};
}

function concat(...parts: Uint8Array[]): Uint8Array {
	const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
	let at = 0;
	for (const part of parts) {
		out.set(part, at);
		at += part.length;
	}
	return out;
}

/** Starts a handshake from 32 random bytes: `hello` goes to the host, its reply goes to `finish`. */
export function startHandshake(secret: Uint8Array) {
	const publicKey = x25519.getPublicKey(secret);
	return {
		hello: concat(Uint8Array.of(VERSION), publicKey),
		/** Checks the reply was signed by `hostKey` and returns the record ciphers. */
		finish(reply: Uint8Array, hostKey: Uint8Array): { toHost: RecordCipher; toPhone: RecordCipher } {
			if (reply.length !== 97 || reply[0] !== VERSION) throw new E2EEError("The machine sent an unexpected handshake.");
			const hostEphemeral = reply.subarray(1, 33);
			const transcript = concat(LABEL, publicKey, hostEphemeral);
			if (!ed25519.verify(reply.subarray(33), transcript, hostKey, { zip215: false })) {
				throw new E2EEError("This machine's key did not sign the connection.");
			}
			let shared: Uint8Array;
			try {
				shared = x25519.getSharedSecret(secret, hostEphemeral);
			} catch {
				throw new E2EEError("The machine sent an unusable key.");
			}
			if (shared.every((b) => b === 0)) throw new E2EEError("The machine sent an unusable key.");
			const keys = hkdf(sha256, shared, sha256(transcript), KEYS_INFO, 64);
			return { toHost: recordCipher(keys.subarray(0, 32)), toPhone: recordCipher(keys.subarray(32)) };
		},
	};
}

/** Decodes a host key as AO Cloud and the pairing code carry it: unpadded base64url. */
export function decodeHostKey(value: string): Uint8Array | null {
	if (!/^[A-Za-z0-9_-]{43}$/.test(value)) return null;
	const binary = atob(value.replace(/-/g, "+").replace(/_/g, "/") + "=");
	const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
	return bytes.length === 32 ? bytes : null;
}
