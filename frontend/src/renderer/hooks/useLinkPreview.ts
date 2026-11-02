import { useEffect, useState } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { queryClient } from "../lib/query-client";

export type LinkPreview = components["schemas"]["LinkPreviewResponse"];

const NEGATIVE_TTL_MS = 2 * 60_000;
const MAX_NEGATIVE_ENTRIES = 256;
const negativePreviews = new Map<string, number>();

function hasRecentUpstreamFailure(url: string): boolean {
	const until = negativePreviews.get(url);
	if (until === undefined) return false;
	if (until > Date.now()) return true;
	negativePreviews.delete(url);
	return false;
}

function rememberUpstreamFailure(url: string): void {
	negativePreviews.delete(url);
	negativePreviews.set(url, Date.now() + NEGATIVE_TTL_MS);
	if (negativePreviews.size > MAX_NEGATIVE_ENTRIES) {
		negativePreviews.delete(negativePreviews.keys().next().value!);
	}
}

export function linkPreviewQueryKey(url: string) {
	return ["link-preview", url] as const;
}

async function fetchLinkPreview(url: string): Promise<LinkPreview> {
	const { data, error, response } = await apiClient.GET("/api/v1/link-preview", {
		params: { query: { url } },
	});
	if (error) {
		if (response?.status === 502) rememberUpstreamFailure(url);
		throw new Error(apiErrorMessage(error, "Could not load link preview"));
	}
	negativePreviews.delete(url);
	return data;
}

type LinkPreviewResult = {
	url: string;
	data?: LinkPreview;
	isError: boolean;
};

/**
 * Fetches Open Graph metadata for an external URL through the daemon (the
 * renderer CSP blocks direct external fetches). Lazy: pass `enabled` only when
 * the preview is actually being shown.
 *
 * Uses the shared queryClient imperatively rather than useQuery so AppLink —
 * which renders in many provider-less contexts — stays safe everywhere. The
 * Successful results are cached per URL. A 502 is suppressed briefly across
 * hovers; other errors can be retried on the next hover.
 *
 * Results are keyed by URL: callers like XtermTerminal reuse one hook instance
 * while the hovered URL changes, so state from a previous URL must never be
 * returned for the current one.
 */
export function useLinkPreview(url: string, enabled: boolean) {
	const [result, setResult] = useState<LinkPreviewResult>(() => ({
		url,
		data: queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url)),
		isError: false,
	}));

	useEffect(() => {
		if (!enabled) {
			// A failure only suppresses the preview for the current hover. Clearing it
			// on disable lets the next hover retry — transient daemon/upstream blips
			// must not kill previews for this component's lifetime. Successes are
			// untouched: they keep serving from the queryClient cache below.
			if (result.url === url && result.isError) setResult({ url, isError: false });
			return;
		}
		if (hasRecentUpstreamFailure(url)) {
			if (result.url !== url || !result.isError) setResult({ url, isError: true });
			return;
		}
		if (result.url === url && result.data !== undefined) return;
		if (result.url === url && result.isError) return;
		if (queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url)) !== undefined) {
			setResult({ url, data: queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url)), isError: false });
			return;
		}
		let cancelled = false;
		queryClient
			.fetchQuery({
				queryKey: linkPreviewQueryKey(url),
				queryFn: () => fetchLinkPreview(url),
				staleTime: Number.POSITIVE_INFINITY,
				retry: false,
			})
			.then((preview) => {
				if (cancelled) return;
				setResult({ url, data: preview, isError: false });
			})
			.catch(() => {
				if (cancelled) return;
				setResult({ url, isError: true });
			});
		return () => {
			cancelled = true;
		};
	}, [enabled, url, result]);

	const current: LinkPreviewResult =
		result.url === url
			? result
			: { url, data: queryClient.getQueryData<LinkPreview>(linkPreviewQueryKey(url)), isError: false };
	return {
		data: current.data,
		isPending: enabled && current.data === undefined && !current.isError,
		isError: current.isError,
	};
}
