import { apiErrorException } from "../lib/api-error-exception";
import { QueryObserver } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { queryClient } from "../lib/query-client";

export type LinkPreview = components["schemas"]["LinkPreviewResponse"];

/** How long a hover waits on the skeleton before showing the compact fallback. */
export const LINK_PREVIEW_SETTLE_MS = 3_000;
// Matches the daemon's success TTL, so a warm link stays instant for as long as
// the daemon would serve it without going upstream.
const LINK_PREVIEW_CACHE_MS = 30 * 60_000;

export function linkPreviewQueryKey(url: string) {
	return ["link-preview", url] as const;
}

async function fetchLinkPreview(url: string, signal: AbortSignal): Promise<LinkPreview> {
	const { data, error } = await apiClient.GET("/api/v1/link-preview", {
		params: { query: { url } },
		signal,
	});
	if (error) throw apiErrorException(error, "Could not load link preview");
	return data;
}

function linkPreviewQueryOptions(url: string) {
	return {
		queryKey: linkPreviewQueryKey(url),
		queryFn: ({ signal }: { signal: AbortSignal }) => fetchLinkPreview(url, signal),
		staleTime: Number.POSITIVE_INFINITY,
		gcTime: LINK_PREVIEW_CACHE_MS,
		retry: false,
	};
}

/** Loads a preview into the cache a hover reads, and resolves with it. Never rejects. */
export async function preloadLinkPreview(url: string): Promise<LinkPreview | undefined> {
	await queryClient.prefetchQuery(linkPreviewQueryOptions(url));
	return queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url));
}

type LinkPreviewSnapshot = {
	url: string;
	data?: LinkPreview;
	isError: boolean;
	expired: boolean;
};

function cachedSnapshot(url: string): LinkPreviewSnapshot {
	return { url, data: queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url)), isError: false, expired: false };
}

/**
 * Fetches Open Graph metadata for an external URL through the daemon (the
 * renderer CSP blocks direct external fetches). Lazy: pass `enabled` only when
 * the preview is actually being shown.
 *
 * Observes the shared queryClient directly rather than through useQuery so
 * AppLink, which renders in many provider-less contexts, stays safe everywhere.
 *
 * Every hover settles. A failure settles at once; a response that is still
 * outstanding after LINK_PREVIEW_SETTLE_MS reports `isError` so the card shows
 * its fallback, and upgrades in place if the response then arrives.
 *
 * When the last hover on a URL ends its request is aborted, so links that were
 * only passed over do not hold the renderer's few daemon connections. The
 * daemon keeps fetching and caches the result, and owns the failure window, so
 * the next hover always asks it again.
 *
 * Results are keyed by URL: callers like XtermTerminal reuse one hook instance
 * while the hovered URL changes, so state from a previous URL must never be
 * returned for the current one.
 */
export function useLinkPreview(url: string, enabled: boolean) {
	const [snapshot, setSnapshot] = useState<LinkPreviewSnapshot>(() => cachedSnapshot(url));

	useEffect(() => {
		if (!enabled || !url) {
			// A failure only applies to the hover that saw it.
			setSnapshot((previous) => (previous.url === url && !previous.isError && !previous.expired ? previous : cachedSnapshot(url)));
			return;
		}
		const observer = new QueryObserver<LinkPreview>(queryClient, linkPreviewQueryOptions(url));
		let expired = false;
		const publish = () => {
			const result = observer.getCurrentResult();
			setSnapshot({ url, data: result.data, isError: result.isError, expired });
		};
		const unsubscribe = observer.subscribe(publish);
		publish();
		const settleTimer = window.setTimeout(() => {
			expired = true;
			publish();
		}, LINK_PREVIEW_SETTLE_MS);
		return () => {
			window.clearTimeout(settleTimer);
			unsubscribe();
		};
	}, [enabled, url]);

	const current = snapshot.url === url ? snapshot : cachedSnapshot(url);
	const settledEmpty = current.data === undefined && (current.isError || current.expired);
	return {
		data: current.data,
		isPending: enabled && current.data === undefined && !settledEmpty,
		isError: settledEmpty,
	};
}

/**
 * The HTTPS site root of a web link (`https://host/`), whose preview names the
 * site's icon. An http link is looked up over HTTPS too, unless it names its own
 * port, which says nothing about where HTTPS is served. Carries none of the
 * link's path, query or credentials.
 */
export function linkOrigin(url: string): string | undefined {
	try {
		const parsed = new URL(url);
		if (parsed.protocol === "https:") return `https://${parsed.host}/`;
		if (parsed.protocol === "http:" && !parsed.port) return `https://${parsed.hostname}/`;
		return undefined;
	} catch {
		return undefined;
	}
}

function subscribeLinkPreviews(onChange: () => void) {
	return queryClient.getQueryCache().subscribe((event) => {
		if (event.query.queryKey[0] === "link-preview") onChange();
	});
}

function previewWithFavicon(url: string): LinkPreview | undefined {
	const preview = queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url));
	return preview?.faviconUrl ? preview : undefined;
}

/**
 * What the cache knows of a link's site icons: the preview naming them (the
 * link's own, which a hover loaded, or its site root's), "failed" when asking
 * for the site root failed so no icon is coming, or undefined while unknown.
 * Reads the cache only, through one subscription, and updates in place.
 */
export function useCachedLinkFavicons(url: string): LinkPreview | "failed" | undefined {
	const origin = useMemo(() => linkOrigin(url), [url]);
	const read = useCallback(() => {
		const preview = previewWithFavicon(url) ?? (origin ? previewWithFavicon(origin) : undefined);
		if (preview) return preview;
		return origin && queryClient.getQueryState(linkPreviewQueryKey(origin))?.status === "error" ? "failed" : undefined;
	}, [origin, url]);
	return useSyncExternalStore(subscribeLinkPreviews, read);
}
