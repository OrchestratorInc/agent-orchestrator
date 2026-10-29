import { useEffect, useState } from "react";
import { artifactFileUrl, getArtifactPage, getArtifactText } from "./api";
import { inlineArtifactPage, MAX_INLINED_IMAGE_BYTES, type ArtifactAssetLoader } from "./artifactPageInliner";
import { authHeaders, type ServerConfig } from "./config";

// A thread or gallery can show the same page again; a page is read once per version.
const CACHE_SIZE = 12;
const cache = new Map<string, Promise<string>>();

function cached(key: string, build: () => Promise<string>): Promise<string> {
	const hit = cache.get(key);
	if (hit) return hit;
	const pending = build();
	cache.set(key, pending);
	// A failure is not kept: the next mount tries again.
	pending.catch(() => cache.delete(key));
	while (cache.size > CACHE_SIZE) cache.delete(cache.keys().next().value!);
	return pending;
}

function assetLoader(config: ServerConfig, sessionId: string): ArtifactAssetLoader {
	return {
		text: (path) => getArtifactText(config, sessionId, path).catch(() => undefined),
		dataUri: async (path) => {
			const url = artifactFileUrl(config, sessionId, path);
			if (!url) return undefined;
			try {
				const res = await fetch(url, { headers: authHeaders(config) });
				if (!res.ok) return undefined;
				const declared = Number(res.headers.get("content-length"));
				// Too large to inline: say so without reading it into memory.
				if (declared > MAX_INLINED_IMAGE_BYTES) return { uri: "", bytes: declared };
				const blob = await res.blob();
				if (blob.size > MAX_INLINED_IMAGE_BYTES) return { uri: "", bytes: blob.size };
				const uri = await new Promise<string>((resolve, reject) => {
					const reader = new FileReader();
					reader.onload = () => typeof reader.result === "string" ? resolve(reader.result) : reject(new Error("unreadable"));
					reader.onerror = () => reject(reader.error ?? new Error("unreadable"));
					reader.readAsDataURL(blob);
				});
				return { uri, bytes: blob.size };
			} catch {
				return undefined;
			}
		},
	};
}

/**
 * The HTML artifact page at `path` with the files it links inlined (see
 * artifactPageInliner for why). `version` (the file's modified time) re-reads
 * a rewritten page. `failed` asks the caller to fall back to the page's URL.
 */
export function useInlinedArtifactPage({ enabled, config, sessionId, path, version }: {
	enabled: boolean;
	config: ServerConfig | null | undefined;
	sessionId: string;
	path: string;
	version?: string;
}): { html?: string; failed: boolean } {
	const key = `${config?.host ?? ""}:${config?.httpPort ?? ""}|${sessionId}|${path}|${version ?? ""}`;
	const [state, setState] = useState<{ key: string; html?: string; failed: boolean }>({ key: "", failed: false });
	const active = enabled && Boolean(config);
	useEffect(() => {
		if (!active || !config) return;
		let live = true;
		cached(key, async () => {
			// The page as the desktop frames it, with AO's theme written in.
			const html = await getArtifactPage(config, sessionId, path);
			return (await inlineArtifactPage(html, path, assetLoader(config, sessionId), sessionId)).html;
		})
			.then((html) => { if (live) setState({ key, html, failed: false }); })
			.catch(() => { if (live) setState({ key, failed: true }); });
		return () => { live = false; };
	}, [active, config, key, path, sessionId]);
	if (!active || state.key !== key) return { failed: false };
	return { html: state.html, failed: state.failed };
}
