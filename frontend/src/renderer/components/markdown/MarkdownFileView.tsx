import type { FilesSource } from "../../hooks/useSessionWorkspaceFiles";
/**
 * GitHub-accurate rendered view of a `.md` file's current worktree content.
 *
 * Shown as the "Preview" tab alongside a markdown file's "Diff" in
 * `WorkspaceDiffView.tsx`. Styled with `github-markdown-css` (`.markdown-body`)
 * rather than AO's own design system — a deliberate, scoped exception for this
 * one view, since the whole point is to look like GitHub, not like AO chrome.
 *
 * Two choices carried over from `ChatMarkdown.tsx`, for the same reasons:
 *
 *   - `rehype-raw` is deliberately absent. A worker's markdown file is agent-
 *     produced content; markdown-only is the whole sanitization story and there
 *     is no schema to get wrong — except ```mermaid fences, which render as
 *     diagrams through the shared `MermaidBlock` (strict mode plus DOMPurify,
 *     active-content tags forbidden), and formulas, which the shared
 *     `MathFormula` typesets with KaTeX's `trust: false`, matching the chat surface.
 *   - Fenced code reuses the app's Shiki engine (`lib/code-highlight.ts`).
 *     Its JavaScript regex engine needs no WASM or CSP exception, and its
 *     syntax classes use AO's existing palette in both themes.
 *
 * No explicit light/dark plumbing is needed for `github-markdown-css`: its
 * combined stylesheet follows `prefers-color-scheme`, and `main.ts` already
 * drives Electron's `nativeTheme.themeSource` from AO's own theme preference,
 * so this renderer's `prefers-color-scheme` already tracks AO's theme, not the
 * raw OS setting. (In `npm run dev:web` there is no `nativeTheme`, so it follows
 * the OS directly.)
 */

import Markdown, { type Components } from "react-markdown";
import type { PluggableList } from "unified";
import remarkGfm from "remark-gfm";
import { rehypeGithubAlerts } from "rehype-github-alerts";
import rehypeSlug from "rehype-slug";
import rehypeAutolinkHeadings from "rehype-autolink-headings";
import { useCallback, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { canonicalLanguage } from "../../lib/code-highlight";
import { fenceOf } from "../../lib/markdown-fence";
import { isWebLink } from "../../lib/external-link-policy";
import { isInlineMath, remarkMath } from "../../lib/remark-math";
import { useUiStore } from "../../stores/ui-store";
import { CopyButton } from "../chat/CopyButton";
import { HighlightedCode } from "../chat/HighlightedCode";
import { MermaidBlock } from "../chat/MermaidBlock";
import { MathFormula } from "../chat/MathFormula";
import "../chat/code-theme.css";
import "github-markdown-css/github-markdown.css";
import { MarkdownFileContext } from "./markdown-file-context";
import { MarkdownImage } from "./MarkdownImage";
import { MarkdownExternalLink } from "./MarkdownExternalLink";

const REMARK_PLUGINS = [remarkGfm];
const MATH_REMARK_PLUGINS = [remarkGfm, remarkMath];

// rehype-slug must run before rehype-autolink-headings, which reads the `id`
// the former just attached. The anchor mirrors GitHub's own markup exactly
// (`.anchor` > `.octicon.octicon-link`) so github-markdown-css's own hover-
// reveal CSS applies with no extra styling of our own.
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
}: {
	sessionId: string;
	hostId?: string;
	filePath: string;
	content: string;
	/** The daemon capped the file it sent; what renders below is a prefix of it. */
	truncated: boolean;
	/** The file detail's load timestamp, for cache-busting relative images. */
	version: number;
	/** Origin serving the artifact directory, for artifact markdown images. */
	artifactOrigin?: string;
 snapshotSource?: Extract<FilesSource,{kind:"snapshot"}>;
}) {
	const { t } = useTranslation();
	const bodyRef = useRef<HTMLDivElement>(null);
	const renderMath = useUiStore((state) => state.renderMath);
	const contextValue = useMemo(() => ({ sessionId, hostId, filePath, version, artifactOrigin, snapshotSource }), [sessionId, hostId, filePath, version, artifactOrigin,snapshotSource]);

	// Heading slugs are unique per rendered file, not per document, and the Files
	// tab expands many files at once — two open READMEs both containing
	// "## Overview" put two `id="overview"` in the same DOM. Resolving inside this
	// file's own container is what keeps a fragment link on the file it was
	// written in; `document.getElementById` would hand back whichever came first.
	// It also leaves a hand-written `[x](#overview)` working, which prefixing the
	// slugs would have broken.
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
				// Diagrams render through the same sandboxed SVG path as chat;
				// a markdown preview that showed source where chat showed a
				// picture would disagree with the conversation about the file.
				if (fence.language?.toLowerCase() === "mermaid") {
					return <MermaidBlock code={fence.code} />;
				}
				if (fence.language?.toLowerCase() === "math") return <MathFormula tex={fence.code} display />;
				return (
					<div className="group/md-code relative">
						<pre className="markdown-code">
							<code>
								<HighlightedCode code={fence.code} language={canonicalLanguage(fence.language)} />
							</code>
						</pre>
						{/* Hover-revealed, and focus-revealed so the keyboard can reach it. */}
						<CopyButton
							compact
							className="absolute right-2 top-2 bg-background opacity-0 transition-opacity focus-visible:opacity-100 group-hover/md-code:opacity-100"
							label={t("files.copyCode")}
							text={fence.code}
						/>
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

			// A dispatcher, not a single link renderer: a same-page heading fragment
			// scrolls to that heading; a genuine http(s)/mailto link opens
			// externally; a relative link to another repo file (`./OTHER.md`) has no
			// in-app navigation target today, so it renders as inert text rather than
			// a link that would silently do nothing (or worse, navigate the app
			// shell).
			a: ({ href, children, node: _node, ...rest }) => {
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
		[onFragmentClick, t],
	);
	return (
		<MarkdownFileContext.Provider value={contextValue}>
			<div>
				{truncated ? (
					<div className="shrink-0 border-b border-border bg-warning/10 px-3 py-1.5 text-xs text-warning">
						{t("files.contentTruncated")}
					</div>
				) : null}
				{/* github-markdown-css hangs each heading's `.anchor` link icon 20px
				    into the left gutter (`margin-left: -20px`), so the inline padding
				    must be wider than that or the scroll container clips the icon. */}
				<div className="markdown-body px-6 py-4" ref={bodyRef}>
					<Markdown remarkPlugins={renderMath ? MATH_REMARK_PLUGINS : REMARK_PLUGINS} rehypePlugins={REHYPE_PLUGINS} components={components}>
						{content}
					</Markdown>
				</div>
			</div>
		</MarkdownFileContext.Provider>
	);
}
