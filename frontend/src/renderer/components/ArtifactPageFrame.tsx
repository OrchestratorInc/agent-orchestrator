import { useContext, useEffect, useRef } from "react";
import { useArtifactZoomTarget, wheelZoomFactor, ZOOM_CONTROLS_REVEAL_PX } from "../lib/artifact-zoom";
import { readRenderLinkRequest } from "../lib/render-frame";
import { AppBrowserLinkContext } from "./AppLink";

type ZoomRequest = { action?: unknown; deltaMode?: unknown; deltaY?: unknown; reveal?: unknown; x?: unknown; y?: unknown };

function readZoomRequest(data: unknown): ZoomRequest | undefined {
	if (typeof data !== "object" || data === null) return undefined;
	const message = data as Record<string, unknown>;
	if (message.jsonrpc !== "2.0" || message.method !== "ao/zoom") return undefined;
	return typeof message.params === "object" && message.params !== null ? message.params as ZoomRequest : undefined;
}

/**
 * An HTML artifact on its isolated inline origin. The page is cross-origin, so
 * its pinch and zoom keys reach the viewer through the render bootstrap
 * (render_bootstrap.js), and the viewer's zoom goes back to it to apply as
 * browser zoom, around the pinch point. The page also reports the pointer
 * nearing its bottom edge, where the zoom controls float, to bring them back.
 * A link the reader clicks in the page is cancelled there and handed to the
 * viewer, which opens it in the session's Browser tab, as other links in a
 * session do, or in the OS browser where no session is in view.
 */
export function ArtifactPageFrame({ src, title, className = "absolute inset-0 h-full w-full border-0 bg-background" }: { src: string; title: string; className?: string }) {
	const frameRef = useRef<HTMLIFrameElement>(null);
	const { controller } = useArtifactZoomTarget();
	const openInAppBrowser = useContext(AppBrowserLinkContext);
	const openInAppBrowserRef = useRef(openInAppBrowser);
	openInAppBrowserRef.current = openInAppBrowser;

	useEffect(() => {
		const onMessage = (event: MessageEvent) => {
			const frame = frameRef.current;
			if (!frame || event.source !== frame.contentWindow) return;
			const url = readRenderLinkRequest(event.data);
			if (!url) return;
			// Only while the reader is using this frame: a page can post on load.
			if (document.activeElement !== frame || navigator.userActivation?.isActive === false) return;
			if (openInAppBrowserRef.current) openInAppBrowserRef.current(url);
			else window.open(url, "_blank", "noopener,noreferrer");
		};
		window.addEventListener("message", onMessage);
		return () => window.removeEventListener("message", onMessage);
	}, []);

	useEffect(() => {
		const frame = frameRef.current;
		if (!frame || !controller) return;
		const send = (anchor?: { clientX: number; clientY: number }) => {
			const rect = frame.getBoundingClientRect();
			frame.contentWindow?.postMessage({
				jsonrpc: "2.0",
				method: "ui/notifications/host-context-changed",
				params: {
					zoomable: true,
					zoomRevealPx: ZOOM_CONTROLS_REVEAL_PX,
					zoom: controller.getZoom(),
					...(anchor ? { zoomAnchor: { x: anchor.clientX - rect.left, y: anchor.clientY - rect.top } } : {}),
				},
			}, "*");
		};
		const onLoad = () => send();
		const onMessage = (event: MessageEvent) => {
			if (event.source !== frame.contentWindow) return;
			const request = readZoomRequest(event.data);
			if (!request) return;
			if (request.reveal === true) controller.reveal();
			else if (request.action === "in") controller.step(1);
			else if (request.action === "out") controller.step(-1);
			else if (request.action === "reset") controller.reset();
			else if (typeof request.deltaY === "number" && typeof request.x === "number" && typeof request.y === "number") {
				const rect = frame.getBoundingClientRect();
				controller.zoomBy(
					wheelZoomFactor({ deltaY: request.deltaY, deltaMode: request.deltaMode === 1 ? 1 : 0 }),
					{ clientX: rect.left + request.x, clientY: rect.top + request.y },
				);
			}
		};
		const unsubscribe = controller.subscribe(() => send(controller.takeAnchor()));
		frame.addEventListener("load", onLoad);
		window.addEventListener("message", onMessage);
		send();
		return () => {
			unsubscribe();
			frame.removeEventListener("load", onLoad);
			window.removeEventListener("message", onMessage);
		};
	}, [controller]);

	return (
		<iframe
			className={className}
			ref={frameRef}
			sandbox="allow-scripts allow-same-origin"
			src={src}
			title={title}
		/>
	);
}
