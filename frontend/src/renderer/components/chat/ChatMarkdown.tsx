import type { Element as HastElement } from "hast";
import { rehypeExcerptSource, excerptElementProps } from "../../lib/rehype-excerpt-source";
import { pageHidden } from "../../lib/page-hidden";

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

const PLUGINS = [remarkGfm, remarkSessionLinks];
const MATH_PLUGINS = [...PLUGINS, remarkMath];
const prefersReducedMotion = () => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
const FADE_SHED_MS = 450;

const StreamingProse = createContext(false);
const InsideMarkdownLink = createContext(false);
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
const ChatLinkWorkspaces = createContext<{ workspaces: WorkspaceSummary[]; demand?: () => void }>({ workspaces: EMPTY_LINK_WORKSPACES });

function ChatLinkWorkspacesSource({ hostId, kind, onWorkspaces }: { hostId?: string; kind?: "cloud"; onWorkspaces: (workspaces: WorkspaceSummary[]) => void }) {
	const { workspaces } = useSessionLinkSource(hostId, kind);
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
	if (isAoLink(url)) return url;
	if (key === "href" && isPotentialWorkspaceFileLink(url)) return url;
	return defaultUrlTransform(url);
}

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
	muted?: boolean;
	className?: string;
	safeOrigin?: boolean;
	remarkPlugins?: Options["remarkPlugins"];
	rehypePlugins?: Options["rehypePlugins"];
}) {
	const [fading, setFading] = useState(streaming);
	if (streaming && !fading) setFading(true);
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
	const [parsed, setParsed] = useState(() => streaming ? splitStreamingMarkdown(text, undefined, renderMath) : {
		source: text, math: renderMath, blocks: [{ source: text, start: 0, repairInline: false }], stableCount: 0,
	});
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

let preferredWrap = false;

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
	const safeOriginContent = useContext(SafeOriginContent);
	const { openSession } = useContext(OpenChatLink);
	return href && !safeOriginContent && openSession && !isWebLink(href) && !isAoLink(href) && hasArtifactDirectorySegment(href)
		? <ArtifactFileLink {...props} href={href} />
		: <MarkdownLinkTarget {...props} />;
}

function ArtifactFileLink(props: MarkdownLinkProps & { href: string }) {
	const artifact = artifactLinkForTarget(props.href, useChatLinkWorkspaces());
	return artifact
		? <MarkdownLinkTarget {...props} href={artifact} menuPath={props.menuPath ?? explicitWorkspaceFilePath(props.href)} />
		: <MarkdownLinkTarget {...props} />;
}

function linksImage(node: HastElement | undefined): boolean {
	return node?.children.some((child) => child.type === "element" && child.tagName === "img") ?? false;
}

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

function useWorkspaceFile(path: string | undefined) {
	const { openFile, findFile } = useContext(OpenChatLink);
	const filePath = path && openFile ? findFile(path) : undefined;
	if (!path || !filePath || !openFile) return undefined;
	const target = workspaceFileOpenTarget(path, filePath);
	return { filePath, open: (line?: number) => (line == null ? openFile(target) : openFile(target, line)) };
}

export function FileReference({ path, line, className, children }: { path: string; line?: number; className?: string; children: ReactNode }) {
	const file = useWorkspaceFile(path);
	if (!file) return <span className={className} title={path}>{children}</span>;
	return (
		<Pressable data-chat-find-include="" className={className} title={path} onClick={() => file.open(line)}>
			{children}
		</Pressable>
	);
}

export function useChatBrowserOpen() {
	return useContext(OpenChatLink).open;
}

export function useChatLinkWorkspaces(): WorkspaceSummary[] {
	const { workspaces, demand } = useContext(ChatLinkWorkspaces);
	useLayoutEffect(() => demand?.(), [demand]);
	return workspaces;
}

export function ArtifactPathCode({ text, children, unlisted = children }: { text: string; children: ReactNode; unlisted?: ReactNode }) {
	const workspaces = useChatLinkWorkspaces();
	const href = artifactLinkForPath(text, workspaces);
	const path = explicitWorkspaceFilePath(text);
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

	p: ({ children, node, ...props }) =>
		isImageOnlyParagraph(node) ? (
			<ChatImageGallery>{children}</ChatImageGallery>
		) : (
			<p {...props} className="my-2 first:mt-0 last:mb-0">{compactEmoji(children)}</p>
		),

	ul: ({ children }) => <ul className="my-2 ml-4 list-disc space-y-1 first:mt-0">{children}</ul>,
	ol: ({ children, start }) => (
		<ol start={start} className="my-2 ml-4 list-decimal space-y-1 first:mt-0">{children}</ol>
	),
	li: ({ children, className, node: _node, ...props }) => (
		<li {...props} className={cn("marker:text-muted-foreground", className?.includes("task-list-item") && "list-none")}>
			{compactEmoji(children)}
		</li>
	),
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

	pre: ({ children, node }) => {
		const fence = fenceOf(children);
		if (!fence) return <CodeBlock code={textOf(children).replace(/\n$/, "")} sourceNode={node} />;
		if (fence.language?.toLowerCase() === "mermaid") return <div {...excerptElementProps(node)} data-excerpt-block="" data-excerpt-flowchart=""><MermaidFence code={fence.code} /></div>;
		if (fence.language?.toLowerCase() === "math") return <div {...excerptElementProps(node)} data-excerpt-block=""><MathFormula tex={fence.code} display /></div>;
		return <CodeBlock code={fence.code} language={fence.language} sourceNode={node} />;
	},
	code: InlineCode,

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

	a: MarkdownLink,

	img: MarkdownImage,
};

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

function UserProse({ children }: { children?: ReactNode }) {
	const render = useContext(UserTextRenderer);
	if (!render) return <>{children}</>;
	if (typeof children === "string") return <>{render(children)}</>;
	if (!Array.isArray(children)) return <>{children}</>;
	return <>{children.map((child, index) => typeof child === "string" ? <Fragment key={index}>{render(child)}</Fragment> : child)}</>;
}

const USER_COMPONENTS: Components = {
	...COMPONENTS,
	p: ({ children }) => <p className="my-2 first:mt-0 last:mb-0 [p+&]:mt-[1lh]"><UserProse>{children}</UserProse></p>,
	li: ({ children, className }) => (
		<li className={cn("marker:text-muted-foreground", className?.includes("task-list-item") && "list-none")}>
			<UserProse>{children}</UserProse>
		</li>
	),
	em: ({ children }) => <em><UserProse>{children}</UserProse></em>,
	strong: ({ children }) => <strong className="font-semibold"><UserProse>{children}</UserProse></strong>,
	pre: ({ children }) => {
		const fence = fenceOf(children);
		return fence ? <CodeBlock code={fence.code} language={fence.language} /> : <>{children}</>;
	},
};

export const UserMarkdown = memo(function UserMarkdown({ text, renderText }: {
	text: string;
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
