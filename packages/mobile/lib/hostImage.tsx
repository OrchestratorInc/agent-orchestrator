import { useEffect, useRef, useState } from "react";
import { Image, type ImageProps } from "react-native";
import type { ServerConfig } from "./config";
import { bytesToDataUri, hostFetch, hostPath } from "./e2ee/host";
import { imageSourceForPlatform } from "./imageSource";

type Source = { uri: string; headers?: Record<string, string> };

const loaded = new Map<string, Promise<string>>();
const MAX_LOADED = 64;

// An image the machine did not send: Image reports it through onError.
const UNLOADABLE = "data:,";

type Via = { config: ServerConfig; headers: Record<string, string> };

function load({ config: cfg, headers }: Via, uri: string): Promise<string> {
	const key = `${cfg.hostKey}\n${uri}`;
	let pending = loaded.get(key);
	if (!pending) {
		pending = (async () => {
			try {
				const res = await hostFetch(cfg, hostPath(uri), { headers });
				if (!res.ok) throw new Error(String(res.status));
				return bytesToDataUri(new Uint8Array(await res.arrayBuffer()), res.headers.get("content-type") ?? "");
			} catch {
				loaded.delete(key);
				return UNLOADABLE;
			}
		})();
		loaded.set(key, pending);
		if (loaded.size > MAX_LOADED) loaded.delete(loaded.keys().next().value as string);
	}
	return pending;
}

/**
 * The source to render: unchanged, or the image read through the encrypted
 * channel (undefined while it loads). A new `attempt` reads a failed image again.
 */
export function useHostImage<T extends Source>(source: T | undefined, attempt = 0): T | { uri: string } | undefined {
	const via = (source as { via?: Via } | undefined)?.via;
	const viaRef = useRef(via);
	viaRef.current = via;
	const uri = source?.uri ?? "";
	// Callers rebuild the source on every render, so the read is keyed on what it asks for.
	const key = via ? `${via.config.hostKey}\n${uri}` : "";
	const [shown, setShown] = useState<{ key: string; attempt: number; data: string }>();
	useEffect(() => {
		const current = viaRef.current;
		if (!key || !current) return;
		let live = true;
		void load(current, uri).then((data) => {
			if (live) setShown({ key, attempt, data });
		});
		return () => {
			live = false;
		};
	}, [key, uri, attempt]);
	if (!via) return source;
	return shown?.key === key && shown.attempt === attempt ? { uri: shown.data } : undefined;
}

/** Image for a source from hostImageSource; draws nothing while an encrypted read is in flight. */
export function HostImage({ source, ...props }: Omit<ImageProps, "source"> & { source: Source }) {
	const shown = useHostImage(source);
	return shown ? <Image {...props} source={imageSourceForPlatform(shown)} /> : null;
}
