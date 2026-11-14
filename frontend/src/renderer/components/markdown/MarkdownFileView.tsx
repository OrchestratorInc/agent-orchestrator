import type { FilesSource } from "../../hooks/useSessionWorkspaceFiles";

import Markdown, { type Components } from "react-markdown";
import type { PluggableList } from "unified";
import remarkGfm from "remark-gfm";
import { rehypeGithubAlerts } from "rehype-github-alerts";
import rehypeSlug from "rehype-slug";
import rehypeAutolinkHeadings from "rehype-autolink-headings";
import { useCallback, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useMarkdownFontsReady } from "../../hooks/useMarkdownFontsReady";
import { canonicalLanguage } from "../../lib/code-highlight";
import { fenceOf } from "../../lib/markdown-fence";
import { isWebLink } from "../../lib/external-link-policy";
import { isInlineMath, remarkMath } from "../../lib/remark-math";
import { stripMarkdownFrontmatter } from "../../lib/markdown-frontmatter";
import { useUiStore } from "../../stores/ui-store";
import { cn } from "../../lib/utils";
import { CopyButton } from "../chat/CopyButton";
import { HighlightedCode } from "../chat/HighlightedCode";
import { MermaidBlock } from "../chat/MermaidBlock";
import { MathFormula } from "../chat/MathFormula";
import "../chat/code-theme.css";
import "github-markdown-css/github-markdown.css";
import "./markdown-file.css";
import { MarkdownFileContext } from "./markdown-file-context";
import { MarkdownImage } from "./MarkdownImage";
import { MarkdownExternalLink } from "./MarkdownExternalLink";

const REMARK_PLUGINS = [remarkGfm];
const MATH_REMARK_PLUGINS = [remarkGfm, remarkMath];

const REHYPE_PLUGINS: PluggableList = [
	[rehypeGithubAlerts, {}],
	rehypeSlug,
	[
		rehypeAutolinkHeadings,
		{
			properties: { className: ["anchor"], ariaHidden: true, tabIndex: -1 },
			content: { type: "element", tagName: "span", properties: { className: ["octicon", "octicon-link"] }, children: [] },
		},
	],
];


export function MarkdownFileView({
	sessionId,
	hostId,
	filePath,
	content,
	truncated,
	version,
	artifactOrigin,
	snapshotSource,
	presentation = "document",
}: {
	sessionId: string;
	hostId?: string;
	filePath: string;
	content: string;
	truncated: boolean;
	version: number;
	artifactOrigin?: string;
	snapshotSource?: Extract<FilesSource,{kind:"snapshot"}>;
	presentation?: "document" | "thumbnail";
}) {
	const { t } = useTranslation();
	const fontsReady = useMarkdownFontsReady();
	const thumbnail = presentation === "thumbnail";
	const body = stripMarkdownFrontmatter(content);
	const bodyRef = useRef<HTMLDivElement>(null);
	const renderMath = useUiStore((state) => state.renderMath);
	const contextValue = useMemo(() => ({ sessionId, hostId, filePath, version, artifactOrigin, snapshotSource }), [sessionId, hostId, filePath, version, artifactOrigin,snapshotSource]);

	const onFragmentClick = useCallback((fragment: string) => {
		const id = fragment.slice(1);
		if (!id) return;
		bodyRef.current
			?.querySelector(`[id="${CSS.escape(id)}"]`)
			?.scrollIntoView({ behavior: "smooth", block: "start" });
	}, []);

	const components = useMemo<Components>(
		() => ({
			pre: ({ children }) => {
				const fence = fenceOf(children);
				if (!fence) return <>{children}</>;
				if (fence.language?.toLowerCase() === "mermaid") {
					return <MermaidBlock code={fence.code} interactive={!thumbnail} />;
				}
				if (fence.language?.toLowerCase() === "math") return <MathFormula tex={fence.code} display />;
				return (
					<div className="group/md-code relative">
						<pre className="markdown-code">
							<code>
								<HighlightedCode code={fence.code} language={canonicalLanguage(fence.language)} />
							</code>
						</pre>
						{thumbnail ? null : <CopyButton
							compact
							className="absolute right-2 top-2 bg-background opacity-0 transition-opacity focus-visible:opacity-100 group-hover/md-code:opacity-100"
							label={t("files.copyCode")}
							text={fence.code}
						/>}
					</div>
				);
			},

			code: ({ node: _node, className, children, ...rest }) =>
				isInlineMath(className) ? (
					<MathFormula tex={String(children ?? "")} />
				) : (
					<code className={className} {...rest}>
						{children}
					</code>
				),

			img: ({ src, alt }) => <MarkdownImage src={typeof src === "string" ? src : undefined} alt={alt} />,

			a: ({ href, children, node: _node, ...rest }) => {
				if (thumbnail) return <span>{children}</span>;
				if (!href) return <>{children}</>;
				if (href.startsWith("#")) {
					return (
						<a
							href={href}
							{...rest}
							onClick={(event) => {
								event.preventDefault();
								onFragmentClick(href);
							}}
						>
							{children}
						</a>
					);
				}
				if (isWebLink(href) || href.startsWith("mailto:")) {
					return <MarkdownExternalLink href={href}>{children}</MarkdownExternalLink>;
				}
				return <span>{children}</span>;
			},
		}),
		[onFragmentClick, t, thumbnail],
	);
	return (
		<MarkdownFileContext.Provider value={contextValue}>
			<div className={cn("ao-document-surface min-h-full select-text", thumbnail && "h-full")} inert={thumbnail || undefined}>
				{truncated && !thumbnail ? (
					<div className="shrink-0 border-b border-border bg-warning/10 px-3 py-1.5 text-xs text-warning">
						{t("files.contentTruncated")}
					</div>
				) : null}
				<div className={cn("markdown-body ao-markdown", thumbnail && "min-h-full")} ref={bodyRef} style={{ visibility: fontsReady ? "visible" : "hidden" }}>
					<Markdown remarkPlugins={renderMath ? MATH_REMARK_PLUGINS : REMARK_PLUGINS} rehypePlugins={REHYPE_PLUGINS} components={components}>
						{thumbnail ? body.slice(0, 4000) : body}
					</Markdown>
				</div>
			</div>
		</MarkdownFileContext.Provider>
	);
}
