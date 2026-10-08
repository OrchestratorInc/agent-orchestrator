import { Code2, Download, ExternalLink, FilePlus, Globe2, Loader2, PanelRightOpen, X } from "lucide-react";
import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { useOpenArtifactPreview } from "../../hooks/useOpenArtifactPreview";
import { apiClient, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import {
	clampRenderHeight,
	measuredRenderHeight,
	readRenderContentHeight,
	readRenderLinkRequest,
	readRenderTheme,
	renderFileName,
	renderThemeFragment,
	renderThemeMessage,
	renderThemesEqual,
	type PanelPage,
	type RenderDisplayMode,
	type RenderTheme,
} from "../../lib/render-frame";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import type { ArtifactRef, RenderRef } from "../../types/conversation";
import { Button, type ButtonProps } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { useChatArtifactLinks, useChatRemoteHost } from "./chat-image-source";

/** What a frame shows: a page the agent rendered, or an HTML artifact it reported. */
interface FramePage {
	title: string;
	/** Daemon-relative route the page is served from. */
	path: string;
	height: number;
	heights?: Array<[number, number]>;
	/** The file Save writes. */
	fileName: string;
	/**
	 * The page on its own inline-artifact origin. When set, the frame loads it
	 * from there with allow-same-origin, so its module scripts, fetches and
	 * fonts work; the daemon refuses that origin, and it is not the app's.
	 */
	frameUrl?: string;
	/**
	 * Caps the inline frame and scrolls the page inside it. An artifact is an
	 * ordinary document, not written to the render rules, so a page sized to
	 * its viewport would otherwise grow the frame each time it reported.
	 */
	scrollable?: boolean;
}

// An artifact is never measured: its frame opens at this height, then fits the
// page up to ARTIFACT_FRAME_MAX_HEIGHT, past which it scrolls inside the frame.
const ARTIFACT_FRAME_HEIGHT = 400;
const ARTIFACT_FRAME_MAX_HEIGHT = 640;

/** The app theme as handed to renders; follows data-theme and data-style-theme flips on <html>. */
function useRenderTheme(): RenderTheme {
	const [theme, setTheme] = useState(readRenderTheme);
	useEffect(() => {
		const observer = new MutationObserver(() =>
			setTheme((current) => {
				const next = readRenderTheme();
				return renderThemesEqual(current, next) ? current : next;
			}),
		);
		// Not `style`: only sidebar/zoom geometry writes it, on every animation tick.
		observer.observe(document.documentElement, {
			attributes: true,
			attributeFilter: ["data-theme", "data-style-theme", "class"],
		});
		return () => observer.disconnect();
	}, []);
	return theme;
}

/**
 * An agent's HTML page inline in its turn. The page runs in an opaque-origin
 * sandbox (no allow-same-origin), so it cannot reach the app's session,
 * storage, or the daemon. It reads the theme from its URL fragment before
 * first paint and restyles from posted messages after, so the src never changes.
 * Both carry the display mode; in fullscreen the page centers a width-capped
 * top-level block and shows its scrollbar.
 */
function RenderDocument({
	page,
	displayMode,
	className,
}: {
	page: FramePage;
	displayMode: RenderDisplayMode;
	className?: string;
}) {
	const theme = useRenderTheme();
	const frameRef = useRef<HTMLIFrameElement>(null);
	const themeRef = useRef(theme);
	themeRef.current = theme;
	// The daemon can come back on another port, so the src follows the API base
	// (a frame not yet loaded would otherwise point at a dead port). The theme
	// rides in the fragment only for the first paint; later flips are posted, so
	// they never reload the page.
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const src = useMemo(
		() => `${page.frameUrl ?? `${baseUrl}${page.path}`}${renderThemeFragment(themeRef.current, displayMode, page.scrollable)}`,
		[baseUrl, page.path, page.frameUrl, displayMode, page.scrollable],
	);
	const [contentHeight, setContentHeight] = useState<number>();
	// The inline frame's width picks its measured first height; read before the
	// first paint, so the frame opens at that height rather than the agent's.
	const [width, setWidth] = useState<number>();
	const { heights } = page;
	useLayoutEffect(() => {
		const frame = frameRef.current;
		if (!frame || displayMode !== "inline" || !heights) return;
		setWidth(frame.getBoundingClientRect().width);
		const observer = new ResizeObserver(([entry]) => {
			if (entry) setWidth(entry.contentRect.width);
		});
		observer.observe(frame);
		return () => observer.disconnect();
	}, [displayMode, heights]);
	const postTheme = () =>
		frameRef.current?.contentWindow?.postMessage(renderThemeMessage(themeRef.current, displayMode, page.scrollable), "*");
	useEffect(() => {
		postTheme();
	}, [theme]);
	// Layout effect: a fast page can post its height before a passive effect runs.
	useLayoutEffect(() => {
		const onMessage = (event: MessageEvent) => {
			const frame = frameRef.current;
			if (!frame || event.source !== frame.contentWindow) return;
			const height = readRenderContentHeight(event.data);
			if (height !== undefined) {
				setContentHeight(height);
				return;
			}
			// Only while the reader is using this frame: a page can post on load.
			const url = readRenderLinkRequest(event.data);
			if (url && document.activeElement === frame && navigator.userActivation?.isActive !== false) {
				window.open(url, "_blank", "noopener,noreferrer");
			}
		};
		window.addEventListener("message", onMessage);
		return () => window.removeEventListener("message", onMessage);
	}, []);
	return (
		<iframe
			ref={frameRef}
			src={src}
			title={page.title}
			sandbox={page.frameUrl ? "allow-scripts allow-forms allow-same-origin" : "allow-scripts allow-forms"}
			loading="lazy"
			onLoad={postTheme}
			className={cn("block w-full border-0", className)}
			style={
				displayMode === "inline"
					? {
							height: Math.min(
								clampRenderHeight(contentHeight ?? (heights && width ? measuredRenderHeight(heights, width) : page.height)),
								page.scrollable ? ARTIFACT_FRAME_MAX_HEIGHT : Number.POSITIVE_INFINITY,
							),
						}
					: undefined
			}
		/>
	);
}

/** An icon-only ghost button, named by its tooltip. */
function RenderAction({ label, children, ...props }: ButtonProps & { label: string }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button variant="ghost" size="icon-sm" aria-label={label} {...props}>
					{children}
				</Button>
			</TooltipTrigger>
			<TooltipContent>{label}</TooltipContent>
		</Tooltip>
	);
}

