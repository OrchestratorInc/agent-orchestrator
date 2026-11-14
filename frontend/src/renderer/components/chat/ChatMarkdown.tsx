import type { Element as HastElement } from "hast";
import { rehypeExcerptSource, excerptElementProps } from "../../lib/rehype-excerpt-source";
import { pageHidden } from "../../lib/page-hidden";
/**
 * Markdown for agent prose.
 *
 * Agents write markdown — headings, lists, tables, fenced code — and rendering it
 * as preformatted text was showing the syntax instead of the structure. `## Plan`
 * appeared as literal hashes, a table as a wall of pipes, a code block bracketed by
 * visible backticks.
 *
 * Raw HTML is escaped, not rendered. Agent output is only as trustworthy as the
 *     files it just read, so `rehype-raw` runs only where a caller passes it with a
 *     sanitizer (GitHub review bodies); otherwise markdown-only is
 *     the whole sanitization story and there is no schema to get wrong, with two
 *     exceptions: ```mermaid fences render as diagrams through `MermaidBlock`,
 *     whose SVG passes through mermaid's strict mode plus DOMPurify with
 *     active-content tags forbidden; and formulas render through `MathFormula`,
 *     whose KaTeX markup is typeset with `trust: false`. Syntax
 *     highlighting keeps that property — it renders a token tree as elements
 *     rather than injecting the highlighter's HTML string.
 *
 * Streaming reuses completed blocks and repairs incomplete inline formatting
 * for display only. Copy actions continue to use the stored source.
 */

import {
	createContext,
	Fragment,
	memo,
	useCallback,
	useContext,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type MouseEvent as ReactMouseEvent,
	type ReactNode,
} from "react";
import { QueryClientContext } from "@tanstack/react-query";
import Markdown, { defaultUrlTransform, type Components, type Options } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Root, RootContent } from "mdast";
import type { Processor } from "unified";
import remend from "remend";
import { splitStreamingMarkdown } from "../../lib/streaming-markdown";
import { WrapText } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { useLatestCallback } from "../../hooks/useStable";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { isHostLocalWebLink } from "../../lib/loopback";
import { canonicalLanguage } from "../../lib/code-highlight";
import { fenceOf, textOf } from "../../lib/markdown-fence";
import { EMOJI_GRAPHEME, rehypeStreamFade } from "../../lib/rehype-stream-fade";
import { isInlineMath, remarkMath } from "../../lib/remark-math";
import { useUiStore } from "../../stores/ui-store";
import { artifactLinkForPath, artifactLinkForTarget, findSessionLinks, hasArtifactDirectorySegment, isAoLink, isSessionLink, remarkSessionLinks, sessionArtifactPath } from "../../lib/session-links";
import { useSessionLinkSource } from "../../lib/use-session-link-navigation";
import type { WorkspaceSummary } from "../../types/workspace";
import {
	isPotentialWorkspaceFileLink,
	isWebLink,
	openLinkInSystemBrowser,
	workspaceFilePath,
} from "../../lib/external-link-policy";
import { AppLink } from "../AppLink";
import { LinkFavicon } from "../LinkPreviewCard";
import { useCachedLinkFavicons } from "../../hooks/useLinkPreview";
import { canLoadLinkFavicon, preloadLinkFavicon } from "../../lib/link-preload";
import { ChatPathMenu, ChatPathMenuContext } from "../ChatPathMenu";
import { ArtifactLinkPreviewCard, SessionLinkPreviewCard } from "../SessionLinkPreviewCard";
import {
	explicitWorkspaceFilePath,
	createWorkspaceFileLookup,
	isAbsoluteFileReference,
	normalizeWorkspaceFileReference,
	workspaceFileOpenTarget,
	workspaceFileReferenceLine,
} from "../../lib/workspace-file-path";
import { HighlightedCode } from "./HighlightedCode";
import { MermaidBlock } from "./MermaidBlock";
import { MathFormula } from "./MathFormula";
import { CopyButton } from "./CopyButton";
import { ChatImage, ChatImageGallery, ChatImageLinkScope, isImageOnlyParagraph } from "./ChatImage";
import "./code-theme.css";

// Activity titles live inside disclosure buttons: keep inline formatting, but
// unwrap block elements and links so provider text cannot nest interactive UI.
const TITLE_ELEMENTS = ["code", "em", "strong", "del"];
const TITLE_COMPONENTS: Components = {
	code: ({ children }) => <code className="font-mono text-[0.95em]">{children}</code>,
};

export const ActivityTitle = memo(function ActivityTitle({ text }: { text: string }) {
	return (
		<Markdown allowedElements={TITLE_ELEMENTS} unwrapDisallowed components={TITLE_COMPONENTS}>
			{text}
		</Markdown>
	);
});

/** GitHub-flavoured markdown: tables, strikethrough, task lists, autolinks. */
const PLUGINS = [remarkGfm, remarkSessionLinks];
const MATH_PLUGINS = [...PLUGINS, remarkMath];
// Reduced motion gets plain text, not a span per character that never animates.
const prefersReducedMotion = () => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
// How long a settled reply keeps its fade spans, so the last characters (and the final
// flush of buffered text) finish fading in before they are dropped.
const FADE_SHED_MS = 450;

/**
 * Whether the prose is still arriving, for the fences inside it.
 *
 * A context rather than a prop because `COMPONENTS` has to stay module-level:
 * rebuilding that map per render would defeat react-markdown's own memoization
 * and re-parse every message on every poll.
 */
