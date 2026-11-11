import { useEffect, useRef, useState } from "react";
import { authHeaders, type ServerConfig } from "./config";
import { bytesToDataUri, hostFetch, hostPath } from "./e2ee/host";

// Larger files are not read into memory as a data URI.
const MAX_ENCRYPTED_FILE_BYTES = 25 * 1024 * 1024;

/** A file read through the encrypted channel as a data URI, for a machine with a pinned key. */
export function useEncryptedFile(config: ServerConfig | null, url: string, version: string): { uri?: string; failed: boolean } {
	const key = config ? `${config.hostKey}|${url}|${version}` : "";
	const [state, setState] = useState<{ key: string; uri?: string; failed: boolean }>({ key: "", failed: false });
	const configRef = useRef(config);
	configRef.current = config;
	useEffect(() => {
		const current = configRef.current;
		if (!key || !current) return;
		let live = true;
		void (async () => {
			const res = await hostFetch(current, hostPath(url), { headers: authHeaders(current) });
			if (!res.ok || Number(res.headers.get("content-length")) > MAX_ENCRYPTED_FILE_BYTES) {
				void res.body?.cancel();
				throw new Error(String(res.status));
			}
			const bytes = new Uint8Array(await res.arrayBuffer());
			if (bytes.length > MAX_ENCRYPTED_FILE_BYTES) throw new Error("too large");
			return bytesToDataUri(bytes, res.headers.get("content-type") ?? "");
		})().then((uri) => { if (live) setState({ key, uri, failed: false }); }, () => { if (live) setState({ key, failed: true }); });
		return () => { live = false; };
	}, [key, url]);
	return state.key === key ? state : { failed: false };
}
