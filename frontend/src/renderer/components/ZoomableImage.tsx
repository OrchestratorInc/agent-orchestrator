import { useEffect, useRef, type ImgHTMLAttributes } from "react";
import type { ArtifactZoomController } from "../lib/artifact-zoom";

/**
 * An image fit to its box that the artifact zoom scales with a transform:
 * a pinch zooms around the cursor, and once zoomed a two-finger scroll or a
 * drag pans it. Pan is clamped to the image as fitted (object-contain leaves
 * letterbox space), so its edges never pass the viewer's, and an axis where
 * the zoomed image still fits stays centered.
 */
export function ZoomableImage({ controller, ...props }: ImgHTMLAttributes<HTMLImageElement> & { controller: ArtifactZoomController }) {
	const boxRef = useRef<HTMLDivElement>(null);
	const imageRef = useRef<HTMLImageElement>(null);

	useEffect(() => {
		const box = boxRef.current;
		const image = imageRef.current;
		if (!box || !image) return;
		let zoom = controller.getZoom();
		let x = 0;
		let y = 0;
		// How far the image's center may move from the box's center on each axis.
		const room = () => {
			// The image's drawn size is its natural size fitted into the box, until it has loaded.
			const width = box.clientWidth;
			const height = box.clientHeight;
			const fit = image.naturalWidth && image.naturalHeight ? Math.min(width / image.naturalWidth, height / image.naturalHeight) : 0;
			const drawnWidth = fit ? image.naturalWidth * fit : width;
			const drawnHeight = fit ? image.naturalHeight * fit : height;
			return { maxX: Math.max(0, (drawnWidth * zoom - width) / 2), maxY: Math.max(0, (drawnHeight * zoom - height) / 2) };
		};
		const pannable = () => {
			const { maxX, maxY } = room();
			return maxX > 0 || maxY > 0;
		};
		const clamp = () => {
			const { maxX, maxY } = room();
			x = Math.max(-maxX, Math.min(maxX, x));
			y = Math.max(-maxY, Math.min(maxY, y));
		};
		const paint = () => {
			image.style.transform = zoom === 1 && x === 0 && y === 0 ? "" : `translate3d(${x}px, ${y}px, 0) scale(${zoom})`;
			box.style.cursor = pannable() ? "grab" : "";
		};
		const onZoom = () => {
			const next = controller.getZoom();
			if (next === zoom) return;
			const anchor = controller.takeAnchor();
			const rect = box.getBoundingClientRect();
			// The anchor relative to the box's center keeps the point under it in place.
			const px = anchor ? anchor.clientX - rect.left - rect.width / 2 : 0;
			const py = anchor ? anchor.clientY - rect.top - rect.height / 2 : 0;
			const ratio = next / zoom;
			x = px - ratio * (px - x);
			y = py - ratio * (py - y);
			zoom = next;
			clamp();
			paint();
		};
		const onWheel = (event: WheelEvent) => {
			// A pinch (ctrlKey) is the viewer's; a plain scroll pans a zoomed image.
			if (event.ctrlKey || !pannable()) return;
			event.preventDefault();
			x -= event.deltaX;
			y -= event.deltaY;
			clamp();
			paint();
		};
		let drag: { pointerId: number; x: number; y: number } | null = null;
		const onPointerDown = (event: PointerEvent) => {
			if (!pannable() || event.button !== 0) return;
			event.preventDefault();
			drag = { pointerId: event.pointerId, x: event.clientX, y: event.clientY };
			box.setPointerCapture(event.pointerId);
			box.style.cursor = "grabbing";
		};
		const onPointerMove = (event: PointerEvent) => {
			if (!drag || drag.pointerId !== event.pointerId) return;
			x += event.clientX - drag.x;
			y += event.clientY - drag.y;
			drag = { ...drag, x: event.clientX, y: event.clientY };
			clamp();
			paint();
		};
		const onPointerUp = (event: PointerEvent) => {
			if (!drag || drag.pointerId !== event.pointerId) return;
			drag = null;
			paint();
		};
		const refit = () => { clamp(); paint(); };
		const resize = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(refit);
		resize?.observe(box);
		// A remount (a rewritten file or a fresh cloud link keys a new image)
		// keeps the viewer's zoom, so the new image starts painted at it.
		refit();
		const unsubscribe = controller.subscribe(onZoom);
		image.addEventListener("load", refit);
		box.addEventListener("wheel", onWheel, { passive: false });
		box.addEventListener("pointerdown", onPointerDown);
		box.addEventListener("pointermove", onPointerMove);
		box.addEventListener("pointerup", onPointerUp);
		box.addEventListener("pointercancel", onPointerUp);
		return () => {
			unsubscribe();
			resize?.disconnect();
			image.removeEventListener("load", refit);
			box.removeEventListener("wheel", onWheel);
			box.removeEventListener("pointerdown", onPointerDown);
			box.removeEventListener("pointermove", onPointerMove);
			box.removeEventListener("pointerup", onPointerUp);
			box.removeEventListener("pointercancel", onPointerUp);
			image.style.transform = "";
			box.style.cursor = "";
		};
	}, [controller]);

	return (
		<div className="relative size-full min-h-0 min-w-0 overflow-hidden" ref={boxRef}>
			<img {...props} className="size-full min-h-0 min-w-0 origin-center object-contain" draggable={false} ref={imageRef} />
		</div>
	);
}
