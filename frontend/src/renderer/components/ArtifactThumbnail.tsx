import { useQuery } from "@tanstack/react-query";
import { File, FileText, Globe, Image, Table2, Workflow } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { artifactContentUrl } from "../lib/artifact-content-url";
import type { ArtifactRow } from "../lib/artifacts";
import { isMermaidPath, isRasterImagePath, isSvgPath } from "../lib/image-files";
import { isDelimitedTextPath } from "../lib/delimited-text";
import { fenceOf } from "../lib/markdown-fence";
import { isInlineMath, remarkMath } from "../lib/remark-math";
import { useUiStore } from "../stores/ui-store";
import { MermaidBlock } from "./chat/MermaidBlock";
import { MathFormula } from "./chat/MathFormula";
import { PdfPreview } from "./PdfPreview";
import { TableFileView } from "./TableFileView";

const PAGE_WIDTH = 1000;
const PAGE_HEIGHT = 625;

/** Gallery previews load near the viewport; pages keep the isolated inline origin. */
export function ArtifactThumbnail({ row, previewLive = true }: { row: ArtifactRow; previewLive?: boolean }) {
	const { t } = useTranslation();
	const renderMath = useUiStore((state) => state.renderMath);
	const container = useRef<HTMLSpanElement>(null);
	const [visible, setVisible] = useState(false);
	const [width, setWidth] = useState(0);
	const [failed, setFailed] = useState(false);
	const [pageAvailable, setPageAvailable] = useState<boolean>();
	const page = previewLive && row.kind === "html" && row.inlineUrl && isInlineArtifactUrl(row.inlineUrl) ? row.inlineUrl : undefined;
	const contentUrl = artifactContentUrl(row.sessionId, row.path, row.rawUrl);
	const image = isRasterImagePath(row.path) || isSvgPath(row.path)
		? contentUrl : undefined;
	const plainText = /\.txt$/i.test(row.path);
	const table = isDelimitedTextPath(row.path);
	const mermaid = isMermaidPath(row.path);
	const pdf = previewLive && !row.cloud && /\.pdf$/i.test(row.path) ? contentUrl : undefined;
	const textPreview = row.kind === "markdown" || plainText || table || mermaid;
	const fileOptions = sessionArtifactFileQueryOptions(row.sessionId, row.path, row.rawUrl, undefined, Boolean(row.cloud));
	const file = useQuery({
		...fileOptions,
		queryKey: [...fileOptions.queryKey, row.updatedAt],
		enabled: visible && Boolean(contentUrl) && textPreview,
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
		setPageAvailable(undefined);
		if (!visible || !page || !contentUrl) return;
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
	}, [visible, page, contentUrl, row.updatedAt]);
	useLayoutEffect(() => {
		const element = container.current;
		if (!element || !page) return;
		const initialWidth = element.getBoundingClientRect().width;
		if (initialWidth > 0) setWidth(initialWidth);
		const observer = new ResizeObserver(([entry]) => {
			if (entry && entry.contentRect.width > 0) setWidth(entry.contentRect.width);
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, [page]);
	const content = file.data && !file.data.binary ? file.data.content : "";
	const markdown = row.kind === "markdown"
		? content.replace(/^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n---[ \t]*(?:\r?\n|$)/, "").trim().slice(0, 4000) : "";
	const text = plainText && content.trim() ? content.slice(0, 4000) : "";
	const Icon = image ? Image : row.kind === "html" ? Globe : table ? Table2 : mermaid ? Workflow : row.kind === "markdown" || plainText || pdf ? FileText : File;
	const unavailable = failed || file.isError || pageAvailable === false || (page && !contentUrl) || (textPreview && !contentUrl)
		|| (!page && !image && !pdf && !textPreview)
		|| (file.isSuccess && textPreview && !content.trim());
	return (
		<span inert ref={container} className="pointer-events-none absolute inset-0 block overflow-hidden bg-[var(--solid-muted,var(--muted))]" aria-hidden="true">
			{unavailable ? (
				<span className="absolute inset-0 grid place-items-center text-passive" title={t("artifactLink.noPreview")}>
					<Icon className="size-7" />
				</span>
			) : !visible ? null : image ? (
				<img src={image} alt="" loading="eager" onError={() => setFailed(true)} className="h-full w-full object-cover object-top" />
			) : page ? (
				pageAvailable ? (
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
				<span className="block h-full overflow-hidden p-4 text-caption leading-relaxed text-foreground [mask-image:linear-gradient(to_bottom,black_75%,transparent)] [&_h1]:mb-3 [&_h1]:text-heading-sm [&_h1]:font-semibold [&_h2]:my-2 [&_h2]:text-control [&_h2]:font-semibold [&_h3]:my-2 [&_h3]:font-semibold [&_p]:my-2 [&_ul]:list-disc [&_ul]:pl-4 [&_ol]:list-decimal [&_ol]:pl-4 [&_pre]:overflow-hidden [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-2 [&_code]:font-mono [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_table]:w-full [&_td]:border-b [&_td]:border-border [&_td]:py-1 [&_th]:border-b [&_th]:border-border [&_th]:py-1 [&_th]:text-left">
					<Markdown remarkPlugins={renderMath ? [remarkGfm, remarkMath] : [remarkGfm]} components={{
						a: ({ children }) => <span>{children}</span>,
						img: ({ alt }) => <span>{alt}</span>,
						input: () => null,
						pre: ({ children }) => {
							const fence = fenceOf(children);
							if (fence?.language?.toLowerCase() === "mermaid") return <MermaidBlock code={fence.code} />;
							if (fence?.language?.toLowerCase() === "math") return <MathFormula tex={fence.code} display />;
							return <pre>{children}</pre>;
						},
						code: ({ className, children }) => isInlineMath(className)
							? <MathFormula tex={String(children ?? "")} />
							: <code className={className}>{children}</code>,
					}}>{markdown}</Markdown>
				</span>
			)}
		</span>
	);
}