const StreamingProse = createContext(false);
const InsideMarkdownLink = createContext(false);
/** Inside a link to an artifact: its name reads in the UI font, as everywhere else artifacts are named. */
const InsideArtifactLink = createContext(false);
const SafeOriginContent = createContext(false);
const REMOTE_PREVIEW_UNAVAILABLE = "This link points to the remote host. Preview is unavailable on this device.";
const OpenChatLink = createContext<{
	sessionId?: string;
	open?: (url: string) => void;
	openFile?: (path: string, line?: number) => void;
	remoteHost?: boolean;
	sessionLinkHostId?: string;
	sessionLinkSourceKind?: "cloud";
	openSession?: (url: string) => void;
	workspacePaths: string[];
	findFile: (reference: string) => string | undefined;
}>({ workspacePaths: [], findFile: () => undefined });
const EMPTY_WORKSPACE_PATHS: string[] = [];
const EMPTY_LINK_WORKSPACES: WorkspaceSummary[] = [];
/**
 * The workspaces this chat's session and artifact links resolve against,
 * read once for the chat, from the first link that asks, and shared by every
 * link in it. Empty outside a ChatLinkProvider, or without a query client to
 * read them with.
 */
const ChatLinkWorkspaces = createContext<{ workspaces: WorkspaceSummary[]; demand?: () => void }>({ workspaces: EMPTY_LINK_WORKSPACES });

/**
 * Reads the link workspaces for its ChatLinkProvider. A leaf beside the chat,
 * not a wrapper around it, so starting the read never remounts the chat.
 */
function ChatLinkWorkspacesSource({ hostId, kind, onWorkspaces }: { hostId?: string; kind?: "cloud"; onWorkspaces: (workspaces: WorkspaceSummary[]) => void }) {
	const { workspaces } = useSessionLinkSource(hostId, kind);
	// A source with no data answers a fresh [] on every render; one empty list
	// keeps the provider's re-render from asking again.
	const shared = workspaces.length === 0 ? EMPTY_LINK_WORKSPACES : workspaces;
	useLayoutEffect(() => onWorkspaces(shared), [onWorkspaces, shared]);
	return null;
}

export function ChatLinkProvider({
	sessionId,
	onLinkOpen,
	onFileOpen,
	remoteHost,
	sessionLinkHostId,
	sessionLinkSourceKind,
	onSessionLinkOpen,
	workspacePaths = EMPTY_WORKSPACE_PATHS,
	children,
}: {
	sessionId?: string;
	onLinkOpen?: (url: string) => void;
	onFileOpen?: (path: string, line?: number) => void;
	remoteHost?: boolean;
	sessionLinkHostId?: string;
	sessionLinkSourceKind?: "cloud";
	onSessionLinkOpen?: (url: string) => void;
	workspacePaths?: string[];
	children: ReactNode;
}) {
	const openLink = useLatestCallback((url: string) => onLinkOpen?.(url));
	const openFile = useLatestCallback((path: string, line?: number) => line == null ? onFileOpen?.(path) : onFileOpen?.(path, line));
	const openSession = useLatestCallback((url: string) => onSessionLinkOpen?.(url));
	const findFile = useMemo(() => createWorkspaceFileLookup(workspacePaths), [workspacePaths]);
	const canOpenLink = Boolean(onLinkOpen);
	const canOpenFile = Boolean(onFileOpen);
	const canOpenSession = Boolean(onSessionLinkOpen);
	const value = useMemo(
		() => ({ sessionId, open: canOpenLink ? openLink : undefined, openFile: canOpenFile ? openFile : undefined, openSession: canOpenSession ? openSession : undefined, remoteHost, sessionLinkHostId, sessionLinkSourceKind, workspacePaths, findFile }),
		[sessionId, canOpenLink, canOpenFile, canOpenSession, openLink, openFile, openSession, remoteHost, sessionLinkHostId, sessionLinkSourceKind, workspacePaths, findFile],
	);
	const pathMenuValue = useMemo(
		() => ({ sessionId, remote: Boolean(remoteHost || sessionLinkHostId || sessionLinkSourceKind === "cloud") }),
		[sessionId, remoteHost, sessionLinkHostId, sessionLinkSourceKind],
	);
	// The link workspaces come from queries, read only once something in the
	// chat asks for them. A render without a query client (an isolated tree)
	// links nothing to an artifact rather than throwing.
	const hasQueryClient = useContext(QueryClientContext) !== undefined;
	const [linkWorkspacesDemanded, setLinkWorkspacesDemanded] = useState(false);
	const [linkWorkspaces, setLinkWorkspaces] = useState(EMPTY_LINK_WORKSPACES);
	const demandLinkWorkspaces = useCallback(() => setLinkWorkspacesDemanded(true), []);
	const linkWorkspacesValue = useMemo(() => ({ workspaces: linkWorkspaces, demand: demandLinkWorkspaces }), [linkWorkspaces, demandLinkWorkspaces]);
	return <ChatPathMenuContext.Provider value={pathMenuValue}>
		<OpenChatLink.Provider value={value}>
			<ChatLinkWorkspaces.Provider value={linkWorkspacesValue}>{children}</ChatLinkWorkspaces.Provider>
		</OpenChatLink.Provider>
		{hasQueryClient && linkWorkspacesDemanded
			? <ChatLinkWorkspacesSource key={`${sessionLinkHostId ?? ""}:${sessionLinkSourceKind ?? ""}`} hostId={sessionLinkHostId} kind={sessionLinkSourceKind} onWorkspaces={setLinkWorkspaces} />
			: null}
	</ChatPathMenuContext.Provider>;
}

function chatUrlTransform(url: string, key: string): string | undefined {
	// react-markdown correctly strips unknown schemes, but a Windows absolute
	// path resembles one (C:). Preserve only hrefs that look like local paths;
	// the click still goes through the workspace-confined preview endpoint.
	if (isAoLink(url)) return url;
	if (key === "href" && isPotentialWorkspaceFileLink(url)) return url;
	return defaultUrlTransform(url);
}

