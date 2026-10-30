import { useQuery } from "@tanstack/react-query";
import { File, FileText, Globe, Image, Table2, Workflow } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { cloudArtifactFileUrl, useCloudArtifactAccess } from "../hooks/useCloudArtifacts";
import { CLOUD_TEXT_ARTIFACT, CloudArtifactAccessError, sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { artifactContentUrl } from "../lib/artifact-content-url";
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

const PAGE_WIDTH = 1000;
const PAGE_HEIGHT = 625;
const MARKDOWN_PAGE_WIDTH = 700;

/** Gallery previews load near the viewport; pages keep the isolated inline origin. */
export function ArtifactThumbnail({ row, previewLive = true }: { row: ArtifactRow; previewLive?: boolean }) {
	const { t } = useTranslation();
	const container = useRef<HTMLSpanElement>(null);
	const [visible, setVisible] = useState(false);
	const [width, setWidth] = useState(0);
	const [failed, setFailed] = useState(false);
	const [pageAvailable, setPageAvailable] = useState<boolean>();
	const cloud = Boolean(row.cloud);
	const imagePath = isRasterImagePath(row.path) || isSvgPath(row.path);
	const plainText = /\.(?:txt|json|jsonl|yaml|yml|toml|ini|conf|log|xml|css|js|jsx|ts|tsx|sql|sh|py|go|rs|diff|patch)$/i.test(row.path);
	const table = isDelimitedTextPath(row.path);
	const mermaid = isMermaidPath(row.path);
	const isMarkdown = row.kind === "markdown" || isMarkdownPreviewPath(row.path);
	const textPreview = isMarkdown || plainText || table || mermaid;
	const cloudPreviewable = imagePath || (row.kind === "html" ? previewLive : textPreview && CLOUD_TEXT_ARTIFACT.test(row.path));
	const access = useCloudArtifactAccess({ id: row.sessionId, cloud: row.cloud }, cloud && visible && cloudPreviewable);
	const rawUrl = cloud ? access.data ? cloudArtifactFileUrl(access.data, row.path, row.updatedAt) : undefined : row.rawUrl;
	const contentUrl = artifactContentUrl(row.sessionId, row.path, rawUrl);
	const page = previewLive && row.kind === "html"
		? cloud ? contentUrl : row.inlineUrl && isInlineArtifactUrl(row.inlineUrl) ? row.inlineUrl : undefined
		: undefined;
	const image = imagePath ? contentUrl : undefined;
	const pdf = previewLive && !cloud && /\.pdf$/i.test(row.path) ? contentUrl : undefined;
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
	useEffect(() => setFailed(false), [contentUrl]);
	useLayoutEffect(() => {
		const element = container.current;
		if (!element) return;
		const bounds = element.getBoundingClientRect();
		if (bounds.width > 0 && bounds.bottom >= -200 && bounds.top <= window.innerHeight + 200) {
			setVisible(true);
			return;
		}
		const observer = new IntersectionObserver(([entry]) => {
			if (!entry?.isIntersecting) return;
			setVisible(true);
			observer.disconnect();
		}, { rootMargin: "200px" });
		observer.observe(element);
		return () => observer.disconnect();
	}, []);
	useLayoutEffect(() => {
		setPageAvailable(undefined);
		if (cloud || !visible || !page || !contentUrl) return;
		const controller = new AbortController();
		// A cross-origin iframe fires load even for a 404 JSON response. Check
		// the readable artifact route before framing it, without retaining HTML.
		void fetch(contentUrl, { signal: controller.signal, cache: "no-store" })
			.then(async (response) => {
				await response.body?.cancel();
				if (!controller.signal.aborted) setPageAvailable(response.ok);
			})
			.catch(() => {
				if (!controller.signal.aborted) setPageAvailable(false);
			});
		return () => controller.abort();
	}, [cloud, visible, page, contentUrl, row.updatedAt]);
	useLayoutEffect(() => {
		const element = container.current;
		if (!element || (!page && !isMarkdown)) return;
		const initialWidth = element.getBoundingClientRect().width;
		if (initialWidth > 0) setWidth(initialWidth);
		const observer = new ResizeObserver(([entry]) => {
			if (entry && entry.contentRect.width > 0) setWidth(entry.contentRect.width);
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, [page, isMarkdown]);
	const content = file.data && !file.data.binary ? file.data.content : "";
	const preview = isMarkdown ? stripMarkdownFrontmatter(content) : content;
	const text = plainText && content.trim() ? content.slice(0, 4000) : "";
	const Icon = imagePath ? Image : row.kind === "html" ? Globe : table ? Table2 : mermaid ? Workflow : isMarkdown || plainText || pdf ? FileText : File;
	const accessPending = cloud && cloudPreviewable && !access.data && access.fetchStatus === "fetching";
	const imagePending = checkImage && imageStatus.isPending;
	const unavailable = failed || access.isError || (readText && file.isError) || (checkImage && (imageStatus.isError || (imageStatus.data !== undefined && imageStatus.data >= 400)))
		|| (cloud && !cloudPreviewable) || pageAvailable === false || (page && !contentUrl) || (textPreview && !contentUrl)
		|| (!page && !image && !pdf && !textPreview)
		|| (file.isSuccess && textPreview && !preview.trim());
	const placeholder = (
		<span className="absolute inset-0 grid place-items-center text-passive" title={t("artifactLink.noPreview")}>
			<Icon className="size-7" />
		</span>
	);
	return (
		<span inert ref={container} className={cn("pointer-events-none absolute inset-0 block overflow-hidden", isMarkdown || plainText ? "ao-document-surface" : "bg-[var(--solid-muted,var(--muted))]")} aria-hidden="true">
			{accessPending || imagePending || unavailable ? placeholder : !visible ? null : image ? (
				<img src={image} alt="" loading="eager" onError={() => setFailed(true)} className="h-full w-full object-cover object-top" />
			) : page ? (
				cloud ? (
					<CloudArtifactFrame onAccessGranted={accessGranted} onAccessRefused={refreshAccess} thumbnail={{ width, fallback: placeholder }} title={row.name} url={page} />
				) : pageAvailable ? (
					<iframe
						src={page}
						title={row.name}
						sandbox="allow-scripts allow-same-origin"
						loading="eager"
						tabIndex={-1}
						className="absolute left-0 top-0 origin-top-left border-0 bg-[var(--solid-muted,var(--muted))]"
						style={{ width: PAGE_WIDTH, height: PAGE_HEIGHT, transform: `scale(${width / PAGE_WIDTH})` }}
					/>
				) : null
			) : pdf ? (
				<div className="flex h-full min-h-0 flex-col overflow-hidden">
					<PdfPreview firstPageOnly onError={() => setFailed(true)} path={row.path} src={`${pdf}${pdf.includes("?") ? "&" : "?"}v=${encodeURIComponent(row.updatedAt)}`} />
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
