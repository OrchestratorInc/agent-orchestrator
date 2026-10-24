import type { BrowserLiveCommand, BrowserLiveState } from "../browserLive";
import type { PreviewFrame } from "../preview/source";

const record = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
export const validBrowserId = (value: unknown): value is string => typeof value === "string" && /^[A-Za-z0-9_-]{1,200}$/.test(value);

export function safeBrowserURL(value: unknown): string | null {
	if (typeof value !== "string" || value.length > 8192 || /[\u0000-\u0020\u007f]/.test(value)) return null;
	try {
		const url = new URL(value);
		return (url.protocol === "https:" || url.protocol === "http:") && url.hostname && !url.username && !url.password ? url.href : null;
	} catch { return null; }
}

export function cloudBrowserHello() {
	return { t: "hello", w: 1280, h: 800, dpr: 1, quality: 70, scrollQuality: 70, scrollScale: 1, maxInFlight: 4, embed: false };
}

export function initialCloudBrowserState(): BrowserLiveState {
	return { url: "", title: "", loading: false, canGoBack: false, canGoForward: false, activeTabId: "", tabs: [], width: 1280, height: 800 };
}

function jpegDimensions(jpeg: Uint8Array): { width: number; height: number } {
	if (jpeg.length < 4 || jpeg[0] !== 255 || jpeg[1] !== 216 || jpeg.at(-2) !== 255 || jpeg.at(-1) !== 217) throw new Error("Invalid browser JPEG");
	let at = 2;
	while (at + 3 < jpeg.length) {
		if (jpeg[at++] !== 255) throw new Error("Invalid browser JPEG marker");
		while (jpeg[at] === 255) at++;
		const marker = jpeg[at++];
		if (marker === 218 || marker === 217) break;
		if (marker === 1 || (marker >= 208 && marker <= 215)) continue;
		const length = jpeg[at] * 256 + jpeg[at + 1];
		if (length < 2 || at + length > jpeg.length) throw new Error("Truncated browser JPEG");
		if ([192, 193, 194, 195, 197, 198, 199, 201, 202, 203, 205, 206, 207].includes(marker)) {
			if (length < 8) throw new Error("Invalid browser JPEG dimensions");
			const height = jpeg[at + 3] * 256 + jpeg[at + 4];
			const width = jpeg[at + 5] * 256 + jpeg[at + 6];
			if (width < 1 || height < 1 || width > 16384 || height > 16384) throw new Error("Invalid browser JPEG dimensions");
			return { width, height };
		}
		at += length;
	}
	throw new Error("Browser JPEG dimensions are missing");
}

/** Cloud has a length-prefixed JSON envelope, unlike the daemon frame protocol. */
export function decodeCloudBrowserFrame(buffer: ArrayBuffer): PreviewFrame {
	if (buffer.byteLength < 9 || buffer.byteLength > 12 * 1024 * 1024) throw new Error("Invalid browser frame size");
	const length = new DataView(buffer).getUint32(0);
	if (length < 2 || length > 16384 || length > buffer.byteLength - 8) throw new Error("Invalid browser frame header");
	const headerBytes = new Uint8Array(buffer, 4, length);
	// Hermes versions without TextDecoder still support strict UTF-8 decoding
	// through decodeURIComponent; the header is capped above at 16 KiB.
	const json = typeof TextDecoder !== "undefined" ? new TextDecoder("utf-8", { fatal: true }).decode(headerBytes)
		: decodeURIComponent(Array.from(headerBytes, (byte) => `%${byte.toString(16).padStart(2, "0")}`).join(""));
	const header: unknown = JSON.parse(json);
	if (!record(header) || !Number.isSafeInteger(header.seq) || (header.seq as number) < 0 || !Number.isSafeInteger(header.ts) || (header.ts as number) < 0) throw new Error("Invalid browser frame sequence");
	const jpeg = buffer.slice(4 + length);
	const dimensions = jpegDimensions(new Uint8Array(jpeg));
	let crop: PreviewFrame["crop"];
	if (header.crop !== undefined) {
		if (!Array.isArray(header.crop) || header.crop.length !== 2) throw new Error("Invalid browser crop");
		const [width, height] = header.crop;
		if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0 || width > dimensions.width || height > dimensions.height) throw new Error("Invalid browser crop");
		crop = { width, height };
	}
	return { sequence: BigInt(header.seq as number), capturedAtMs: BigInt(header.ts as number), ...dimensions, jpeg, ...(crop ? { crop } : {}) };
}

