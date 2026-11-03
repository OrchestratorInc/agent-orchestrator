import aoLogo from "../../assets/ao-qr-logo.png";

/**
 * The logo drawn in the middle of a QR code, decoded once for the whole app:
 * Ruto from the mobile app's icon, without the tile, so it sits clean on the
 * code's white paper and reads as "scan me with that app".
 *
 * It starts loading when the QR module is first imported, long before a code
 * is drawn, so the first code a user sees already has its logo. A canvas can
 * only draw an image that has finished loading; until then a code is drawn
 * with the middle left clear, and drawn again the moment the logo arrives.
 */
let image: HTMLImageElement | null = null;
let ready = false;
const waiting = new Set<() => void>();

/** Start loading the logo. Safe to call any number of times. */
export function preloadQrLogo(): void {
	if (image || typeof Image === "undefined") return;
	const next = new Image();
	next.onload = () => {
		ready = true;
		for (const callback of waiting) callback();
		waiting.clear();
	};
	next.src = aoLogo;
	image = next;
}

/** The decoded logo, or null while it is still loading. */
export function qrLogo(): HTMLImageElement | null {
	preloadQrLogo();
	return ready ? image : null;
}

/** Call `callback` once the logo has loaded (at once if it already has). Returns a canceller. */
export function whenQrLogoReady(callback: () => void): () => void {
	if (ready) {
		callback();
		return () => undefined;
	}
	waiting.add(callback);
	return () => {
		waiting.delete(callback);
	};
}
