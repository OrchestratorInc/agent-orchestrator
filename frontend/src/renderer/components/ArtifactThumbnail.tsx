import { useQuery } from "@tanstack/react-query";
import { artifactFileIcon } from "./artifact-file-icon";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { cloudArtifactFileUrl, useCloudArtifactAccess, usePinnedArtifactUrl } from "../hooks/useCloudArtifacts";
import { CLOUD_TEXT_ARTIFACT, CloudArtifactAccessError, sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { artifactContentUrl } from "../lib/artifact-content-url";
import { fileMediaKind, mediaCrossOrigin } from "../lib/file-media";
import type { ArtifactRow } from "../lib/artifacts";
import { isMermaidPath, isRasterImagePath, isSvgPath } from "../lib/image-files";
import { isDelimitedTextPath } from "../lib/delimited-text";
import { isMarkdownPreviewPath, stripMarkdownFrontmatter } from "../lib/markdown-frontmatter";
import { MermaidBlock } from "./chat/MermaidBlock";
import { cn } from "../lib/utils";
import { MarkdownFileView } from "./markdown/MarkdownFileView";
import { PdfPreview } from "./PdfPreview";
import { TableFileView } from "./TableFileView";
import { CloudArtifactFrame } from "./CloudArtifactFrame";
import { cloudCpFetch } from "../lib/cloud-cp/renderer-client";
import { useDownscaledImage } from "../lib/downscaled-image";

const PAGE_WIDTH = 1000;
const PAGE_HEIGHT = 625;
const MARKDOWN_PAGE_WIDTH = 700;

const PLAIN_TEXT = /\.(?:txt|json|jsonl|yaml|yml|toml|ini|conf|log|xml|css|js|jsx|ts|tsx|sql|sh|py|go|rs|diff|patch)$/i;

/** Gallery previews load near the viewport; pages keep the isolated inline origin. */
export function ArtifactThumbnail({ row, previewLive = true, pageHeight = PAGE_HEIGHT }: { row: ArtifactRow; previewLive?: boolean; /** Layout height of an HTML page, for tiles taller than 16:10. */ pageHeight?: number }) {
	const { t } = useTranslation();
	const container = useRef<HTMLSpanElement>(null);
	const [visible, setVisible] = useState(false);
	const [width, setWidth] = useState(0);
	const [failed, setFailed] = useState(false);
	// A Cloud page's frame, by the URL it reported for: a frame for another URL is still loading.
	const [frame, setFrame] = useState<{ url?: string; state: "loading" | "failed" | "ready" }>({ state: "loading" });
	const cloud = Boolean(row.cloud);
	const imagePath = isRasterImagePath(row.path) || isSvgPath(row.path);
	const plainText = PLAIN_TEXT.test(row.path);
	const table = isDelimitedTextPath(row.path);
	const mermaid = isMermaidPath(row.path);
	const isMarkdown = row.kind === "markdown" || isMarkdownPreviewPath(row.path);
	const textPreview = isMarkdown || plainText || table || mermaid;
	const pdfPath = /\.pdf$/i.test(row.path);
	const cloudPreviewable = imagePath || (row.kind === "html" || pdfPath ? previewLive : textPreview && CLOUD_TEXT_ARTIFACT.test(row.path));
	const access = useCloudArtifactAccess({ id: row.sessionId, cloud: row.cloud }, cloud && visible && cloudPreviewable);
	const rawUrl = cloud ? access.data ? cloudArtifactFileUrl(access.data, row.path, row.updatedAt) : undefined : row.rawUrl;
	const contentUrl = artifactContentUrl(row.sessionId, row.path, rawUrl);
	const page = previewLive && row.kind === "html"
		? cloud ? contentUrl : row.inlineUrl && isInlineArtifactUrl(row.inlineUrl) ? row.inlineUrl : undefined
		: undefined;
	const image = imagePath ? contentUrl : undefined;
	// A local photo or screenshot shows a small copy: the original is decoded once, off the main
	// thread, rather than in full by every card each time it scrolls into view. An SVG stays
	// vector, and a Cloud image is shown as it is served.
	const smallImage = useDownscaledImage(visible && !cloud && image && isRasterImagePath(row.path) ? image : undefined, row.updatedAt);
	const downscaling = visible && !cloud && Boolean(image) && isRasterImagePath(row.path) && smallImage === undefined;
	const imageSrc = smallImage && smallImage !== "failed" ? smallImage : image;
	// A cross-origin iframe fires load even for a 404 JSON response, so the readable artifact
	// route is checked before the page is framed, without keeping the HTML. The answer is kept
	// for this version of the file: a card scrolling back into view does not ask again. Only a
	// file that is not there is kept as unavailable; any other failure may pass, so it is an
	// error, which the next card to mount asks about again.
	const pageCheck = useQuery({
		queryKey: ["artifact-page-available", contentUrl ?? "", row.updatedAt],
		queryFn: async ({ signal }) => {
			const response = await fetch(contentUrl ?? "", { signal, cache: "no-store" });
			await response.body?.cancel();
			if (response.ok) return true;
			if (response.status === 404 || response.status === 410) return false;
			throw new Error(`artifact page check failed: ${response.status}`);
		},
		enabled: !cloud && visible && Boolean(page) && Boolean(contentUrl),
		staleTime: Infinity,
		gcTime: 10 * 60_000,
		retry: false,
	});
	const pageAvailable = !cloud && page && contentUrl ? (pageCheck.isError ? false : pageCheck.data) : undefined;
	const onFrameState = useCallback((state: "loading" | "failed" | "ready") => setFrame({ url: page, state }), [page]);
	const frameState = cloud && page ? (frame.url === page ? frame.state : "loading") : undefined;
	// A local video shows the frame Chromium paints after a metadata preload,
	// which reads only the ranges it needs rather than the whole file.
	const video = !cloud && fileMediaKind(row.path) === "video" ? contentUrl : undefined;
	// A cloud PDF card keeps the link it loaded with, so an access renewal does
	// not download and parse the whole file again.
	const pinnedPdf = usePinnedArtifactUrl(cloud && pdfPath ? access.data : undefined, cloud && pdfPath ? row.path : undefined, row.updatedAt);
	const pdf = previewLive && pdfPath ? cloud ? pinnedPdf.url : contentUrl : undefined;
	const readText = visible && Boolean(contentUrl) && textPreview && (!cloud || (cloudPreviewable && !imagePath));
	const fileOptions = sessionArtifactFileQueryOptions(row.sessionId, row.path, rawUrl, undefined, cloud);
	const file = useQuery({
		...fileOptions,
		queryKey: [...fileOptions.queryKey, row.updatedAt],
		enabled: readText,
		staleTime: Infinity,
		gcTime: 10 * 60_000,
		retry: false,
	});
	const checkImage = cloud && visible && imagePath && Boolean(rawUrl);
	const imageStatus = useQuery({
		queryKey: ["cloud-artifact-status", rawUrl ?? ""],
		queryFn: async () => (await cloudCpFetch(rawUrl ?? "", { method: "HEAD" })).status,
		enabled: checkImage,
		staleTime: Infinity,
		retry: false,
	});
	const refusalKey = JSON.stringify([row.cloud?.orgId, row.sessionId, row.path, row.updatedAt]);
	const refusal = useRef({ key: refusalKey, refreshed: false });
	const refreshAccess = useCallback(() => {
		if (refusal.current.key !== refusalKey) refusal.current = { key: refusalKey, refreshed: false };
		if (refusal.current.refreshed) return;
		refusal.current.refreshed = true;
		void access.refetch({ cancelRefetch: false });
	}, [access.refetch, refusalKey]);
	const accessGranted = useCallback(() => {
		refusal.current = { key: refusalKey, refreshed: false };
	}, [refusalKey]);
	useEffect(() => {
		if (!readText) return;
		if (file.error instanceof CloudArtifactAccessError) refreshAccess();
		else if (file.isSuccess) accessGranted();
	}, [readText, file.error, file.isSuccess, file.dataUpdatedAt, refreshAccess, accessGranted]);
	useEffect(() => {
		if (!checkImage) return;
		if (imageStatus.data === 401 || imageStatus.data === 403) refreshAccess();
		else if (imageStatus.data !== undefined && imageStatus.data >= 200 && imageStatus.data < 400) accessGranted();
	}, [checkImage, imageStatus.data, imageStatus.dataUpdatedAt, refreshAccess, accessGranted]);
	useEffect(() => setFailed(false), [contentUrl, pdf]);
	useLayoutEffect(() => {
		const element = container.current;
		if (!element) return;
		// The observer reports a card already in view before it is painted. Measuring here instead
		// would force a layout of the whole page for every card mounted, as each one scrolls in.
		const observer = new IntersectionObserver(([entry]) => {
			if (!entry?.isIntersecting) return;
			setVisible(true);
			observer.disconnect();
		}, { rootMargin: "200px" });
		observer.observe(element);
		return () => observer.disconnect();
	}, []);
	useLayoutEffect(() => {
		const element = container.current;
		if (!element || (!page && !isMarkdown)) return;
		// The first observation brings the width, before the card is painted, without forcing a layout.
		const observer = new ResizeObserver(([entry]) => {
			if (entry && entry.contentRect.width > 0) setWidth(entry.contentRect.width);
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, [page, isMarkdown]);
	const content = file.data && !file.data.binary ? file.data.content : "";
	const preview = isMarkdown ? stripMarkdownFrontmatter(content) : content;
	const text = plainText && content.trim() ? content.slice(0, 4000) : "";
	const Icon = artifactFileIcon(row);
	const accessPending = cloud && cloudPreviewable && !access.data && access.fetchStatus === "fetching";
	const imagePending = checkImage && imageStatus.isPending;
	const unavailable = failed || access.isError || (readText && file.isError) || (checkImage && (imageStatus.isError || (imageStatus.data !== undefined && imageStatus.data >= 400)))
		|| (cloud && !cloudPreviewable) || pageAvailable === false || (page && !contentUrl) || (textPreview && !contentUrl)
		|| (!page && !image && !video && !pdf && !textPreview)
		|| (file.isSuccess && textPreview && !preview.trim());
	// A preview that failed to load for a reason that may pass (the network, an expired access link, a read error).
	const loadFailed = failed || access.isError || (!cloud && Boolean(page) && pageCheck.isError) || (readText && file.isError) || (checkImage && (imageStatus.isError || (imageStatus.data !== undefined && imageStatus.data >= 400))) || frameState === "failed";
	// What a snapshot of this card may wait for: the preview drawn, nothing worth drawing for good, or a failure to try again later.
	const previewState = accessPending || imagePending || downscaling || !visible || (readText && file.isPending) || (page && !cloud && pageAvailable === undefined) || frameState === "loading"
		? "loading"
		: loadFailed ? "error" : unavailable ? "unavailable" : "content";
	const placeholder = (
		<span className="absolute inset-0 grid place-items-center text-passive" title={t("artifactLink.noPreview")}>
			<Icon className="size-7" />
		</span>
	);
	return (
		<span inert ref={container} data-preview-state={previewState} className={cn("pointer-events-none absolute inset-0 block overflow-hidden", isMarkdown || plainText ? "ao-document-surface" : "bg-[var(--solid-muted,var(--muted))]")} aria-hidden="true">
			{accessPending || imagePending || unavailable ? placeholder : !visible || downscaling ? null : image ? (
				<img src={imageSrc} alt="" decoding="async" loading="lazy" onError={() => setFailed(true)} className="h-full w-full object-cover object-top" />
			) : video ? (
				<video crossOrigin={mediaCrossOrigin(video)} muted onError={() => setFailed(true)} playsInline preload="metadata" src={video} tabIndex={-1} className="h-full w-full object-cover object-top" />
			) : page ? (
				cloud ? (
					<CloudArtifactFrame onAccessGranted={accessGranted} onAccessRefused={refreshAccess} onThumbnailState={onFrameState} thumbnail={{ width, height: pageHeight, fallback: placeholder }} title={row.name} url={page} />
				) : pageAvailable ? (
					<iframe
						src={page}
						title={row.name}
						sandbox="allow-scripts allow-same-origin"
						loading="eager"
						tabIndex={-1}
						className="absolute left-0 top-0 origin-top-left border-0 bg-[var(--solid-muted,var(--muted))]"
						style={{ width: PAGE_WIDTH, height: pageHeight, transform: `scale(${width / PAGE_WIDTH})` }}
					/>
				) : null
			) : pdf ? (
				<div className="flex h-full min-h-0 flex-col overflow-hidden">
					{/* A cloud file's URL already carries its version. */}
					<PdfPreview cloud={cloud} firstPageOnly onError={() => {
						setFailed(true);
						if (!cloud) return;
						pinnedPdf.repin();
						refreshAccess();
					}} path={row.path} src={cloud ? pdf : `${pdf}${pdf.includes("?") ? "&" : "?"}v=${encodeURIComponent(row.updatedAt)}`} />
				</div>
			) : table ? (
				<TableFileView content={content.slice(0, 4000)} path={row.path} />
			) : mermaid ? (
				<div className="h-full overflow-hidden p-3"><MermaidBlock code={content} /></div>
			) : plainText ? (
				<pre className="m-0 h-full overflow-hidden whitespace-pre-wrap break-words p-4 font-mono text-caption leading-relaxed text-foreground [mask-image:linear-gradient(to_bottom,black_75%,transparent)]">{text}</pre>
			) : (
				<span
					className="absolute left-0 top-0 block origin-top-left"
					style={{ width: MARKDOWN_PAGE_WIDTH, height: PAGE_HEIGHT, transform: `scale(${width / MARKDOWN_PAGE_WIDTH})` }}
				>
					<MarkdownFileView
						artifactOrigin={cloud ? access.data?.replace(/\/+$/, "") : contentUrl ? new URL(rawUrl ?? contentUrl).origin : undefined}
						content={content}
						filePath={row.path}
						presentation="thumbnail"
						sessionId={row.sessionId}
						truncated={file.data?.contentTruncated ?? false}
						version={file.dataUpdatedAt}
					/>
				</span>
			)}
		</span>
	);
}
