import { Directory, File, Paths } from "expo-file-system";
import { useEffect, useRef, useState } from "react";
import { authHeaders, type ServerConfig } from "./config";
import { hostFetch, hostPath } from "./e2ee/host";

// Larger files are not read into memory.
const MAX_ENCRYPTED_FILE_BYTES = 25 * 1024 * 1024;

// A WebView loads the file from here: iOS loads a hostless URI with
// loadFileURL, which takes only file: URLs, and Android refuses a data: URL
// over 2 MB.
const folder = () => new Directory(Paths.cache, "encrypted-files");
let cleared = false;

function writeFile(bytes: Uint8Array, path: string): File {
	const dir = folder();
	// Files left by an earlier run, as after a crash, go before the first new one.
	if (!cleared) {
		cleared = true;
		if (dir.exists) dir.delete();
	}
	dir.create({ intermediates: true, idempotent: true });
	const extension = /\.([a-z0-9]{1,8})$/i.exec(path)?.[1] ?? "bin";
	const file = new File(dir, `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}.${extension}`);
	file.write(bytes);
	return file;
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
		void (async () => {
			const path = hostPath(url);
			const res = await hostFetch(current, path, { headers: authHeaders(current) });
			if (!res.ok || Number(res.headers.get("content-length")) > MAX_ENCRYPTED_FILE_BYTES) {
				void res.body?.cancel();
				throw new Error(String(res.status));
			}
			const bytes = new Uint8Array(await res.arrayBuffer());
			if (bytes.length > MAX_ENCRYPTED_FILE_BYTES) throw new Error("too large");
			if (!live) return;
			written = writeFile(bytes, path);
			setState({ key, uri: written.uri, readAccess: folder().uri, failed: false });
		})().catch(() => { if (live) setState({ key, failed: true }); });
		return () => {
			live = false;
			try {
				written?.delete();
			} catch {
				// Already gone; the folder is cleared on the next run either way.
			}
		};
	}, [key, url]);
	return state.key === key ? state : { failed: false };
}
