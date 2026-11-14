import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { cloudCpFetch } from "../lib/cloud-cp/renderer-client";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";

export function CloudArtifactFrame({ title, url, onAccessRefused, onAccessGranted, onNavigate, onThumbnailState, thumbnail, zoom = 1 }: {
	title: string;
	url: string;
	onAccessRefused?: () => void;
	onAccessGranted?: () => void;
	onNavigate?: (path: string) => void;
	onThumbnailState?: (state: "loading" | "failed" | "ready") => void;
	thumbnail?: { width: number; height?: number; fallback: ReactNode };
	zoom?: number;
}) {
	const { t } = useTranslation();
	const [loadedUrl, setLoadedUrl] = useState<string>();
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
	useEffect(() => {
		if (status.data !== undefined && status.data >= 200 && status.data < 400) onAccessGranted?.();
	}, [status.data, status.dataUpdatedAt, onAccessGranted]);
	const thumbnailState = !thumbnail ? undefined : status.isPending ? "loading" : refused || status.isError || (status.data !== undefined && status.data >= 400) ? "failed" : loadedUrl === url ? "ready" : "loading";
	useEffect(() => {
		if (thumbnailState) onThumbnailState?.(thumbnailState);
	}, [thumbnailState, onThumbnailState]);
	if (thumbnail && (status.isPending || refused || status.isError || (status.data !== undefined && status.data >= 400))) return thumbnail.fallback;
	if (status.isPending || refused) return <PanelMessage>{t("files.loading")}</PanelMessage>;
	if (status.data === 404) return <PanelMessage>{t("artifacts.unavailable")}</PanelMessage>;
	if (status.isError || (status.data !== undefined && status.data >= 400)) {
		return <PanelMessage action={<RetryButton onClick={() => void status.refetch()} />}>{t("artifacts.accessError")}</PanelMessage>;
	}
	return (
		<iframe
			className={thumbnail ? "absolute left-0 top-0 origin-top-left border-0 bg-white" : "block h-full w-full border-0 bg-white"}
			style={thumbnail
				? { width: 1000, height: thumbnail.height ?? 625, transform: `scale(${thumbnail.width / 1000})` }
				: zoom !== 1 ? { width: `${100 / zoom}%`, height: `${100 / zoom}%`, transform: `scale(${zoom})`, transformOrigin: "0 0" } : undefined}
			referrerPolicy="no-referrer"
			sandbox="allow-scripts allow-forms"
			src={url}
			title={title}
			onLoad={() => setLoadedUrl(url)}
		/>
	);
}

function artifactFilesPrefix(url: string): string | undefined {
	try {
		const parsed = new URL(url);
		const prefix = /^(.*\/api\/cloud\/v1\/artifact-files\/[^/]+\/)/.exec(parsed.pathname)?.[1];
		return prefix ? parsed.origin + prefix : undefined;
	} catch {
		return undefined;
	}
}

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