/**
 * True while the surrounding chat is rendering its first commit. A stable ref,
 * not a value, so ending the opening never re-renders every reply.
 */
export const ChatOpeningRef = createContext<{ readonly current: boolean }>({ current: false });

export const ChatMarkdown = memo(function ChatMarkdown({
	text,
	streaming = false,
	muted = false,
	className,
	safeOrigin = false,
	remarkPlugins,
	rehypePlugins,
}: {
	text: string;
	streaming?: boolean;
	/**
	 * Prose that is not the answer. Reasoning is the case: it reads alongside the
	 * agent's reply and must not compete with it, so it steps back a tone rather than
	 * being dimmed with opacity — which would wash out code and links too.
	 */
	muted?: boolean;
	className?: string;
	safeOrigin?: boolean;
	/** Run after the built-in plugins; keep the array stable, a new one re-parses the text. */
	remarkPlugins?: Options["remarkPlugins"];
	rehypePlugins?: Options["rehypePlugins"];
}) {
	// Replies opened from history mount settled and never wrap; live ones keep the fade a
	// beat past the end of the stream.
	const [fading, setFading] = useState(streaming);
	if (streaming && !fading) setFading(true);
	// A reply already on screen when its chat opens (switching to a chat
	// mid-stream) is not new; only characters that arrive afterwards fade in. A
	// reply that arrives later fades from its first character. Text that arrives
	// while the window is hidden is not watched either: its fade could only play
	// once the reader is back, replaying the whole stretch at once.
	const chatOpening = useContext(ChatOpeningRef);
	const [openedOffset] = useState(() => (chatOpening.current ? text.length : 0));
	const hiddenOffset = useRef(0);
	if (pageHidden()) hiddenOffset.current = text.length;
	const settledOffset = Math.max(openedOffset, hiddenOffset.current);
	const renderMath = useUiStore((state) => state.renderMath);
	const plugins = useMemo(() => {
		const base = renderMath ? MATH_PLUGINS : PLUGINS;
		return remarkPlugins ? [...base, ...remarkPlugins] : base;
	}, [renderMath, remarkPlugins]);
	// History stays on the single-parser path. Replies that streamed retain
	// their chunks on completion, preserving code controls and diagram state.
	const [parsed, setParsed] = useState(() => streaming ? splitStreamingMarkdown(text, undefined, renderMath) : {
		source: text, math: renderMath, blocks: [{ source: text, start: 0, repairInline: false }], stableCount: 0,
	});
	// Chunks are cut, and their prose repair decided, in the math dialect they
	// render in, so turning math on or off re-cuts them. A settled single block
	// is exempt: it is a whole document in either dialect, and repair only runs
	// while streaming.
	const current = parsed.source === text && (parsed.math === renderMath || (!streaming && parsed.blocks.length === 1));
	const blocks = current ? parsed : splitStreamingMarkdown(text, parsed, renderMath);
	if (blocks !== parsed) setParsed(blocks);
	const animate = fading && !prefersReducedMotion();
	useEffect(() => {
		if (streaming) return;
		const timer = setTimeout(() => setFading(false), FADE_SHED_MS);
		return () => clearTimeout(timer);
	}, [streaming]);

	return (
		<SafeOriginContent.Provider value={safeOrigin}>
			<div
				className={cn(
					"chat-md leading-[1.58]",
					muted ? "text-sm text-muted-foreground" : "text-sm text-foreground",
					className,
				)}
			>
				{blocks.blocks.map((block, index) => (
					<MarkdownChunk
						key={index}
						source={block.source}
						sourceOffset={block.start}
						repairInline={block.repairInline}
						streaming={streaming && index === blocks.blocks.length - 1}
						fading={animate && index === blocks.blocks.length - 1}
						settledOffset={Math.max(0, settledOffset - block.start)}
						remarkPlugins={plugins}
						rehypePlugins={rehypePlugins}
					/>
				))}
			</div>
		</SafeOriginContent.Provider>
	);
});

// No wrapper DOM: list/table layout, image galleries and prose spacing keep
// their existing parent. Only the changing chunk enters react-markdown again.
const MarkdownChunk = memo(function MarkdownChunk({ source, sourceOffset, repairInline, streaming, fading, settledOffset, remarkPlugins, rehypePlugins }: {
	source: string;
	sourceOffset: number;
	repairInline: boolean;
	streaming: boolean;
	fading: boolean;
	settledOffset: number;
	remarkPlugins: Options["remarkPlugins"];
	rehypePlugins: Options["rehypePlugins"];
}) {
	const repaired = useMemo(() => streaming && repairInline ? remend(source, {
		linkMode: "text-only", images: false, htmlTags: false,
		katex: false, setextHeadings: false, comparisonOperators: false,
	}) : source, [source, streaming, repairInline]);
	const plugins = useMemo<NonNullable<Options["rehypePlugins"]>>(() => {
		// Repaired streaming tails cannot be selected. Settled chunks retain only
		// source bounds; detailed mappings are built when a selection is captured.
		const entries: NonNullable<Options["rehypePlugins"]> = [...(rehypePlugins ?? [])];
		if (!streaming) entries.push([rehypeExcerptSource, { offset: sourceOffset }]);
		if (fading) entries.push([rehypeStreamFade, { settledOffset }]);
		return entries;
	}, [rehypePlugins, sourceOffset, streaming, fading, settledOffset]);
	return <StreamingProse.Provider value={streaming}>
		<Markdown remarkPlugins={remarkPlugins} rehypePlugins={plugins} components={COMPONENTS} urlTransform={chatUrlTransform}>
			{repaired}
		</Markdown>
	</StreamingProse.Provider>;
});