export function projectCloudBrowserMessage(previous: BrowserLiveState, text: string): { state: BrowserLiveState; notice?: string } {
	if (text.length > 128 * 1024) throw new Error("Browser message is too large");
	const message: unknown = JSON.parse(text);
	if (!record(message)) throw new Error("Invalid browser message");
	let state = previous;
	let notice: string | undefined;
	switch (message.t) {
		case "tabs": {
			if (!Array.isArray(message.tabs) || message.tabs.length > 500) throw new Error("Invalid browser tabs");
			const tabs = message.tabs.filter((tab) => record(tab) && validBrowserId(tab.id)).map((tab) => ({ id: tab.id as string, title: typeof tab.title === "string" ? tab.title.slice(0, 1000) : "", url: safeBrowserURL(tab.url) ?? "", active: tab.id === previous.activeTabId }));
			const active = tabs.find((tab) => tab.active);
			state = { ...previous, tabs, ...(active ? { title: active.title, url: active.url } : {}) };
			break;
		}
		case "active": {
			if (message.id !== "" && !validBrowserId(message.id)) break;
			const tabs = previous.tabs.map((tab) => ({ ...tab, active: tab.id === message.id }));
			const tab = tabs.find((tab) => tab.active);
			state = { ...previous, tabs, activeTabId: message.id as string, title: tab?.title ?? "", url: tab?.url ?? "" };
			break;
		}
		case "url": state = { ...previous, url: safeBrowserURL(message.url) ?? "" }; break;
		case "loading": if (typeof message.on === "boolean") state = { ...previous, loading: message.on }; break;
		case "history": if (typeof message.back === "boolean" && typeof message.fwd === "boolean") state = { ...previous, canGoBack: message.back, canGoForward: message.fwd }; break;
		case "viewport": if (Number.isFinite(message.w) && Number.isFinite(message.h) && (message.w as number) >= 1 && (message.w as number) <= 16384 && (message.h as number) >= 1 && (message.h as number) <= 16384) state = { ...previous, width: message.w as number, height: message.h as number }; break;
		case "control": if (message.owner === "agent") notice = "The agent is using this browser. Your input shares the same page."; break;
		case "dialog": case "select": case "auth": case "filechooser": case "clipboard": case "download": notice = "This browser prompt needs the desktop viewer. Return there to continue."; break;
		case "ui": if (record(message.data) && ["clipboard", "contextmenu"].includes(String(message.data.kind))) notice = "This browser action needs the desktop viewer. Return there to continue."; break;
		case "crashed": notice = "The browser page crashed. Reload it or ask the agent to reopen it."; break;
	}
	return { state, ...(notice ? { notice } : {}) };
}

export function cloudBrowserCommand(command: BrowserLiveCommand, viewport: { width: number; height: number }, sequence: number): Record<string, unknown> | null {
	if (command.type === "navigate") {
		if (command.payload.action === "open") { const url = safeBrowserURL(command.payload.url); return url ? { t: "nav", url } : null; }
		return ["back", "forward", "reload", "stop"].includes(command.payload.action) ? { t: command.payload.action } : null;
	}
	if (command.type === "tab") {
		if (command.payload.action === "new") return { t: "newtab" };
		return validBrowserId(command.payload.tabId) && ["select", "close"].includes(command.payload.action) ? { t: command.payload.action === "select" ? "tab" : "closetab", id: command.payload.tabId } : null;
	}
	if (!Number.isFinite(viewport.width) || !Number.isFinite(viewport.height) || viewport.width <= 0 || viewport.height <= 0 || viewport.width > 16384 || viewport.height > 16384) return null;
	const input = command.payload;
	if (input.kind === "wheel") {
		if (typeof input.deltaX !== "number" || typeof input.deltaY !== "number" || !Number.isFinite(input.deltaX) || !Number.isFinite(input.deltaY)) return null;
		const clamp = (value: number) => Math.max(-10000, Math.min(10000, value));
		return { t: "mouse", type: "mouseWheel", seq: sequence, x: viewport.width / 2, y: viewport.height / 2, dx: clamp(-input.deltaX), dy: clamp(-input.deltaY), mods: 0 };
	}
	if (input.kind !== "pointer" || typeof input.x !== "number" || typeof input.y !== "number" || !Number.isFinite(input.x) || !Number.isFinite(input.y) || input.x < 0 || input.x > 1 || input.y < 0 || input.y > 1 || input.button !== "left" || !["down", "up", "move"].includes(String(input.phase))) return null;
	return { t: "mouse", type: input.phase === "down" ? "mousePressed" : input.phase === "up" ? "mouseReleased" : "mouseMoved", seq: sequence, x: input.x * viewport.width, y: input.y * viewport.height, button: "left", buttons: input.phase === "down" ? 1 : 0, clickCount: input.phase === "move" ? 0 : 1, mods: 0 };
}
