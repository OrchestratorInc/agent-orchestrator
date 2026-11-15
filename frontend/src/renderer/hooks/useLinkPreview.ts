import { apiErrorException } from "../lib/api-error-exception";
import { QueryObserver } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { queryClient } from "../lib/query-client";

export type LinkPreview = components["schemas"]["LinkPreviewResponse"];

export const LINK_PREVIEW_SETTLE_MS = 3_000;
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

export function useLinkPreview(url: string, enabled: boolean) {
	const [snapshot, setSnapshot] = useState<LinkPreviewSnapshot>(() => cachedSnapshot(url));

	useEffect(() => {
		if (!enabled || !url) {
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

export function useCachedLinkFavicons(url: string): LinkPreview | "failed" | undefined {
	const origin = useMemo(() => linkOrigin(url), [url]);
	const read = useCallback(() => {
		const preview = previewWithFavicon(url) ?? (origin ? previewWithFavicon(origin) : undefined);
		if (preview) return preview;
		return origin && queryClient.getQueryState(linkPreviewQueryKey(origin))?.status === "error" ? "failed" : undefined;
	}, [origin, url]);
	return useSyncExternalStore(subscribeLinkPreviews, read);
}
