import { Code2, Download, ExternalLink, FilePlus, Globe2, Loader2, Maximize2 } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
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
	type RenderDisplayMode,
	type RenderTheme,
} from "../../lib/render-frame";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import type { ArtifactRef, RenderRef } from "../../types/conversation";
import { Button, type ButtonProps } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
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
 * top-level block and shows its scrollbar. In both modes the frame fits the
 * page's height; the expanded frame's class caps it at the window.
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
			style={{
				height: Math.min(
					clampRenderHeight(contentHeight ?? (heights && width ? measuredRenderHeight(heights, width) : page.height)),
					page.scrollable && displayMode === "inline" ? ARTIFACT_FRAME_MAX_HEIGHT : Number.POSITIVE_INFINITY,
				),
			}}
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

/** Opens an artifact in the session's Browser panel, on its own preview origin. */
function OpenInPanelAction({ sessionId, previewUrl, onOpen }: { sessionId: string; previewUrl: string; onOpen: () => void }) {
	const { t } = useTranslation();
	const openArtifactPreview = useOpenArtifactPreview(sessionId);
	return (
		<RenderAction
			label={t("chat.artifact.openInPanel")}
			onClick={() => {
				onOpen();
				openArtifactPreview(previewUrl);
			}}
		>
			<Globe2 className="size-3.5" />
		</RenderAction>
	);
}

/** A render, or an HTML artifact, inline in its turn and expandable to a dialog sized to the page. */
export function RenderFrame(props: { render: RenderRef } | { artifact: ArtifactRef }) {
	const { t } = useTranslation();
	const remoteHost = useChatRemoteHost();
	const render = "render" in props ? props.render : undefined;
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
	const [expanded, setExpanded] = useState(false);
	const [showSource, setShowSource] = useState(false);
	const [saving, setSaving] = useState(false);
	const [savingArtifact, setSavingArtifact] = useState(false);
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
	return (
		<div className="group/render relative min-w-0">
			<RenderDocument page={page} displayMode="inline" />
			<RenderAction
				label={t("chat.render.expand")}
				className="absolute end-1 top-1 opacity-0 transition-opacity group-hover/render:opacity-100 focus-visible:opacity-100"
				onClick={() => {
					setShowSource(false);
					setExpanded(true);
				}}
			>
				<Maximize2 className="size-3.5" />
			</RenderAction>
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
						{render ? (
							<RenderAction label={t("chat.render.saveAsArtifact")} disabled={savingArtifact} onClick={() => saveArtifact(render)}>
								<FilePlus className="size-3.5" />
							</RenderAction>
						) : null}
						{links?.previewUrl ? (
							<OpenInPanelAction sessionId={links.sessionId} previewUrl={links.previewUrl} onOpen={() => setExpanded(false)} />
						) : null}
						<RenderAction
							label={t("chat.render.openInBrowser")}
							onClick={() =>
								window.open(
									`${getApiBaseUrl()}${page.path}${renderThemeFragment(readRenderTheme(), "fullscreen")}`,
									"_blank",
									"noopener,noreferrer",
								)
							}
						>
							<ExternalLink className="size-3.5" />
						</RenderAction>
					</div>
					{!expanded ? null : showSource ? (
						<RenderSource path={page.path} />
					) : (
						<RenderDocument page={page} displayMode="fullscreen" className="max-h-[calc(100svh-8rem)]" />
					)}
				</DialogContent>
			</Dialog>
		</div>
	);
}