/** The render as the daemon serves it; `?source=1` is the page as the agent wrote it. */
async function fetchRender(path: string, signal?: AbortSignal): Promise<Response> {
	const response = await fetch(`${getApiBaseUrl()}${path}`, { signal });
	if (!response.ok) throw new Error(`render ${path}: HTTP ${response.status}`);
	return response;
}

/** The served page, bootstrap included, so the saved file renders on its own. */
async function saveRender(page: FramePage) {
	const url = URL.createObjectURL(await (await fetchRender(page.path)).blob());
	const link = document.createElement("a");
	link.href = url;
	link.download = page.fileName;
	link.click();
	setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** Keeps the page as a session artifact; resolves to the saved file's name. */
async function saveRenderAsArtifact(render: RenderRef): Promise<string> {
	// readRenderRef only accepts /api/v1/sessions/{sessionId}/renders/{renderId}.
	const sessionId = decodeURIComponent(render.path.split("/")[4] ?? "");
	const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/renders/{renderId}/artifact", {
		params: { path: { sessionId, renderId: render.id } },
		body: { title: render.title },
	});
	if (!data) throw new Error(apiErrorMessage(error, "save render as artifact failed"));
	return data.name;
}

/** The page's HTML as plain text. No highlighting: a page can run to 25 MiB. */
function RenderSource({ path }: { path: string }) {
	const { t } = useTranslation();
	// undefined while loading, null when the fetch failed.
	const [source, setSource] = useState<string | null>();
	useEffect(() => {
		const controller = new AbortController();
		fetchRender(`${path}?source=1`, controller.signal)
			.then((response) => response.text())
			.then(setSource, () => {
				if (!controller.signal.aborted) setSource(null);
			});
		return () => controller.abort();
	}, [path]);
	if (source === undefined) {
		return (
			<div aria-busy="true" className="flex min-h-0 flex-1 items-center justify-center">
				<Loader2 aria-hidden="true" className="size-4 animate-spin text-muted-foreground" />
			</div>
		);
	}
	if (source === null) {
		return (
			<p role="alert" className="px-2 text-xs text-destructive">
				{t("chat.render.sourceError")}
			</p>
		);
	}
	return <pre className="min-h-0 w-full flex-1 overflow-auto px-2 font-mono text-xs whitespace-pre select-text">{source}</pre>;
}

/** Opens an artifact in the session's Browser panel, on its own preview origin. */
function OpenInPanelAction({ sessionId, previewUrl }: { sessionId: string; previewUrl: string }) {
	const { t } = useTranslation();
	const openArtifactPreview = useOpenArtifactPreview(sessionId);
	return (
		<RenderAction label={t("chat.artifact.openInPanel")} onClick={() => openArtifactPreview(previewUrl)}>
			<Globe2 className="size-3.5" />
		</RenderAction>
	);
}

/**
 * Opens a page beside the chat, in the session's inspector. The session view
 * provides it; a chat with no inspector around it opens the page in the
 * system browser instead.
 */
export const RenderPanelContext = createContext<((page: PanelPage) => void) | null>(null);

/** The page as the system browser should open it: the daemon route, themed, full width. */
function externalPageUrl(page: Pick<PanelPage, "path">): string {
	return `${getApiBaseUrl()}${page.path}${renderThemeFragment(readRenderTheme(), "fullscreen")}`;
}

/**
 * A render or an HTML artifact open beside the chat, in the inspector's Page
 * view: the page fills the panel, and its actions sit in a header row above it.
 */
