import { Code2, Download, ExternalLink, Loader2, Maximize2 } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getApiBaseUrl } from "../../lib/api-client";
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
import type { RenderRef } from "../../types/conversation";
import { Button, type ButtonProps } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { useChatRemoteHost } from "./chat-image-source";

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
	render,
	displayMode,
	className,
}: {
	render: RenderRef;
	displayMode: RenderDisplayMode;
	className?: string;
}) {
	const theme = useRenderTheme();
	const frameRef = useRef<HTMLIFrameElement>(null);
	const themeRef = useRef(theme);
	themeRef.current = theme;
	const [src] = useState(() => `${getApiBaseUrl()}${render.path}${renderThemeFragment(theme, displayMode)}`);
	const [contentHeight, setContentHeight] = useState<number>();
	// The inline frame's width picks its measured first height; read before the
	// first paint, so the frame opens at that height rather than the agent's.
	const [width, setWidth] = useState<number>();
	const { heights } = render;
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
	const postTheme = () => frameRef.current?.contentWindow?.postMessage(renderThemeMessage(themeRef.current, displayMode), "*");
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
			title={render.title}
			sandbox="allow-scripts allow-forms"
			loading="lazy"
			onLoad={postTheme}
			className={cn("block w-full border-0", className)}
			style={
				displayMode === "inline"
					? { height: clampRenderHeight(contentHeight ?? (heights && width ? measuredRenderHeight(heights, width) : render.height)) }
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
async function saveRender(render: RenderRef) {
	const url = URL.createObjectURL(await (await fetchRender(render.path)).blob());
	const link = document.createElement("a");
	link.href = url;
	link.download = renderFileName(render.title);
	link.click();
	setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** The page's HTML as plain text. No highlighting: a page can run to 25 MiB. */
function RenderSource({ render }: { render: RenderRef }) {
	const { t } = useTranslation();
	// undefined while loading, null when the fetch failed.
	const [source, setSource] = useState<string | null>();
	useEffect(() => {
		const controller = new AbortController();
		fetchRender(`${render.path}?source=1`, controller.signal)
			.then((response) => response.text())
			.then(setSource, () => {
				if (!controller.signal.aborted) setSource(null);
			});
		return () => controller.abort();
	}, [render.path]);
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

export function RenderFrame({ render }: { render: RenderRef }) {
	const { t } = useTranslation();
	const remoteHost = useChatRemoteHost();
	const [expanded, setExpanded] = useState(false);
	const [showSource, setShowSource] = useState(false);
	const [saving, setSaving] = useState(false);
	// The local daemon has no copy of a remote host's render, and the remote
	// proxy URL must not reach the page: its path carries the proxy's capability
	// token, which the page could read from its own location.
	if (remoteHost) {
		return (
			<p className="text-xs text-muted-foreground">
				{render.title} · {t("chat.render.remoteHost")}
			</p>
		);
	}
	const save = () => {
		setSaving(true);
		saveRender(render)
			.catch((error: unknown) => {
				console.error("save render", error);
				useUiStore.getState().showGlobalToast(t("chat.render.saveError"), undefined, "error");
			})
			.finally(() => setSaving(false));
	};
	return (
		<div className="group/render relative min-w-0">
			<RenderDocument render={render} displayMode="inline" />
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
					className="z-overlay flex h-[calc(100svh-6rem)] w-[calc(100vw-6rem)] max-w-none flex-col gap-2 p-2 outline-none"
					// Focus the dialog, not its first action: a focused action opens its tooltip.
					onOpenAutoFocus={(event) => {
						event.preventDefault();
						(event.currentTarget as HTMLElement).focus();
					}}
				>
					{/* pe-9 keeps the actions clear of the dialog's own close button. */}
					<div className="flex h-8 shrink-0 items-center gap-1 ps-2 pe-9">
						<DialogTitle className="min-w-0 flex-1 truncate text-subtitle">{render.title}</DialogTitle>
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
						<RenderAction
							label={t("chat.render.openInBrowser")}
							onClick={() =>
								window.open(
									`${getApiBaseUrl()}${render.path}${renderThemeFragment(readRenderTheme(), "fullscreen")}`,
									"_blank",
									"noopener,noreferrer",
								)
							}
						>
							<ExternalLink className="size-3.5" />
						</RenderAction>
					</div>
					{!expanded ? null : showSource ? (
						<RenderSource render={render} />
					) : (
						<RenderDocument render={render} displayMode="fullscreen" className="min-h-0 w-full flex-1" />
					)}
				</DialogContent>
			</Dialog>
		</div>
	);
}
