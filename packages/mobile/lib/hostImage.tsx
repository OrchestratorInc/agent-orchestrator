import { useEffect, useState } from "react";
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

/** The source to render: unchanged, or the image read through the encrypted channel (undefined while it loads). */
export function useHostImage<T extends Source>(source: T | undefined): T | { uri: string } | undefined {
	const via = (source as { via?: Via } | undefined)?.via;
	const uri = source?.uri ?? "";
	const [shown, setShown] = useState<{ uri: string; data: string }>();
	useEffect(() => {
		if (!via || !uri) return;
		let live = true;
		void load(via, uri).then((data) => {
			if (live) setShown({ uri, data });
		});
		return () => {
			live = false;
		};
	}, [via, uri]);
	if (!via) return source;
	return shown?.uri === uri ? { uri: shown.data } : undefined;
}

/** Image for a source from hostImageSource; draws nothing while an encrypted read is in flight. */
export function HostImage({ source, ...props }: Omit<ImageProps, "source"> & { source: Source }) {
	const shown = useHostImage(source);
	return shown ? <Image {...props} source={imageSourceForPlatform(shown)} /> : null;
}
