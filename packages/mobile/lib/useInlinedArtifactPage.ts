import { useEffect, useState } from "react";
import { artifactFileUrl, getArtifactPage, getArtifactText } from "./api";
import { FULL_PAGE_LIMITS, inlineArtifactPage, type ArtifactAssetLoader, type InlineLimits } from "./artifactPageInliner";
import { cachedArtifactPage } from "./artifactPageCache";
import { authHeaders, type ServerConfig } from "./config";

/** Reads the files a session's artifact pages link, with the Bearer header. */
export function artifactAssetLoader(config: ServerConfig, sessionId: string): ArtifactAssetLoader {
	return {
		text: (path) => getArtifactText(config, sessionId, path).catch(() => undefined),
		dataUri: async (path, maxBytes) => {
			const url = artifactFileUrl(config, sessionId, path);
			if (!url) return undefined;
			try {
				const res = await fetch(url, { headers: authHeaders(config) });
				if (!res.ok) return undefined;
				const declared = Number(res.headers.get("content-length"));
				// Too large to inline: say so without reading it into memory.
				if (declared > maxBytes) {
					void res.body?.cancel().catch(() => {});
					return { uri: "", bytes: declared };
				}
				const blob = await res.blob();
				if (blob.size > maxBytes) return { uri: "", bytes: blob.size };
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
 * a rewritten page; without one the page is read afresh on each mount.
 * `failed` asks the caller to fall back to the page's URL.
 */
export function useInlinedArtifactPage({ enabled, config, sessionId, path, version, limits = FULL_PAGE_LIMITS }: {
	enabled: boolean;
	config: ServerConfig | null | undefined;
	sessionId: string;
	path: string;
	version?: string;
	/** How much of the files it links the page takes in: a card's is small. */
	limits?: InlineLimits;
}): { html?: string; failed: boolean } {
	const page = `${config?.host ?? ""}:${config?.httpPort ?? ""}|${sessionId}|${path}|${limits.name}`;
	const key = `${page}|${version ?? ""}`;
	const [state, setState] = useState<{ key: string; html?: string; failed: boolean }>({ key: "", failed: false });
	const active = enabled && Boolean(config);
	useEffect(() => {
		if (!active || !config) return;
		let live = true;
		// A page with no known version is read on every mount (see cachedArtifactPage).
		cachedArtifactPage(page, version, async () => {
			// The page as the desktop frames it, with AO's theme written in.
			const html = await getArtifactPage(config, sessionId, path);
			return (await inlineArtifactPage(html, path, artifactAssetLoader(config, sessionId), sessionId, limits)).html;
		})
			.then((html) => { if (live) setState({ key, html, failed: false }); })
			.catch(() => { if (live) setState({ key, failed: true }); });
		return () => { live = false; };
	}, [active, config, key, limits, page, path, sessionId, version]);
	if (!active || state.key !== key) return { failed: false };
	return { html: state.html, failed: state.failed };
}
