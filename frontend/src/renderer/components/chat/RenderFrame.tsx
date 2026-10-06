import { Maximize2 } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { getApiBaseUrl } from "../../lib/api-client";
import {
	clampRenderHeight,
	readRenderContentHeight,
	readRenderLinkRequest,
	readRenderTheme,
	renderThemeFragment,
	renderThemeMessage,
	renderThemesEqual,
	type RenderTheme,
} from "../../lib/render-frame";
import { cn } from "../../lib/utils";
import type { RenderRef } from "../../types/conversation";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogTitle } from "../ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

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
 */
function RenderDocument({ render, fit, className }: { render: RenderRef; fit?: boolean; className?: string }) {
	const theme = useRenderTheme();
	const frameRef = useRef<HTMLIFrameElement>(null);
	const themeRef = useRef(theme);
	themeRef.current = theme;
	const [src] = useState(() => `${getApiBaseUrl()}${render.path}${renderThemeFragment(theme)}`);
	const [contentHeight, setContentHeight] = useState<number>();
	const postTheme = () => frameRef.current?.contentWindow?.postMessage(renderThemeMessage(themeRef.current), "*");
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
			style={fit ? { height: clampRenderHeight(contentHeight ?? render.height) } : undefined}
		/>
	);
}

export function RenderFrame({ render }: { render: RenderRef }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	return (
		<div className="group/render relative min-w-0">
			<RenderDocument render={render} fit />
			<Tooltip>
				<TooltipTrigger asChild>
					<Button
						variant="ghost"
						size="icon-sm"
						aria-label={t("chat.render.expand")}
						className="absolute end-1 top-1 opacity-0 transition-opacity group-hover/render:opacity-100 focus-visible:opacity-100"
						onClick={() => setExpanded(true)}
					>
						<Maximize2 className="size-3.5" />
					</Button>
				</TooltipTrigger>
				<TooltipContent>{t("chat.render.expand")}</TooltipContent>
			</Tooltip>
			<Dialog open={expanded} onOpenChange={setExpanded}>
				<DialogContent
					aria-describedby={undefined}
					className="z-overlay flex h-[calc(100svh-6rem)] w-[calc(100vw-6rem)] max-w-none flex-col gap-2 p-2 pt-10"
				>
					<DialogTitle className="sr-only">{render.title}</DialogTitle>
					{expanded ? <RenderDocument render={render} className="min-h-0 flex-1" /> : null}
				</DialogContent>
			</Dialog>
		</div>
	);
}
