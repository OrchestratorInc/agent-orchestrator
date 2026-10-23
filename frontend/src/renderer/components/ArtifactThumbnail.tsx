import { useQuery } from "@tanstack/react-query";
import { File, FileText, Globe, Image } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { artifactContentUrl } from "../lib/artifact-content-url";
import type { ArtifactRow } from "../lib/artifacts";
import { isRasterImagePath, isSvgPath } from "../lib/image-files";
import { isMarkdownPreviewPath, stripMarkdownFrontmatter } from "../lib/markdown-frontmatter";
import { MarkdownFileView } from "./markdown/MarkdownFileView";

const PAGE_WIDTH = 1000;
const PAGE_HEIGHT = 625;

/** Gallery previews load near the viewport; pages keep the isolated inline origin. */
export function ArtifactThumbnail({ row }: { row: ArtifactRow }) {
	const { t } = useTranslation();
	const container = useRef<HTMLSpanElement>(null);
	const [visible, setVisible] = useState(false);
	const [width, setWidth] = useState(0);
	const [failed, setFailed] = useState(false);
	const page = row.kind === "html" && row.inlineUrl && isInlineArtifactUrl(row.inlineUrl) ? row.inlineUrl : undefined;
	const contentUrl = artifactContentUrl(row.sessionId, row.path, row.rawUrl);
	const image = isRasterImagePath(row.path) || isSvgPath(row.path)
		? contentUrl : undefined;
	const plainText = /\.txt$/i.test(row.path);
	const isMarkdown = row.kind === "markdown" || isMarkdownPreviewPath(row.path);
	const fileOptions = sessionArtifactFileQueryOptions(row.sessionId, row.path, row.rawUrl);
	const file = useQuery({
		...fileOptions,
		queryKey: [...fileOptions.queryKey, row.updatedAt],
		enabled: visible && Boolean(contentUrl) && (isMarkdown || plainText),
		staleTime: Infinity,
		gcTime: 10 * 60_000,
		retry: false,
	});
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
	const markdown = isMarkdown ? stripMarkdownFrontmatter(content).trim() : "";
	const text = plainText && content.trim() ? content.slice(0, 4000) : "";
	const Icon = image ? Image : row.kind === "html" ? Globe : isMarkdown || plainText ? FileText : File;
	const unavailable = failed || file.isError || ((isMarkdown || plainText) && !contentUrl)
		|| (!page && !image && !markdown && !text && !(isMarkdown || plainText))
		|| (file.isSuccess && !page && !image && !markdown && !text);
	return (
		<span ref={container} className="pointer-events-none absolute inset-0 block overflow-hidden bg-[var(--solid-muted,var(--muted))]" aria-hidden="true">
			{unavailable ? (
				<span className="absolute inset-0 grid place-items-center text-passive" title={t("artifactLink.noPreview")}>
					<Icon className="size-7" />
				</span>
			) : !visible ? null : image ? (
				<img src={image} alt="" loading="eager" onError={() => setFailed(true)} className="h-full w-full object-cover object-top" />
			) : page ? (
				<iframe
					src={page}
					title={row.name}
					sandbox="allow-scripts allow-same-origin"
					loading="eager"
					tabIndex={-1}
					onError={() => setFailed(true)}
					className="absolute left-0 top-0 origin-top-left border-0 bg-[var(--solid-muted,var(--muted))]"
					style={{ width: PAGE_WIDTH, height: PAGE_HEIGHT, transform: `scale(${width / PAGE_WIDTH})` }}
				/>
			) : plainText ? (
				<pre className="m-0 h-full overflow-hidden whitespace-pre-wrap break-words p-4 font-mono text-caption leading-relaxed text-foreground [mask-image:linear-gradient(to_bottom,black_75%,transparent)]">{text}</pre>
			) : (
				<span
					className="absolute left-0 top-0 block origin-top-left"
					style={{ width: PAGE_WIDTH, height: PAGE_HEIGHT, transform: `scale(${width / PAGE_WIDTH})` }}
				>
					<MarkdownFileView
						artifactOrigin={contentUrl ? new URL(row.rawUrl ?? contentUrl).origin : undefined}
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
