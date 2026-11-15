import aoLogo from "../../assets/ao-qr-logo.png";

let image: HTMLImageElement | null = null;
let ready = false;
const waiting = new Set<() => void>();

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

export function qrLogo(): HTMLImageElement | null {
	preloadQrLogo();
	return ready ? image : null;
}

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
