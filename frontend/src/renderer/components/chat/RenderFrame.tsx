import { apiErrorException } from "../../lib/api-error-exception";
import { Code2, Download, ExternalLink, FilePlus, Loader2, Maximize2, WrapText } from "@/components/app-icons";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { apiClient, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import {
	clampRenderHeight,
	measuredRenderHeight,
	readRenderContentHeight,
	readRenderImageRequest,
	readRenderLinkRequest,
	readRenderTheme,
	renderFileName,
	renderThemeFragment,
	renderThemeMessage,
	renderThemesEqual,
	type RenderDisplayMode,
	type RenderTheme,
} from "../../lib/render-frame";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import type { RenderRef } from "../../types/conversation";
import { BrowserIcon } from "../icons";
import { Button, type ButtonProps } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { ChatImageDialog } from "./ChatImage";
import { useChatRemoteHost, useChatRemoteSession } from "./chat-image-source";
import { useChatBrowserOpen } from "./ChatMarkdown";

type RenderImage = { src: string; alt: string };

/** What a frame shows: a page the agent rendered. */
interface FramePage {
	title: string;
	/** Daemon-relative route the page is served from. */
	path: string;
	height: number;
	heights?: Array<[number, number]>;
	/** The file Save writes. */
	fileName: string;
	/** A remote host's render on its proxy's render origin; framed with the same sandbox as the daemon's route. */
	remoteUrl?: string;
}

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
 * top-level block and shows its scrollbar. In both modes the frame fits the
 * page's height; the expanded frame's class caps it at the window.
 */
function RenderDocument({
	page,
	displayMode,
	wrapContent,
	onOpenImage,
	className,
}: {
	page: FramePage;
	displayMode: RenderDisplayMode;
	wrapContent: boolean;
	onOpenImage: (image: RenderImage) => void;
	className?: string;
}) {
	const theme = useRenderTheme();
	const frameRef = useRef<HTMLIFrameElement>(null);
	const themeRef = useRef(theme);
	themeRef.current = theme;
	const wrapContentRef = useRef(wrapContent);
	wrapContentRef.current = wrapContent;
	const onOpenImageRef = useRef(onOpenImage);
	onOpenImageRef.current = onOpenImage;
	// The daemon can come back on another port, so the src follows the API base
	// (a frame not yet loaded would otherwise point at a dead port). Theme and
	// wrapping ride in the fragment only for the first paint; later changes are
	// posted, so they never reload the page.
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const src = useMemo(
		() => `${page.remoteUrl ?? `${baseUrl}${page.path}`}${renderThemeFragment(themeRef.current, displayMode, false, wrapContentRef.current)}`,
		[baseUrl, page.path, page.remoteUrl, displayMode],
	);
	const [contentHeight, setContentHeight] = useState<number>();
	// The frame's width picks its measured first height; read before the first
	// paint, so the frame opens at that height rather than the agent's.
	const [width, setWidth] = useState<number>();
	const { heights } = page;
	useLayoutEffect(() => {
		const frame = frameRef.current;
		if (!frame || !heights) return;
		setWidth(frame.getBoundingClientRect().width);
		const observer = new ResizeObserver(([entry]) => {
			if (entry) setWidth(entry.contentRect.width);
		});
		observer.observe(frame);
		return () => observer.disconnect();
	}, [heights]);
	const postTheme = () =>
		frameRef.current?.contentWindow?.postMessage(renderThemeMessage(themeRef.current, displayMode, false, wrapContent), "*");
	useEffect(() => {
		postTheme();
	}, [theme, wrapContent]);
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
			if (document.activeElement !== frame || navigator.userActivation?.isActive === false) return;
			const url = readRenderLinkRequest(event.data);
			if (url) window.open(url, "_blank", "noopener,noreferrer");
			const image = readRenderImageRequest(event.data);
			if (image) onOpenImageRef.current(image);
		};
		window.addEventListener("message", onMessage);
		return () => window.removeEventListener("message", onMessage);
	}, []);
	return (
		<iframe
			ref={frameRef}
			src={src}
			title={page.title}
			sandbox="allow-scripts allow-forms"
			loading="lazy"
			onLoad={postTheme}
			className={cn("block w-full border-0", className)}
			style={{ height: clampRenderHeight(contentHeight ?? (heights && width ? measuredRenderHeight(heights, width) : page.height)) }}
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

/** Where the render is served: its remote host's render origin, or this computer's daemon. */
function renderUrl(page: FramePage): string {
	return page.remoteUrl ?? `${getApiBaseUrl()}${page.path}`;
}

/** The render as the daemon serves it; `?source=1` is the page as the agent wrote it. */
async function fetchRender(url: string, signal?: AbortSignal): Promise<Response> {
	const response = await fetch(url, { signal });
	if (!response.ok) throw new Error(`render ${url}: HTTP ${response.status}`);
	return response;
}

/**
 * A remote host's render on its proxy's render origin. The proxy URL itself is
 * not framed: its path carries the proxy's token, which the page could read.
 * undefined while it is looked up, null when the host cannot serve it.
 */
function useRemoteRenderUrl(path: string | undefined): string | null | undefined {
	const remote = useChatRemoteSession();
	const hostId = remote?.hostId;
	const sessionId = remote?.sessionId;
	// The proxy's base changes when it reconnects, and with it the render's origin.
	const baseUrl = remote?.baseUrl;
	// A result counts only for the inputs that produced it.
	const key = JSON.stringify([hostId, sessionId, baseUrl, path]);
	const [lookup, setLookup] = useState<{ key: string; url: string | null }>();
	useEffect(() => {
		if (!sessionId || !path || !hostId || !baseUrl) return;
		let live = true;
		aoBridge.remotes.previewUrl(hostId, sessionId, path).then(
			(next) => {
				if (live) setLookup({ key, url: /^http:\/\//.test(next) ? next : null });
			},
			(error: unknown) => {
				console.error("remote render url", error);
				if (live) setLookup({ key, url: null });
			},
		);
		return () => {
			live = false;
		};
	}, [hostId, sessionId, baseUrl, path, key]);
	if (!sessionId || !path) return undefined;
	if (!hostId || !baseUrl) return null;
	return lookup?.key === key ? lookup.url : undefined;
}

/** The served page, bootstrap included, so the saved file renders on its own. */
async function saveRender(page: FramePage) {
	const url = URL.createObjectURL(await (await fetchRender(renderUrl(page))).blob());
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
	if (!data) throw apiErrorException(error, "save render as artifact failed");
	return data.name;
}

/** The page's HTML as plain text. No highlighting: a page can run to 25 MiB. */
function RenderSource({ url }: { url: string }) {
	const { t } = useTranslation();
	// undefined while loading, null when the fetch failed.
	const [source, setSource] = useState<string | null>();
	useEffect(() => {
		const controller = new AbortController();
		fetchRender(`${url}?source=1`, controller.signal)
			.then((response) => response.text())
			.then(setSource, () => {
				if (!controller.signal.aborted) setSource(null);
			});
		return () => controller.abort();
	}, [url]);
	if (source === undefined) {
		return (
			<div aria-busy="true" className="flex h-20 items-center justify-center">
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
	return (
		<pre className="max-h-[calc(100svh-8rem)] w-full overflow-auto px-2 font-mono text-xs whitespace-pre select-text">{source}</pre>
	);
}

/**
 * A render inline in its turn and expandable to a dialog sized to the page.
 * A reported artifact is not framed in the thread: see ReportedArtifactRow.
 */
export function RenderFrame({ render }: { render: RenderRef }) {
	const { t } = useTranslation();
	const remoteHost = useChatRemoteHost();
	const remoteUrl = useRemoteRenderUrl(render.path);
	const page: FramePage = { title: render.title, path: render.path, height: render.height, heights: render.heights, fileName: renderFileName(render.title), remoteUrl: remoteUrl ?? undefined };
	const [expanded, setExpanded] = useState(false);
	const [wrapContent, setWrapContent] = useState(false);
	const [showSource, setShowSource] = useState(false);
	const [saving, setSaving] = useState(false);
	const [savingArtifact, setSavingArtifact] = useState(false);
	const [image, setImage] = useState<RenderImage>();
	const [imageOpen, setImageOpen] = useState(false);
	const openImage = (next: RenderImage) => {
		setImage(next);
		setImageOpen(true);
	};
	const openInBrowserPanel = useChatBrowserOpen();
	if (remoteHost && remoteUrl === null) {
		return (
			<p className="text-xs text-muted-foreground">
				{page.title} · {t("chat.render.remoteHost")}
			</p>
		);
	}
	if (remoteHost && !remoteUrl) return <div aria-busy="true" style={{ height: clampRenderHeight(page.height) }} />;
	const save = () => {
		setSaving(true);
		saveRender(page)
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
	const wrapAction = (
		<RenderAction
			label={t("chat.render.wrapContent")}
			aria-pressed={wrapContent}
			className="aria-pressed:bg-muted"
			onClick={() => setWrapContent((current) => !current)}
		>
			<WrapText aria-hidden="true" className="size-3.5" />
		</RenderAction>
	);
	// The page on its own, as a tab opens it: from its render route, whose
	// sandbox CSP keeps it off the daemon wherever it is opened. A remote host's
	// render opens only in its frame here.
	const opensStandalone = !remoteHost;
	const openStandalone = (open: (url: string) => void) =>
		open(`${getApiBaseUrl()}${page.path}${renderThemeFragment(readRenderTheme(), "fullscreen", false, wrapContent)}`);
	const browserPanelAction = openInBrowserPanel && opensStandalone ? (
		<RenderAction
			label={t("inspector.openInAOBrowser")}
			onClick={() => {
				setExpanded(false);
				openStandalone(openInBrowserPanel);
			}}
		>
			<BrowserIcon aria-hidden="true" className="size-3.5" />
		</RenderAction>
	) : null;
	return (
		<div className="group/render relative min-w-0">
			<RenderDocument page={page} displayMode="inline" wrapContent={wrapContent} onOpenImage={openImage} />
			<div className="absolute end-1 top-1 flex gap-1 opacity-0 transition-opacity group-hover/render:opacity-100 focus-within:opacity-100">
				{wrapAction}
				{browserPanelAction}
				<RenderAction
					label={t("chat.render.expand")}
					onClick={() => {
						setShowSource(false);
						setExpanded(true);
					}}
				>
					<Maximize2 className="size-3.5" />
				</RenderAction>
			</div>
			<Dialog open={expanded} onOpenChange={setExpanded}>
				<DialogContent
					aria-describedby={undefined}
					// A reading column wide and as tall as the page, up to the window;
					// borderless, like the inline frame.
					className="z-overlay w-[min(64rem,calc(100vw-4rem))] max-w-none gap-2 border-0 p-2 outline-none"
					// Focus the dialog, not its first action: a focused action opens its tooltip.
					onOpenAutoFocus={(event) => {
						event.preventDefault();
						(event.currentTarget as HTMLElement).focus();
					}}
				>
					{/* pe-9 keeps the actions clear of the dialog's own close button. */}
					<div className="flex h-8 shrink-0 items-center gap-1 ps-2 pe-9">
						<DialogTitle className="min-w-0 flex-1 truncate text-subtitle">{page.title}</DialogTitle>
						{wrapAction}
						<RenderAction
							label={t("chat.render.viewSource")}
							aria-pressed={showSource}
							className="aria-pressed:bg-muted"
							onClick={() => setShowSource((current) => !current)}
						>
							<Code2 className="size-3.5" />
						</RenderAction>
						<RenderAction
							label={t("chat.render.save")}
							disabled={saving}
							onClick={save}
						>
							<Download className="size-3.5" />
						</RenderAction>
						{!remoteHost ? (
							<RenderAction label={t("chat.render.saveAsArtifact")} disabled={savingArtifact} onClick={() => saveArtifact(render)}>
								<FilePlus className="size-3.5" />
							</RenderAction>
						) : null}
						{browserPanelAction}
						{opensStandalone ? (
							<RenderAction
								label={t("chat.render.openInBrowser")}
								onClick={() => openStandalone((url) => window.open(url, "_blank", "noopener,noreferrer"))}
							>
								<ExternalLink className="size-3.5" />
							</RenderAction>
						) : null}
					</div>
					{!expanded ? null : showSource ? (
						<RenderSource url={renderUrl(page)} />
					) : (
						<RenderDocument page={page} displayMode="fullscreen" wrapContent={wrapContent} onOpenImage={openImage} className="max-h-[calc(100svh-8rem)]" />
					)}
				</DialogContent>
			</Dialog>
			{image ? <ChatImageDialog open={imageOpen} onOpenChange={setImageOpen} url={image.src} label={image.alt} /> : null}
		</div>
	);
}
