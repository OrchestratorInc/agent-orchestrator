const BASE64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function base64Head(data: string, length: number): number[] {
	const bytes: number[] = [];
	let buffer = 0;
	let bits = 0;
	for (const char of data) {
		if (bytes.length >= length || char === "=") break;
		const value = BASE64.indexOf(char);
		if (value < 0) continue;
		buffer = (buffer << 6) | value;
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			bytes.push((buffer >> bits) & 0xff);
		}
	}
	return bytes;
}

/**
 * The image type a base64 payload's magic bytes declare, if recognisable.
 * Pickers report the source file's type, but the bytes they export can differ:
 * Android re-encodes to JPEG at `quality < 1`, iOS always returns JPEG base64.
 */
export function sniffImageMimeType(data: string): string | undefined {
	const b = base64Head(data, 16);
	const ascii = (from: number, to: number) => String.fromCharCode(...b.slice(from, to));
	if (b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return "image/jpeg";
	if (b[0] === 0x89 && ascii(1, 4) === "PNG") return "image/png";
	if (ascii(0, 4) === "GIF8") return "image/gif";
	if (ascii(0, 4) === "RIFF" && ascii(8, 12) === "WEBP") return "image/webp";
	if (ascii(0, 2) === "BM") return "image/bmp";
	if (ascii(4, 8) === "ftyp") {
		const brand = ascii(8, 12);
		if (brand === "avif" || brand === "avis") return "image/avif";
		if (["heic", "heix", "hevc", "hevx", "heim", "heis"].includes(brand)) return "image/heic";
		if (brand === "mif1" || brand === "msf1") return "image/heif";
	}
	return undefined;
}
