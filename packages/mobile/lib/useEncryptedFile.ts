import { Directory, File, Paths } from "expo-file-system";
import { useEffect, useRef, useState } from "react";
import { authHeaders, type ServerConfig } from "./config";
import { hostFetch, hostPath, type HostResponse } from "./e2ee/host";

// Larger files are not read into memory.
const MAX_ENCRYPTED_FILE_BYTES = 25 * 1024 * 1024;

// A WebView loads the file from here: iOS loads a hostless URI with
// loadFileURL, which takes only file: URLs, and Android refuses a data: URL
// over 2 MB.
const folder = () => new Directory(Paths.cache, "encrypted-files");
let cleared = false;

// A cached page requests nothing: a request to the machine would leave the
// encrypted channel with its path in the clear, and WebView offers no per-request block.
const NO_REQUESTS = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; media-src data:; font-src data:; style-src 'unsafe-inline'">`;

/** The bytes to cache and their extension: HTML gets the policy, and SVG is shown inside a page that has it. */
export function cachedDocument(bytes: Uint8Array, path: string, contentType: string): { bytes: Uint8Array; extension: string } {
	if (/^text\/html\b/i.test(contentType)) {
		return { bytes: new TextEncoder().encode(NO_REQUESTS + new TextDecoder().decode(bytes)), extension: "html" };
	}
	if (/^image\/svg\+xml\b/i.test(contentType) || /\.svg$/i.test(path)) {
		const page = `<!doctype html>${NO_REQUESTS}<style>body{margin:0}svg{max-width:100%;height:auto}</style><body>${new TextDecoder().decode(bytes)}</body>`;
		return { bytes: new TextEncoder().encode(page), extension: "html" };
	}
	return { bytes, extension: /\.([a-z0-9]{1,8})$/i.exec(path)?.[1] ?? "bin" };
}

function writeFile(bytes: Uint8Array, path: string, contentType: string): File {
	const dir = folder();
	// Files left by an earlier run, as after a crash, go before the first new one.
	if (!cleared) {
		cleared = true;
		if (dir.exists) dir.delete();
	}
	dir.create({ intermediates: true, idempotent: true });
	// The WebView takes the type from the extension, and a large Markdown file arrives rendered as HTML.
	const cached = cachedDocument(bytes, path, contentType);
	const file = new File(dir, `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}.${cached.extension}`);
	file.write(cached.bytes);
	return file;
}

/**
 * Whether a WebView showing a cached file may load `url`: only from the file's
 * folder. A link to the machine would leave the encrypted channel, and one
 * handed to the system browser would carry its path in the clear.
 */
export function staysInCache(url: string, readAccess: string): boolean {
	return url.startsWith(readAccess) || url === "about:blank";
}

/** The body, read until it passes `max` bytes; a body without a length can be any size. */
async function readUpTo(res: HostResponse, max: number): Promise<Uint8Array> {
	const reader = res.body?.getReader();
	if (!reader) return new Uint8Array(0);
	const pieces: Uint8Array[] = [];
	let total = 0;
	for (;;) {
		const { done, value } = await reader.read();
		if (done || !value) break;
		total += value.length;
		if (total > max) {
			await reader.cancel();
			throw new Error("too large");
		}
		pieces.push(value);
	}
	const bytes = new Uint8Array(total);
	let at = 0;
	for (const piece of pieces) {
		bytes.set(piece, at);
		at += piece.length;
	}
	return bytes;
}

/** A file read through the encrypted channel into the app's cache, for a machine with a pinned key. Deleted on unmount. */
export function useEncryptedFile(config: ServerConfig | null, url: string, version: string): { uri?: string; readAccess?: string; failed: boolean } {
	const key = config ? `${config.hostKey}|${url}|${version}` : "";
	const [state, setState] = useState<{ key: string; uri?: string; readAccess?: string; failed: boolean }>({ key: "", failed: false });
	const configRef = useRef(config);
	configRef.current = config;
	useEffect(() => {
		const current = configRef.current;
		if (!key || !current) return;
		let live = true;
		let written: File | undefined;
		// Leaving the file, or a new version of it, stops the download.
		const controller = new AbortController();
		void (async () => {
			const path = hostPath(url);
			const res = await hostFetch(current, path, { headers: authHeaders(current), signal: controller.signal });
			if (!res.ok || Number(res.headers.get("content-length")) > MAX_ENCRYPTED_FILE_BYTES) {
				void res.body?.cancel();
				throw new Error(String(res.status));
			}
			const bytes = await readUpTo(res, MAX_ENCRYPTED_FILE_BYTES);
			if (!live) return;
			written = writeFile(bytes, path, res.headers.get("content-type") ?? "");
			setState({ key, uri: written.uri, readAccess: folder().uri, failed: false });
		})().catch(() => { if (live) setState({ key, failed: true }); });
		return () => {
			live = false;
			controller.abort();
			try {
				written?.delete();
			} catch {
				// Already gone; the folder is cleared on the next run either way.
			}
		};
	}, [key, url]);
	return state.key === key ? state : { failed: false };
}