/**
 * Wrapping is a reading preference, not a property of one block: a fence that
 * scrolled out of view and back must not silently lose it, and a reader who
 * turned wrapping on is usually about to want it again — so the last choice seeds
 * the next block. Deliberately not persisted; reflowing every block of a
 * conversation on launch because of a choice made days ago is worse than starting
 * from the readable default.
 */
let preferredWrap = false;

/**
 * A fenced code block: language on the left, quiet actions on the right, its own
 * scroll.
 *
 * The block scrolls rather than wrapping because a wrapped line of code is harder
 * to read than a scrolled one, and it must never widen the conversation column.
 * The toggle is there for the case that argument loses — a single 300-character
 * line, where scrolling means losing the rest of the block.
 */
function CodeBlock({ code, language, sourceNode }: { code: string; language?: string; sourceNode?: HastElement }) {
	const positions = excerptElementProps(sourceNode);
	const streaming = useContext(StreamingProse);
	const grammar = canonicalLanguage(language);
	const [wrap, setWrap] = useState(preferredWrap);

	return (
		<div
			{...positions}
			className="chat-code group/code my-2.5 overflow-hidden rounded-lg border border-border bg-surface"
			data-wrap={wrap ? "true" : "false"}
		>
			<div data-excerpt-exclude="" data-chat-find-exclude="" className="flex items-center gap-2 border-b border-border bg-raised/40 px-2.5 py-1">
				<span className="text-caption text-muted-foreground">
					{language || "text"}
				</span>
				{/* Hover-revealed, and focus-revealed so the keyboard can reach it. The
				    timeline's whole point is that prose dominates; a permanent row of
				    controls on every fence is the opposite. */}
				<div className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity duration-150 focus-within:opacity-100 group-hover/code:opacity-100">
					<Button
						type="button"
						variant="quiet"
						size="icon-sm"
						onClick={() => {
							preferredWrap = !wrap;
							setWrap(!wrap);
						}}
						aria-pressed={wrap}
						aria-label="Wrap long lines"
						title="Wrap long lines"
					>
						<WrapText aria-hidden="true" className="size-3" />
					</Button>
					<CopyButton text={code} label="Copy code" />
				</div>
			</div>
			<pre className="scrollbar-none overflow-x-auto px-3 py-2.5">
				<code {...positions} data-excerpt-kind="block-code" className="font-mono text-xs leading-[1.6] text-foreground">
					<HighlightedCode code={code} language={grammar} streaming={streaming} />
				</code>
			</pre>
		</div>
	);
}

const GRAPHEME_SEGMENTER = new Intl.Segmenter(undefined, { granularity: "grapheme" });

function compactEmoji(children: ReactNode): ReactNode {
	if (typeof children === "string") {
		if (!EMOJI_GRAPHEME.test(children)) return children;
		let last = 0;
		const parts: ReactNode[] = [];
		for (const { segment, index } of GRAPHEME_SEGMENTER.segment(children)) {
			if (!EMOJI_GRAPHEME.test(segment)) continue;
			if (index > last) parts.push(children.slice(last, index));
			parts.push(
				<span key={`${index}-${segment}`} className="chat-md-emoji">
					{segment}
				</span>,
			);
			last = index + segment.length;
		}
		if (last === 0) return children;
		if (last < children.length) parts.push(children.slice(last));
		return parts;
	}
	if (Array.isArray(children)) {
		return children.map((child, index) => <Fragment key={index}>{compactEmoji(child)}</Fragment>);
	}
	return children;
}

type MarkdownLinkProps = { href?: string; children?: ReactNode; className?: string; menuPath?: string; node?: HastElement };

function MarkdownLink(props: MarkdownLinkProps) {
	const { href } = props;
	// Safe-origin text links only what it wrote as an ao:// link. Mapping its
	// file links to artifacts first would hand the safe-origin check a link the
	// text never wrote, so they stay text there. A chat that cannot open an
	// artifact link keeps the file link it can open.
	const safeOriginContent = useContext(SafeOriginContent);
	const { openSession } = useContext(OpenChatLink);
	return href && !safeOriginContent && openSession && !isWebLink(href) && !isAoLink(href) && hasArtifactDirectorySegment(href)
		? <ArtifactFileLink {...props} href={href} />
		: <MarkdownLinkTarget {...props} />;
}

/**
 * A link to the file a session artifact was written to (the worker writes it
 * under .../artifacts/<session>/<path>), opened as that artifact when the
 * session lists it: the file lives outside the checkout, so the workspace file
 * view cannot read it. Its path actions still name the file linked.
 */
function ArtifactFileLink(props: MarkdownLinkProps & { href: string }) {
	const artifact = artifactLinkForTarget(props.href, useChatLinkWorkspaces());
	return artifact
		? <MarkdownLinkTarget {...props} href={artifact} menuPath={props.menuPath ?? explicitWorkspaceFilePath(props.href)} />
		: <MarkdownLinkTarget {...props} />;
}

/** Whether a markdown link wraps a picture, which marks itself. */
function linksImage(node: HastElement | undefined): boolean {
	return node?.children.some((child) => child.type === "element" && child.tagName === "img") ?? false;
}

/**
 * A web link's site icon before its text, behind a shimmering globe until it
 * arrives and a still one when none does. Asked for as soon as the link
 * renders (its site root only, once per site). A fixed 14px slot, so the text
 * never moves when the icon arrives or fails.
 */
