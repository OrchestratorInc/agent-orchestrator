import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { cloudCpFetch } from "../lib/cloud-cp/renderer-client";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";

/**
 * A cloud session's HTML artifact, framed from the control plane. The frame
 * is sandboxed without allow-same-origin or popups, so the page runs in an
 * opaque origin and cannot open a window; the control plane's own
 * Content-Security-Policy sandboxes it too and blocks its network requests.
 * A file whose stored object is gone shows as unavailable; a refused link
 * calls onAccessRefused so the caller can take a fresh one. When the page
 * opens another file of its session, onNavigate gets that file's path, and a
 * link refused on the way (the frame shows "Link expired") calls
 * onAccessRefused.
 */
export function CloudArtifactFrame({ title, url, onAccessRefused, onNavigate }: {
	title: string;
	url: string;
	onAccessRefused?: () => void;
	onNavigate?: (path: string) => void;
}) {
	const { t } = useTranslation();
	useEffect(() => {
		const prefix = artifactFilesPrefix(url);
		if (!prefix) return;
		return aoBridge.cloudArtifact.onFrameNavigated((navigation) => {
			const path = artifactPathUnder(prefix, navigation.url);
			if (path === undefined) return;
			if (navigation.status === 401 || navigation.status === 403) onAccessRefused?.();
			if (path) onNavigate?.(path);
		});
	}, [url, onAccessRefused, onNavigate]);
	const status = useQuery({
		queryKey: ["cloud-artifact-status", url],
		queryFn: async () => (await cloudCpFetch(url, { method: "HEAD" })).status,
		retry: false,
	});
	const refused = status.data === 401 || status.data === 403;
	useEffect(() => {
		if (refused) onAccessRefused?.();
	}, [refused, onAccessRefused]);
	if (status.isPending || refused) return <PanelMessage>{t("files.loading")}</PanelMessage>;
	if (status.data === 404) return <PanelMessage>{t("artifacts.unavailable")}</PanelMessage>;
	if (status.isError || (status.data !== undefined && status.data >= 400)) {
		return <PanelMessage action={<RetryButton onClick={() => void status.refetch()} />}>{t("artifacts.accessError")}</PanelMessage>;
	}
	return (
		<iframe
			className="block h-full w-full border-0 bg-white"
			referrerPolicy="no-referrer"
			sandbox="allow-scripts allow-forms"
			src={url}
			title={title}
		/>
	);
}

/** The origin and access prefix of a cloud artifact file URL. */
function artifactFilesPrefix(url: string): string | undefined {
	try {
		const parsed = new URL(url);
		const prefix = /^(.*\/api\/cloud\/v1\/artifact-files\/[^/]+\/)/.exec(parsed.pathname)?.[1];
		return prefix ? parsed.origin + prefix : undefined;
	} catch {
		return undefined;
	}
}

/** The artifact path of url under prefix, or undefined for a URL elsewhere. */
export function artifactPathUnder(prefix: string, url: string): string | undefined {
	try {
		const parsed = new URL(url);
		const full = parsed.origin + parsed.pathname;
		if (!full.startsWith(prefix)) return undefined;
		return full.slice(prefix.length).split("/").map(decodeURIComponent).join("/");
	} catch {
		return undefined;
	}
}
