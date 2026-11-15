import qrcode from "qrcode-generator";

/** A QR code as a module grid: the data, with no opinion on how it is drawn. */
export interface QrMatrix {
	/** Modules per side. */
	readonly modules: number;
	/** The QR version (1 to 40) the payload was encoded at. */
	readonly version: number;
	isDark(row: number, col: number): boolean;
}

/** Modules per side of a QR version: 21 at version 1, plus 4 per version. */
export function modulesForVersion(version: number): number {
	return 4 * version + 17;
}

/**
 * Encode `value` (byte mode) at error correction M.
 *
 * `version` pins the QR version, so codes of different payload lengths draw
 * at the same density. A payload too long for it still gets the smallest
 * version that holds it: a code that cannot carry its payload is worse than a
 * denser one. Encoding is plain JS over a few thousand modules, a few
 * milliseconds at most, so callers encode when the value changes. A pairing
 * code carries the connection password and is never cached; `cache` is for
 * codes that are public.
 */
export function encodeQr(value: string, options: { version?: number; cache?: boolean } = {}): QrMatrix {
	const key = `${options.version ?? 0}|${value}`;
	if (options.cache) {
		const hit = cache.get(key);
		if (hit) {
			// Most recently used last.
			cache.delete(key);
			cache.set(key, hit);
			return hit;
		}
	}
	const matrix = encode(value, options.version);
	if (options.cache) {
		cache.set(key, matrix);
		if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value as string);
	}
	return matrix;
}

/** Encoded matrices of codes that are not secret (the decoy, the store links), so
 *  reopening a page does not encode them again. Never used for a pairing code. */
const cache = new Map<string, QrMatrix>();
const CACHE_MAX = 6;

function encode(value: string, version?: number): QrMatrix {
	const build = (typeNumber: Parameters<typeof qrcode>[0]) => {
		const qr = qrcode(typeNumber, "M");
		qr.addData(value);
		qr.make();
		return qr;
	};
	const pinned = version as Parameters<typeof qrcode>[0] | undefined;
	let qr: ReturnType<typeof qrcode>;
	try {
		qr = build(pinned ?? 0);
	} catch (error) {
		if (pinned === undefined) throw error;
		// Payload too long for the pinned version: let the encoder pick.
		qr = build(0);
	}
	const modules = qr.getModuleCount();
	return { modules, version: (modules - 17) / 4, isDark: (row, col) => qr.isDark(row, col) };
}