function InlineLinkFavicon({ href }: { href: string }) {
	const cachedState = useCachedLinkFavicons(href);
	const favicons = cachedState === "failed" ? undefined : cachedState;
	const cached = Boolean(favicons);
	const loadable = useMemo(() => canLoadLinkFavicon(href), [href]);
	useEffect(() => {
		if (!cached && loadable) preloadLinkFavicon(href);
	}, [cached, href, loadable]);
	return (
		<span
			aria-hidden="true"
			data-link-favicon=""
			className="me-1 inline-flex size-3.5 select-none align-[-0.15em] text-muted-foreground"
		>
			<LinkFavicon
				faviconUrl={favicons?.faviconUrl}
				faviconDarkUrl={favicons?.faviconDarkUrl}
				pending={cachedState === undefined && loadable}
				className="size-3.5 shrink-0"
			/>
		</span>
	);
}

function MarkdownLinkTarget({ href, children, className, menuPath, node }: MarkdownLinkProps) {
	const safeOriginContent = useContext(SafeOriginContent);
	const { sessionId, open: onLinkOpen, openFile: onFileOpen, openSession: onSessionLinkOpen, remoteHost, sessionLinkHostId, sessionLinkSourceKind, workspacePaths, findFile } = useContext(OpenChatLink);
	const filePath = href && onFileOpen
		? workspaceFilePath(href, workspacePaths) ?? findFile(href) ?? explicitWorkspaceFilePath(href)
		: undefined;
	const sessionLink = Boolean(href && isAoLink(href));
	if (safeOriginContent && !sessionLink) return <>{children}</>;
	// An HTML file opens as a page in the Browser, except one in this session's
	// artifact directory: the file opener shows it in Artifacts, listed or not.
	// That file is named by the link's own path, never by a workspace file that
	// shares its name, and the link's query or fragment is cut while the target
	// is still encoded, so a name's own "#" or "?" (%23, %3F) stays.
	const linkedFile = href && sessionId && onFileOpen ? explicitWorkspaceFilePath(href.split(/[?#]/, 1)[0]!) : undefined;
	const ownArtifact = linkedFile && sessionId && sessionArtifactPath(linkedFile, sessionId) ? linkedFile : undefined;
	const openInFiles = ownArtifact ?? (filePath && href && !/\.html?$/i.test(filePath) ? workspaceFileOpenTarget(href, filePath) : undefined);
	if (remoteHost && href && (isHostLocalWebLink(href) || (isPotentialWorkspaceFileLink(href) && !openInFiles))) {
		const unavailable = <span className="text-muted-foreground" title={REMOTE_PREVIEW_UNAVAILABLE}>
			{children}<span className="sr-only"> (remote preview unavailable)</span>
		</span>;
		return isPotentialWorkspaceFileLink(href)
			? <ChatPathMenu path={normalizeWorkspaceFileReference(href)}>{unavailable}</ChatPathMenu>
			: unavailable;
	}
	const browserLink = href ? !sessionLink && (isWebLink(href) || (!remoteHost && (!!filePath || isPotentialWorkspaceFileLink(href)))) : false;
	return (
		<AppLink
			href={href}
			onBrowserOpen={onLinkOpen}
			inAppLink={href ? () => browserLink : undefined}
			filePath={ownArtifact ?? (filePath && href ? workspaceFileOpenTarget(href, filePath) : filePath)}
			menuPath={menuPath ?? (href && (filePath || isPotentialWorkspaceFileLink(href)) ? normalizeWorkspaceFileReference(href) : undefined)}
			onFileOpen={onFileOpen}
			hoverPreview={sessionLink
				? () => isSessionLink(href!)
					? <SessionLinkPreviewCard href={href!} sourceHostId={sessionLinkHostId} sourceKind={sessionLinkSourceKind} onOpen={onSessionLinkOpen && (() => onSessionLinkOpen(href!))} />
					: <ArtifactLinkPreviewCard href={href!} sourceHostId={sessionLinkHostId} sourceKind={sessionLinkSourceKind} />
				: undefined}
			onClick={(event) => {
				if (href && sessionLink) {
					event.preventDefault();
					onSessionLinkOpen?.(href);
					return;
				}
				if (openInFiles && onFileOpen) {
					event.preventDefault();
					const line = workspaceFileReferenceLine(href ?? "");
					if (line == null) onFileOpen(openInFiles);
					else onFileOpen(openInFiles, line);
					return;
				}
				if (href && !browserLink) {
					event.preventDefault();
					void openLinkInSystemBrowser(href);
				}
			}}
			target="_blank"
			rel="noreferrer noopener"
			className={className ?? "text-markdown-link underline decoration-markdown-link/45 underline-offset-2 transition-colors hover:text-markdown-link-hover hover:decoration-markdown-link-hover/75"}
		>
			{href && !sessionLink && isWebLink(href) && !linksImage(node) ? <InlineLinkFavicon href={href} /> : null}
			<ChatImageLinkScope>
				<InsideMarkdownLink.Provider value>
					<InsideArtifactLink.Provider value={sessionLink && !isSessionLink(href!)}>{children}</InsideArtifactLink.Provider>
				</InsideMarkdownLink.Provider>
			</ChatImageLinkScope>
		</AppLink>
	);
}

/** Render one safe in-app session or artifact link using the surrounding ChatLinkProvider. */
export function SessionLabelLink({ href, children, className }: { href: string; children: ReactNode; className?: string }) {
	return <MarkdownLink href={href} className={className}>{children}</MarkdownLink>;
}

function MarkdownImage({ src, alt }: { src?: string | Blob; alt?: string }) {
	if (useContext(SafeOriginContent)) return <span className="text-muted-foreground">{alt || (typeof src === "string" ? src : "")}</span>;
	const { remoteHost } = useContext(OpenChatLink);
	if (remoteHost && typeof src === "string" && isHostLocalWebLink(src)) {
		return <span className="text-muted-foreground" title={REMOTE_PREVIEW_UNAVAILABLE}>{alt || src}</span>;
	}
	return <ChatImage src={src} alt={alt} />;
}

const ORIGIN_PREVIEW_ELEMENTS = ["a", "br", "code", "del", "em", "img", "strong"];
const ORIGIN_PREVIEW_COMPONENTS: Components = {
	a: ({ href, children }) => href && isAoLink(href)
		? <MarkdownLink href={href}>{children}</MarkdownLink>
		: <>{children}</>,
	img: ({ alt }) => <span className="text-muted-foreground">{alt}</span>,
	code: ({ children }) => <code className="font-mono text-[0.95em] text-markdown-code">{children}</code>,
};

/**
 * A collapsed cross-boundary report preview. Inline formatting and canonical
 * session links remain useful, while block UI, external links, and remote image
 * fetches stay inert until the reader expands the report.
 */
export const OriginPreviewMarkdown = memo(function OriginPreviewMarkdown({ text }: { text: string }) {
	return (
		<div className="chat-md whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground">
			<Markdown
				allowedElements={ORIGIN_PREVIEW_ELEMENTS}
				unwrapDisallowed
				remarkPlugins={PLUGINS}
				components={ORIGIN_PREVIEW_COMPONENTS}
				urlTransform={chatUrlTransform}
			>
				{text}
			</Markdown>
		</div>
	);
});

/** Linkify canonical session URLs without interpreting any surrounding text as Markdown. */
export const SessionLinkedText = memo(function SessionLinkedText({ text }: { text: string }) {
	const links = findSessionLinks(text);
	if (links.length === 0) return text;
	const content: ReactNode[] = [];
	let cursor = 0;
	for (const link of links) {
		if (link.start > cursor) content.push(text.slice(cursor, link.start));
		content.push(
			<MarkdownLink key={`${link.start}:${link.text}`} href={link.text}>
				{link.text}
			</MarkdownLink>,
		);
		cursor = link.end;
	}
	if (cursor < text.length) content.push(text.slice(cursor));
	return <>{content}</>;
});

/**
 * A mermaid fence with the streaming state it was rendered under.
 *
 * A separate component rather than a hook call in the `pre` override because
 * overrides are not components: hooks are illegal there, and `COMPONENTS`
 * must stay module-level so react-markdown's memoization survives polling.
 */
function MermaidFence({ code }: { code: string }) {
	const streaming = useContext(StreamingProse);
	const { open: onLinkOpen, remoteHost } = useContext(OpenChatLink);
	const blockHostLocalLink = (event: ReactMouseEvent<HTMLDivElement>) => {
		const href = (event.target as Element).closest?.("a[href]")?.getAttribute("href");
		if (!href || !isHostLocalWebLink(href)) return;
		event.preventDefault();
		event.stopPropagation();
	};
	const diagram = <MermaidBlock code={code} streaming={streaming} onLinkOpen={onLinkOpen} />;
	return remoteHost
		? <div onClickCapture={blockHostLocalLink} onAuxClickCapture={blockHostLocalLink} onContextMenuCapture={blockHostLocalLink}>{diagram}</div>
		: diagram;
}

/** A path this chat can open in Files, and how to open it (at a line when given); undefined when it cannot. */
function useWorkspaceFile(path: string | undefined) {
	const { openFile, findFile } = useContext(OpenChatLink);
	const filePath = path && openFile ? findFile(path) : undefined;
	if (!path || !filePath || !openFile) return undefined;
	const target = workspaceFileOpenTarget(path, filePath);
	return { filePath, open: (line?: number) => (line == null ? openFile(target) : openFile(target, line)) };
}

/**
 * A workspace file named outside markdown: opens in Files at the line when this
 * chat can find it, as an inline code path does; otherwise plain text.
 */
export function FileReference({ path, line, className, children }: { path: string; line?: number; className?: string; children: ReactNode }) {
	const file = useWorkspaceFile(path);
	if (!file) return <span className={className} title={path}>{children}</span>;
	return (
		<Pressable data-chat-find-include="" className={className} title={path} onClick={() => file.open(line)}>
			{children}
		</Pressable>
	);
}

/** Opens a URL in this chat session's Browser panel; undefined when the chat cannot. */
export function useChatBrowserOpen() {
	return useContext(OpenChatLink).open;
}

/** The workspaces this chat's session links resolve against: the local daemon's, a remote host's, or Cloud's. */
export function useChatLinkWorkspaces(): WorkspaceSummary[] {
	const { workspaces, demand } = useContext(ChatLinkWorkspaces);
	// Before paint, so a link already known to name an artifact never shows as a file link first.
	useLayoutEffect(() => demand?.(), [demand]);
	return workspaces;
}

/**
 * Inline code naming a listed session artifact, linked to that artifact's
 * preview and showing `children`; a path the session does not list shows
 * `unlisted` (default `children`).
 */
export function ArtifactPathCode({ text, children, unlisted = children }: { text: string; children: ReactNode; unlisted?: ReactNode }) {
	const workspaces = useChatLinkWorkspaces();
	const href = artifactLinkForPath(text, workspaces);
	const path = explicitWorkspaceFilePath(text);
	// Every workspace refresh, for any session's status, reaches here; the same
	// element back lets the path menu below skip it unless the link changed.
	return useMemo(() => {
		if (href) return <MarkdownLink href={href} menuPath={path && isAbsoluteFileReference(path) ? path : undefined}>{children}</MarkdownLink>;
		return path ? <ChatPathMenu path={path}><span>{unlisted}</span></ChatPathMenu> : <>{unlisted}</>;
	}, [children, href, path, unlisted]);
}

function InlineCode({ children, className, node }: { children?: ReactNode; className?: string; node?: HastElement }) {
	const positions = excerptElementProps(node);
	const { openSession } = useContext(OpenChatLink);
	const insideLink = useContext(InsideMarkdownLink);
	const insideArtifactLink = useContext(InsideArtifactLink);
	const text = typeof children === "string" ? children : undefined;
	const file = useWorkspaceFile(text);
	if (isInlineMath(className)) return <span {...positions} data-excerpt-block=""><MathFormula tex={text ?? ""} /></span>;
	// An artifact's name is not code: it reads in the UI font, like its rows and
	// headers. It is still inline code in the source, so excerpts map it as one.
	const name = <span {...positions} data-excerpt-kind="inline-code">{children}</span>;
	if (insideArtifactLink) return name;
	const code = (
		<code {...positions} data-excerpt-kind="inline-code" className="rounded bg-surface px-[5px] py-[2px] font-mono text-xs text-markdown-code">
			{children}
		</code>
	);
	if (insideLink) return code;
	if (!file) {
		if (text?.includes("/") && openSession) return <ArtifactPathCode text={text} unlisted={code}>{name}</ArtifactPathCode>;
		const path = text ? explicitWorkspaceFilePath(text) : undefined;
		return path ? <ChatPathMenu path={path}>{code}</ChatPathMenu> : code;
	}
	const openFile = () => file.open(workspaceFileReferenceLine(text ?? ""));
	return (
		<ChatPathMenu path={normalizeWorkspaceFileReference(text ?? "")} onFileOpen={openFile}>
			<Pressable
				data-chat-find-include=""
				data-excerpt-include=""
				onClick={openFile}
				aria-label={`Open ${file.filePath} in Files`}
				className="inline rounded transition-colors hover:bg-interactive-hover"
			>
				{code}
			</Pressable>
		</ChatPathMenu>
	);
}

const COMPONENTS: Components = {
	span: ({ children, node: _node, ...props }) => <span {...props}>{props.className?.split(" ").includes("stream-char") ? children : compactEmoji(children)}</span>,
	// Headings step down in size but stay in the conversation's voice — an agent's
	// "## Findings" is a paragraph label, not a page title.
	h1: ({ children, node: _node, ...props }) => (
		<h3 {...props} className="mb-1.5 mt-4 text-subtitle font-semibold leading-snug text-foreground first:mt-0">
			{children}
		</h3>
	),
	h2: ({ children, node: _node, ...props }) => (
		<h4 {...props} className="mb-1.5 mt-3.5 text-sm font-semibold leading-snug text-foreground first:mt-0">
			{children}
		</h4>
	),
	h3: ({ children, node: _node, ...props }) => (
		<h5 {...props} className="mb-1 mt-3 text-sm font-semibold leading-snug text-foreground first:mt-0">
			{children}
		</h5>
	),
	h4: ({ children, node: _node, ...props }) => (
		<h6 {...props} className="mb-1 mt-3 text-xs font-semibold text-muted-foreground first:mt-0">
			{children}
		</h6>
	),

	// A paragraph of nothing but images is a set of pictures, not prose.
	p: ({ children, node, ...props }) =>
		isImageOnlyParagraph(node) ? (
			<ChatImageGallery>{children}</ChatImageGallery>
		) : (
			<p {...props} className="my-2 first:mt-0 last:mb-0">{compactEmoji(children)}</p>
		),

	ul: ({ children }) => <ul className="my-2 ml-4 list-disc space-y-1 first:mt-0">{children}</ul>,
	// Keep the source's first number: a reply of "6." is one item numbered 6, not 1.
	ol: ({ children, start }) => (
		<ol start={start} className="my-2 ml-4 list-decimal space-y-1 first:mt-0">{children}</ol>
	),
	li: ({ children, className, node: _node, ...props }) => (
		// A task-list item drops its bullet: the checkbox is the marker.
		<li {...props} className={cn("marker:text-muted-foreground", className?.includes("task-list-item") && "list-none")}>
			{compactEmoji(children)}
		</li>
	),
	// Read-only on purpose. The checkbox reflects what the agent wrote; clicking it
	// would imply AO could change the agent's plan, which it cannot.
	input: ({ checked, type }) =>
		type === "checkbox" ? (
			<input
				type="checkbox"
				checked={Boolean(checked)}
				readOnly
				aria-label={checked ? "done" : "not done"}
				className="mr-1.5 -ml-4 size-3 translate-y-[1px] accent-accent"
			/>
		) : null,

	// A fenced block arrives as `pre > code`, with the language — when the agent
	// gave one — as a class on the inner element. It is claimed here rather than in
	// `code` because a fence with no language carries no class at all, and matching
	// on the class alone rendered those as inline code.
	pre: ({ children, node }) => {
		const fence = fenceOf(children);
		if (!fence) return <CodeBlock code={textOf(children).replace(/\n$/, "")} sourceNode={node} />;
		// Mermaid is a diagram, not source: it renders through the sandboxed SVG
		// path rather than the highlighter. See `MermaidBlock.tsx`.
		if (fence.language?.toLowerCase() === "mermaid") return <div {...excerptElementProps(node)} data-excerpt-block="" data-excerpt-flowchart=""><MermaidFence code={fence.code} /></div>;
		// Display formulas (`$$…$$`, `\[…\]`) arrive as `language-math` fences too,
		// so one check covers them and explicit ```math fences.
		if (fence.language?.toLowerCase() === "math") return <div {...excerptElementProps(node)} data-excerpt-block=""><MathFormula tex={fence.code} display /></div>;
		return <CodeBlock code={fence.code} language={fence.language} sourceNode={node} />;
	},
	// Only inline code and inline formulas reach here; `pre` above takes every fence.
	code: InlineCode,

	// Wide tables scroll inside their own container so the conversation column
	// never scrolls sideways.
	table: ({ children }) => (
		<div className="my-2.5 overflow-x-auto rounded-lg border border-border">
			<table className="w-full border-collapse text-xs">{children}</table>
		</div>
	),
	thead: ({ children }) => <thead className="bg-raised/40">{children}</thead>,
	th: ({ children, node: _node, ...props }) => (
		<th {...props} className="border-b border-border px-2.5 py-1.5 text-left font-medium text-muted-foreground">
			{compactEmoji(children)}
		</th>
	),
	td: ({ children, node: _node, ...props }) => (
		<td {...props} className="border-b border-border/60 px-2.5 py-1.5 align-top">{compactEmoji(children)}</td>
	),

	blockquote: ({ children }) => (
		<blockquote className="my-2.5 border-l-2 border-border-strong pl-3 text-muted-foreground">
			{children}
		</blockquote>
	),
	hr: () => <hr className="my-3 border-border" />,
	strong: ({ children, node: _node, ...props }) => <strong {...props} className="font-semibold text-foreground">{children}</strong>,
	del: ({ children, node: _node, ...props }) => <del {...props} className="text-muted-foreground">{children}</del>,
	details: ({ children, node: _node, ...props }) => <details {...props} className="my-2 first:mt-0 last:mb-0">{children}</details>,
	summary: ({ children, node: _node, ...props }) => (
		<summary {...props} className="cursor-pointer text-muted-foreground transition-colors hover:text-foreground">{children}</summary>
	),

	// Match terminal links: a plain HTTP(S) click uses the session's AO Browser;
	// Cmd/Ctrl- or Option/Alt-click and non-web schemes use the system browser,
	// and right-click offers the system browser and copying the address.
	a: MarkdownLink,

	img: MarkdownImage,
};

/**
 * What markdown leaves literal in a person's own message. Pasted text keeps its
 * look: `# notes` is not a heading, four spaces are not a code block, a `---`
 * under a line is not a rule, and `<Component>` stays visible text. Images,
 * tables, footnotes, link definitions and strikethrough are out for the same
 * reason. The daemon's find splits these messages the same way
 * (`FindUserBlocks`), so change both together.
 */
const USER_LITERAL_CONSTRUCTS = [
	"headingAtx", "setextUnderline", "thematicBreak", "codeIndented", "htmlFlow", "htmlText",
	"definition", "labelStartImage", "table", "strikethrough",
	"gfmFootnoteDefinition", "gfmFootnoteCall", "gfmPotentialFootnoteCall",
];

function remarkUserSubset(this: Processor) {
	const data = this.data();
	(data.micromarkExtensions ??= []).push({ disable: { null: USER_LITERAL_CONSTRUCTS } });
}

type MarkdownParent = { children: RootContent[] };

/** A newline the person typed is a line break, as it was in the plain bubble. */
function splitSoftBreaks(parent: MarkdownParent) {
	parent.children = parent.children.flatMap((child): RootContent[] => {
		if ("children" in child) splitSoftBreaks(child as MarkdownParent);
		if (child.type !== "text" || !child.value.includes("\n")) return [child];
		return child.value.split("\n").flatMap((value, index): RootContent[] => [
			...(index > 0 ? [{ type: "break" } as const] : []),
			...(value ? [{ type: "text", value } as const] : []),
		]);
	});
}

function remarkSoftBreaks() {
	return (tree: Root) => splitSoftBreaks(tree);
}

const USER_PLUGINS: Options["remarkPlugins"] = [remarkGfm, remarkSessionLinks, remarkUserSubset, remarkSoftBreaks];

const UserTextRenderer = createContext<((text: string) => ReactNode) | undefined>(undefined);

/** Hands the message's words to the bubble's own text renderer (inline images, chips), inside lists and emphasis too. */
function UserProse({ children }: { children?: ReactNode }) {
	const render = useContext(UserTextRenderer);
	if (!render) return <>{children}</>;
	if (typeof children === "string") return <>{render(children)}</>;
	if (!Array.isArray(children)) return <>{children}</>;
	return <>{children.map((child, index) => typeof child === "string" ? <Fragment key={index}>{render(child)}</Fragment> : child)}</>;
}

const USER_COMPONENTS: Components = {
	...COMPONENTS,
	// Paragraphs the person split with a blank line keep a blank line's gap.
	p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0 [p+&]:mt-[1lh]"><UserProse>{children}</UserProse></p>,
	li: ({ children, className }) => (
		<li className={cn("marker:text-muted-foreground", className?.includes("task-list-item") && "list-none")}>
			<UserProse>{children}</UserProse>
		</li>
	),
	em: ({ children }) => <em><UserProse>{children}</UserProse></em>,
	strong: ({ children }) => <strong className="font-semibold"><UserProse>{children}</UserProse></strong>,
	// Every fence is code here, a ```mermaid one included: nothing in a person's
	// own message runs or renders as something else.
	pre: ({ children }) => {
		const fence = fenceOf(children);
		return fence ? <CodeBlock code={fence.code} language={fence.language} /> : <>{children}</>;
	},
};

/**
 * A person's own message with the light markdown people type: lists, task
 * boxes, bold and italic, inline and fenced code, links and quotes. Display
 * only: the stored text, the text sent to the agent and the Copy action all
 * stay the raw source. Links open only when clicked.
 */
export const UserMarkdown = memo(function UserMarkdown({ text, renderText }: {
	text: string;
	/** Renders each run of plain text, for inline images and chips; keep it stable. */
	renderText?: (text: string) => ReactNode;
}) {
	return (
		<UserTextRenderer.Provider value={renderText}>
			<Markdown remarkPlugins={USER_PLUGINS} components={USER_COMPONENTS} urlTransform={chatUrlTransform}>
				{text}
			</Markdown>
		</UserTextRenderer.Provider>
	);
});
