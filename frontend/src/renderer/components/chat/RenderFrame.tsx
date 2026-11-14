import { apiErrorException } from "../../lib/api-error-exception";
import { Code2, Download, ExternalLink, FilePlus, FileText, Loader2, Maximize2, WrapText } from "@/components/app-icons";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { apiClient, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
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
import type { ArtifactRef, RenderRef } from "../../types/conversation";
import { BrowserIcon } from "../icons";
import { Button, type ButtonProps } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { ChatImageDialog } from "./ChatImage";
import { useChatArtifactLinks, useChatRemoteHost } from "./chat-image-source";
import { useChatBrowserOpen } from "./ChatMarkdown";

type RenderImage = { src: string; alt: string };

interface FramePage {
	title: string;
	path: string;
	height: number;
	heights?: Array<[number, number]>;
	fileName: string;
	frameUrl?: string;
	scrollable?: boolean;
}

const ARTIFACT_FRAME_HEIGHT = 400;
const ARTIFACT_FRAME_MAX_HEIGHT = 640;

function useRenderTheme(): RenderTheme {
	const [theme, setTheme] = useState(readRenderTheme);
	useEffect(() => {
		const observer = new MutationObserver(() =>
			setTheme((current) => {
				const next = readRenderTheme();
				return renderThemesEqual(current, next) ? current : next;
			}),
		);
		observer.observe(document.documentElement, {
			attributes: true,
			attributeFilter: ["data-theme", "data-style-theme", "class"],
		});
		return () => observer.disconnect();
	}, []);
	return theme;
}

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
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const src = useMemo(
		() => `${page.frameUrl ?? `${baseUrl}${page.path}`}${renderThemeFragment(themeRef.current, displayMode, page.scrollable, wrapContentRef.current)}`,
		[baseUrl, page.path, page.frameUrl, displayMode, page.scrollable],
	);
	const [contentHeight, setContentHeight] = useState<number>();
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
		frameRef.current?.contentWindow?.postMessage(renderThemeMessage(themeRef.current, displayMode, page.scrollable, wrapContent), "*");
	useEffect(() => {
		postTheme();
	}, [theme, wrapContent]);
	useLayoutEffect(() => {
		const onMessage = (event: MessageEvent) => {
			const frame = frameRef.current;
			if (!frame || event.source !== frame.contentWindow) return;
			const height = readRenderContentHeight(event.data);
			if (height !== undefined) {
				setContentHeight(height);
				return;
			}
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

async function fetchRender(path: string, signal?: AbortSignal): Promise<Response> {
	const response = await fetch(`${getApiBaseUrl()}${path}`, { signal });
	if (!response.ok) throw new Error(`render ${path}: HTTP ${response.status}`);
	return response;
}

async function saveRender(page: FramePage) {
	const url = URL.createObjectURL(await (await fetchRender(page.path)).blob());
	const link = document.createElement("a");
	link.href = url;
	link.download = page.fileName;
	link.click();
	setTimeout(() => URL.revokeObjectURL(url), 0);
}

async function saveRenderAsArtifact(render: RenderRef): Promise<string> {
	const sessionId = decodeURIComponent(render.path.split("/")[4] ?? "");
	const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/renders/{renderId}/artifact", {
		params: { path: { sessionId, renderId: render.id } },
		body: { title: render.title },
	});
	if (!data) throw apiErrorException(error, "save render as artifact failed");
	return data.name;
}

function RenderSource({ path }: { path: string }) {
	const { t } = useTranslation();
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

function OpenInPanelAction({ sessionId, path, onOpen }: { sessionId: string; path: string; onOpen: () => void }) {
	const { t } = useTranslation();
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	return (
		<RenderAction
			label={t("chat.artifact.openInPanel")}
			onClick={() => {
				onOpen();
				requestWorkspaceFileOpen(sessionId, path, undefined, "artifact");
			}}
		>
			<FileText className="size-3.5" />
		</RenderAction>
	);
}

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
	const artifactPreviewUrl = "artifact" in props && links?.previewUrl && /^https?:\/\//.test(links.previewUrl) ? links.previewUrl : undefined;
	const standaloneUrl = (): string | undefined => "artifact" in props
		? artifactPreviewUrl
		: `${getApiBaseUrl()}${page.path}${renderThemeFragment(readRenderTheme(), "fullscreen", false, wrapContent)}`;
	const opensStandalone = !("artifact" in props) || artifactPreviewUrl !== undefined;
	const openStandalone = (open: (url: string) => void) => {
		const url = standaloneUrl();
		if (url) open(url);
	};
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
			{"artifact" in props ? (
				<div className="flex h-8 items-center gap-1.5 text-xs" data-testid="artifact-card-header">
					<FileText aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
					<span className="shrink-0 font-medium text-foreground">{t("chat.artifact.label")}</span>
					<span className="min-w-0 flex-1 truncate text-muted-foreground">{page.title}</span>
					{links && "artifact" in props ? (
						<OpenInPanelAction sessionId={links.sessionId} path={props.artifact.path} onOpen={() => undefined} />
					) : null}
				</div>
			) : null}
			<RenderDocument page={page} displayMode="inline" wrapContent={wrapContent} onOpenImage={openImage} />
			<div
				className={cn(
					"absolute end-1 flex gap-1 opacity-0 transition-opacity group-hover/render:opacity-100 focus-within:opacity-100",
					"artifact" in props ? "top-9" : "top-1",
				)}
			>
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
					className="z-overlay w-[min(64rem,calc(100vw-4rem))] max-w-none gap-2 border-0 p-2 outline-none"
					onOpenAutoFocus={(event) => {
						event.preventDefault();
						(event.currentTarget as HTMLElement).focus();
					}}
				>
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
						{render ? (
							<RenderAction label={t("chat.render.saveAsArtifact")} disabled={savingArtifact} onClick={() => saveArtifact(render)}>
								<FilePlus className="size-3.5" />
							</RenderAction>
						) : null}
						{links && "artifact" in props ? (
							<OpenInPanelAction sessionId={links.sessionId} path={props.artifact.path} onOpen={() => setExpanded(false)} />
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
						<RenderSource path={page.path} />
					) : (
						<RenderDocument page={page} displayMode="fullscreen" wrapContent={wrapContent} onOpenImage={openImage} className="max-h-[calc(100svh-8rem)]" />
					)}
				</DialogContent>
			</Dialog>
			{image ? <ChatImageDialog open={imageOpen} onOpenChange={setImageOpen} url={image.src} label={image.alt} /> : null}
		</div>
	);
}