export function RenderPagePanel({ page, onClose }: { page: PanelPage; onClose: () => void }) {
	const { t } = useTranslation();
	const [showSource, setShowSource] = useState(false);
	const [saving, setSaving] = useState(false);
	const [savingArtifact, setSavingArtifact] = useState(false);
	const framePage: FramePage = { title: page.title, path: page.path, height: ARTIFACT_FRAME_HEIGHT, fileName: page.fileName, frameUrl: page.frameUrl };
	const save = () => {
		setSaving(true);
		saveRender(framePage)
			.catch((error: unknown) => {
				console.error("save render", error);
				useUiStore.getState().showGlobalToast(t("chat.render.saveError"), undefined, "error");
			})
			.finally(() => setSaving(false));
	};
	const saveArtifact = (target: RenderRef) => {
		setSavingArtifact(true);
		saveRenderAsArtifact(target)
			.then((name) => useUiStore.getState().showGlobalToast(t("chat.render.savedAsArtifact", { name })))
			.catch((error: unknown) => {
				console.error("save render as artifact", error);
				useUiStore.getState().showGlobalToast(t("chat.render.saveArtifactError"), undefined, "error");
			})
			.finally(() => setSavingArtifact(false));
	};
	return (
		<section aria-label={page.title} className="flex min-h-0 flex-1 flex-col">
			<div className="flex h-9 shrink-0 items-center gap-1 border-b border-border px-2">
				<h2 className="min-w-0 flex-1 truncate text-sm font-medium">{page.title}</h2>
				<RenderAction
					label={t("chat.render.viewSource")}
					aria-pressed={showSource}
					className="aria-pressed:bg-muted"
					onClick={() => setShowSource((current) => !current)}
				>
					<Code2 className="size-3.5" />
				</RenderAction>
				<RenderAction label={t("chat.render.save")} disabled={saving} onClick={save}>
					<Download className="size-3.5" />
				</RenderAction>
				{page.render ? (
					<RenderAction label={t("chat.render.saveAsArtifact")} disabled={savingArtifact} onClick={() => saveArtifact(page.render!)}>
						<FilePlus className="size-3.5" />
					</RenderAction>
				) : null}
				{page.browserPanel ? <OpenInPanelAction {...page.browserPanel} /> : null}
				<RenderAction label={t("chat.render.openInBrowser")} onClick={() => window.open(externalPageUrl(page), "_blank", "noopener,noreferrer")}>
					<ExternalLink className="size-3.5" />
				</RenderAction>
				<RenderAction label={t("chat.render.closePage")} onClick={onClose}>
					<X className="size-3.5" />
				</RenderAction>
			</div>
			{showSource ? (
				<RenderSource path={page.path} />
			) : (
				// Inset to the header's title, so the page lines up with it in the panel.
				<div className="flex min-h-0 flex-1 px-2">
					<RenderDocument key={page.key} page={framePage} displayMode="fullscreen" className="min-h-0 w-full flex-1" />
				</div>
			)}
		</section>
	);
}

/** A render, or an HTML artifact, inline in its turn; it opens beside the chat for a closer look. */
export function RenderFrame(props: { render: RenderRef } | { artifact: ArtifactRef }) {
	const { t } = useTranslation();
	const remoteHost = useChatRemoteHost();
	const openBeside = useContext(RenderPanelContext);
	const links = useChatArtifactLinks("artifact" in props ? props.artifact.path : undefined);
	const page: FramePage =
		"render" in props
			? { title: props.render.title, path: props.render.path, height: props.render.height, heights: props.render.heights, fileName: renderFileName(props.render.title) }
			: {
					title: props.artifact.name,
					path: props.artifact.url,
					height: ARTIFACT_FRAME_HEIGHT,
					fileName: props.artifact.name,
					scrollable: true,
					frameUrl: links?.inlineUrl,
				};
	// The local daemon has no copy of a remote host's render, and the remote
	// proxy URL must not reach the page: its path carries the proxy's capability
	// token, which the page could read from its own location.
	if (remoteHost) {
		return (
			<p className="text-xs text-muted-foreground">
				{page.title} · {t("chat.render.remoteHost")}
			</p>
		);
	}
	const panelPage: PanelPage = {
		key: "render" in props ? `render:${props.render.id}` : `artifact:${props.artifact.path}`,
		title: page.title,
		path: page.path,
		fileName: page.fileName,
		frameUrl: page.frameUrl,
		render: "render" in props ? props.render : undefined,
		browserPanel: links?.previewUrl ? { sessionId: links.sessionId, previewUrl: links.previewUrl } : undefined,
	};
	return (
		<div className="group/render relative min-w-0">
			<RenderDocument page={page} displayMode="inline" />
			<RenderAction
				label={t("chat.render.openBeside")}
				className="absolute end-1 top-1 opacity-0 transition-opacity group-hover/render:opacity-100 focus-visible:opacity-100"
				onClick={() => (openBeside ? openBeside(panelPage) : window.open(externalPageUrl(page), "_blank", "noopener,noreferrer"))}
			>
				<PanelRightOpen className="size-3.5" />
			</RenderAction>
		</div>
	);
}
