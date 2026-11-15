import qrcode from "qrcode-generator";

export interface QrMatrix {
	readonly modules: number;
	readonly version: number;
	isDark(row: number, col: number): boolean;
}

export function modulesForVersion(version: number): number {
	return 4 * version + 17;
}

export function encodeQr(value: string, options: { version?: number; cache?: boolean } = {}): QrMatrix {
	const key = `${options.version ?? 0}|${value}`;
	if (options.cache) {
		const hit = cache.get(key);
		if (hit) {
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
		qr = build(0);
	}
	const modules = qr.getModuleCount();
	return { modules, version: (modules - 17) / 4, isDark: (row, col) => qr.isDark(row, col) };
}
